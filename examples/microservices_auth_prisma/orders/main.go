package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	neo "github.com/Protocol-Lattice/neo"
	prismadb "github.com/Protocol-Lattice/neo/examples/microservices_auth_prisma/db"
	"github.com/Protocol-Lattice/neo/examples/microservices_auth_prisma/internal/dbutil"
)

type NoInput struct{}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type CreateOrderInput struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type Order struct {
	ID        int    `json:"id"`
	UserID    string `json:"userID"`
	UserEmail string `json:"userEmail"`
	UserName  string `json:"userName"`
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
}

type userContextKey struct{}

type prismaOrderStore struct {
	client *prismadb.PrismaClient
}

func main() {
	ctx := context.Background()

	datasourceURL, err := dbutil.DatasourceURL()
	if err != nil {
		log.Fatal(err)
	}

	prisma := prismadb.NewClient(prismadb.WithDatasourceURL(datasourceURL))
	if err := prisma.Prisma.Connect(); err != nil {
		log.Fatalf("connect prisma: %v", err)
	}
	defer func() {
		if err := prisma.Prisma.Disconnect(); err != nil {
			log.Printf("disconnect prisma: %v", err)
		}
	}()

	if err := dbutil.EnsureSQLiteSchema(ctx, prisma); err != nil {
		log.Fatalf("init sqlite schema: %v", err)
	}

	store := &prismaOrderStore{client: prisma}
	authURL := env("AUTH_URL", "http://localhost:8091/neo")
	router := buildRouter(store)

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	addr := env("ORDERS_ADDR", ":8092")
	server := &http.Server{
		Addr:         addr,
		Handler:      authContextMiddleware(authURL, mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("orders service listening on %s", localServiceURL(addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildRouter(store *prismaOrderStore) *neo.Router {
	router := neo.NewRouter()
	router.Use(requireUser)

	router.Register("create", neo.Mutation[CreateOrderInput, Order](func(ctx context.Context, input CreateOrderInput) (Order, error) {
		if input.SKU == "" {
			return Order{}, neo.NewError(neo.CodeBadRequest, "sku is required")
		}
		if input.Quantity <= 0 {
			return Order{}, neo.NewError(neo.CodeBadRequest, "quantity must be positive")
		}

		user, _ := userFromContext(ctx)
		return store.createOrder(ctx, user, input)
	}))

	return router
}

func (store *prismaOrderStore) createOrder(ctx context.Context, user User, input CreateOrderInput) (Order, error) {
	order, err := store.client.Order.CreateOne(
		prismadb.Order.Sku.Set(input.SKU),
		prismadb.Order.Quantity.Set(input.Quantity),
		prismadb.Order.User.Link(prismadb.User.ID.Equals(user.ID)),
	).Exec(ctx)
	if err != nil {
		return Order{}, err
	}

	return Order{
		ID:        order.ID,
		UserID:    order.UserID,
		UserEmail: user.Email,
		UserName:  user.Name,
		SKU:       order.Sku,
		Quantity:  order.Quantity,
	}, nil
}

func authContextMiddleware(authURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := authenticateBearer(r.Context(), authURL, r.Header.Get("Authorization")); ok {
			ctx := context.WithValue(r.Context(), userContextKey{}, user)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

func authenticateBearer(ctx context.Context, authURL string, authorization string) (User, bool) {
	token := bearerToken(authorization)
	if token == "" {
		return User{}, false
	}

	client := neo.NewClient(
		authURL,
		neo.WithBinaryCodec(),
		neo.WithHeader("Authorization", "Bearer "+token),
	)
	user, err := neo.CallTyped[NoInput, User](ctx, client.Query.Procedure("me"), NoInput{})
	if err != nil || user.ID == "" {
		return User{}, false
	}

	return user, true
}

func requireUser(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		if user, ok := userFromContext(ctx); !ok || user.ID == "" {
			return nil, neo.NewError(neo.CodeUnauthorized, "unauthorized")
		}

		return next(ctx, input)
	}
}

func userFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey{}).(User)
	return user, ok
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func localServiceURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr + "/neo"
	}
	return "http://" + addr + "/neo"
}
