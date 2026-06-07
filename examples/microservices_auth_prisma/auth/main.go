package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	neo "github.com/Protocol-Lattice/neo"
	prismadb "github.com/Protocol-Lattice/neo/examples/microservices_auth_prisma/db"
	"github.com/Protocol-Lattice/neo/examples/microservices_auth_prisma/internal/dbutil"
	"golang.org/x/crypto/bcrypt"
)

type NoInput struct{}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type LoginInput struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

type RegisterInput struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type AuthOutput struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type userIDContextKey struct{}

type prismaAuthStore struct {
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

	store := &prismaAuthStore{client: prisma}
	if err := dbutil.EnsureSQLiteSchema(ctx, prisma); err != nil {
		log.Fatalf("init sqlite schema: %v", err)
	}
	if err := seedExampleData(ctx, store); err != nil {
		log.Fatalf("seed example data: %v", err)
	}

	router := buildRouter(store)
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	addr := env("AUTH_ADDR", ":8091")
	server := &http.Server{
		Addr:         addr,
		Handler:      authContextMiddleware(store, mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("auth service listening on %s", localServiceURL(addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildRouter(store *prismaAuthStore) *neo.Router {
	router := neo.NewRouter()

	router.Register("login", neo.Query[LoginInput, AuthOutput](func(ctx context.Context, input LoginInput) (AuthOutput, error) {
		userIdentifier := strings.TrimSpace(input.User)
		password := input.Password
		if userIdentifier == "" {
			return AuthOutput{}, neo.NewError(neo.CodeBadRequest, "user is required")
		}
		if password == "" {
			return AuthOutput{}, neo.NewError(neo.CodeBadRequest, "password is required")
		}

		user, err := store.userByEmail(ctx, userIdentifier)
		if prismadb.IsErrNotFound(err) {
			return AuthOutput{}, neo.NewError(neo.CodeUnauthorized, "invalid credentials")
		}
		if err != nil {
			return AuthOutput{}, err
		}

		if !passwordMatches(user.PasswordHash, password) {
			return AuthOutput{}, neo.NewError(neo.CodeUnauthorized, "invalid credentials")
		}

		return store.authOutputForUser(ctx, user)
	}))

	router.Register("register", neo.Mutation[RegisterInput, AuthOutput](func(ctx context.Context, input RegisterInput) (AuthOutput, error) {
		email := strings.TrimSpace(input.Email)
		name := strings.TrimSpace(input.Name)
		password := input.Password
		if email == "" {
			return AuthOutput{}, neo.NewError(neo.CodeBadRequest, "email is required")
		}
		if name == "" {
			return AuthOutput{}, neo.NewError(neo.CodeBadRequest, "name is required")
		}
		if len(password) < 8 {
			return AuthOutput{}, neo.NewError(neo.CodeBadRequest, "password must be at least 8 characters")
		}

		passwordHash, err := hashPassword(password)
		if err != nil {
			return AuthOutput{}, err
		}

		user, err := store.createUser(ctx, email, name, passwordHash)
		if err != nil {
			if _, ok := prismadb.IsErrUniqueConstraint(err); ok {
				return AuthOutput{}, neo.NewError(neo.CodeConflict, "email is already registered")
			}
			return AuthOutput{}, err
		}

		token, err := newAuthToken()
		if err != nil {
			return AuthOutput{}, err
		}
		if err := store.createAuthToken(ctx, user.ID, token); err != nil {
			return AuthOutput{}, err
		}

		return AuthOutput{Token: token, User: publicUser(user)}, nil
	}))

	router.Use(requireUser)

	router.Register("me", neo.Query[NoInput, User](func(ctx context.Context, _ NoInput) (User, error) {
		userID, _ := userIDFromContext(ctx)

		user, err := store.userByID(ctx, userID)
		if prismadb.IsErrNotFound(err) {
			return User{}, neo.NewError(neo.CodeNotFound, "user not found")
		}
		if err != nil {
			return User{}, err
		}

		return publicUser(user), nil
	}))

	return router
}

func (store *prismaAuthStore) authOutputForUser(ctx context.Context, user *prismadb.UserModel) (AuthOutput, error) {
	token, err := store.authTokenForUserID(ctx, user.ID)
	if prismadb.IsErrNotFound(err) {
		return AuthOutput{}, neo.NewError(neo.CodeUnauthorized, "invalid credentials")
	}
	if err != nil {
		return AuthOutput{}, err
	}

	return AuthOutput{Token: token, User: publicUser(user)}, nil
}

func authContextMiddleware(store *prismaAuthStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		userID, err := store.userIDForAuthToken(r.Context(), token)
		if err == nil && userID != "" {
			ctx := context.WithValue(r.Context(), userIDContextKey{}, userID)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

func requireUser(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		if userID, ok := userIDFromContext(ctx); !ok || userID == "" {
			return nil, neo.NewError(neo.CodeUnauthorized, "unauthorized")
		}

		return next(ctx, input)
	}
}

func userIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDContextKey{}).(string)
	return userID, ok
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func (store *prismaAuthStore) userIDForAuthToken(ctx context.Context, token string) (string, error) {
	authToken, err := store.client.AuthToken.FindUnique(
		prismadb.AuthToken.Token.Equals(token),
	).Exec(ctx)
	if err != nil {
		return "", err
	}

	return authToken.UserID, nil
}

func (store *prismaAuthStore) userByID(ctx context.Context, userID string) (*prismadb.UserModel, error) {
	return store.client.User.FindUnique(
		prismadb.User.ID.Equals(userID),
	).Exec(ctx)
}

func (store *prismaAuthStore) userByEmail(ctx context.Context, email string) (*prismadb.UserModel, error) {
	return store.client.User.FindUnique(
		prismadb.User.Email.Equals(email),
	).Exec(ctx)
}

func (store *prismaAuthStore) createUser(ctx context.Context, email string, name string, passwordHash string, optional ...prismadb.UserSetParam) (*prismadb.UserModel, error) {
	return store.client.User.CreateOne(
		prismadb.User.Email.Set(email),
		prismadb.User.Name.Set(name),
		prismadb.User.PasswordHash.Set(passwordHash),
		optional...,
	).Exec(ctx)
}

func (store *prismaAuthStore) authTokenForUserID(ctx context.Context, userID string) (string, error) {
	authToken, err := store.client.AuthToken.FindUnique(
		prismadb.AuthToken.UserID.Equals(userID),
	).Exec(ctx)
	if err != nil {
		return "", err
	}

	return authToken.Token, nil
}

func (store *prismaAuthStore) createAuthToken(ctx context.Context, userID string, token string) error {
	_, err := store.client.AuthToken.CreateOne(
		prismadb.AuthToken.Token.Set(token),
		prismadb.AuthToken.User.Link(prismadb.User.ID.Equals(userID)),
	).Exec(ctx)
	return err
}

func (store *prismaAuthStore) deleteAuthTokenByUserID(ctx context.Context, userID string) error {
	_, err := store.client.AuthToken.FindUnique(
		prismadb.AuthToken.UserID.Equals(userID),
	).Delete().Exec(ctx)
	if prismadb.IsErrNotFound(err) {
		return nil
	}

	return err
}

func (store *prismaAuthStore) deleteUserByEmail(ctx context.Context, email string) error {
	user, err := store.userByEmail(ctx, email)
	if prismadb.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := store.deleteAuthTokenByUserID(ctx, user.ID); err != nil {
		return err
	}

	_, err = store.client.User.FindUnique(
		prismadb.User.ID.Equals(user.ID),
	).Delete().Exec(ctx)
	if prismadb.IsErrNotFound(err) {
		return nil
	}

	return err
}

func seedExampleData(ctx context.Context, store *prismaAuthStore) error {
	for _, email := range []string{"kamil@example.com"} {
		if err := store.deleteUserByEmail(ctx, email); err != nil {
			return err
		}
	}

	passwordHash, err := hashPassword("secret123")
	if err != nil {
		return err
	}

	user, err := store.createUser(
		ctx,
		"kamil@example.com",
		"Kamil",
		passwordHash,
		prismadb.User.ID.Set("user_1"),
	)
	if err != nil {
		return err
	}

	return store.createAuthToken(ctx, user.ID, "demo-token")
}

func publicUser(user *prismadb.UserModel) User {
	return User{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	}
}

func hashPassword(password string) (string, error) {
	rawHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	return string(rawHash), nil
}

func passwordMatches(passwordHash string, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) == nil
}

func newAuthToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}

	return "auth_" + hex.EncodeToString(raw), nil
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
