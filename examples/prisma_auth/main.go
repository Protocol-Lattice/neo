package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	neo "github.com/Protocol-Lattice/neo"
	prismadb "github.com/Protocol-Lattice/neo/examples/prisma_auth/db"
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

	datasourceURL, err := exampleDatasourceURL()
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
	if err := ensureSQLiteSchema(ctx, prisma); err != nil {
		log.Fatalf("init sqlite schema: %v", err)
	}
	if err := seedExampleData(ctx, store); err != nil {
		log.Fatalf("seed example data: %v", err)
	}

	router := buildRouter(store)
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	server := httptest.NewServer(authContextMiddleware(store, mux))
	defer server.Close()

	client := NewTypedClient(server.URL+"/neo", neo.WithBinaryCodec())
	if _, err := client.User.Me.Query(ctx, NoInput{}); err != nil {
		fmt.Printf("unauthenticated user.me: %v\n", err)
	}

	registered, err := client.Auth.Register.Mutate(ctx, RegisterInput{
		Email:    "raezil@example.com",
		Name:     "Raezil",
		Password: "secret123",
	})
	if err != nil {
		log.Fatalf("auth.register failed: %v", err)
	}
	fmt.Printf("auth.register: token=%t user.email=%q\n", registered.Token != "", registered.User.Email)

	login, err := client.Auth.Login.Query(ctx, LoginInput{
		User:     "kamil@example.com",
		Password: "secret123",
	})
	if err != nil {
		log.Fatalf("auth.login failed: %v", err)
	}
	fmt.Printf("auth.login: token=%t user.email=%q\n", login.Token != "", login.User.Email)

	authenticatedClient := NewTypedClient(
		server.URL+"/neo",
		neo.WithBinaryCodec(),
		neo.WithHeader("Authorization", "Bearer "+registered.Token),
	)

	me, err := authenticatedClient.User.Me.Query(ctx, NoInput{})
	if err != nil {
		log.Fatalf("authenticated user.me failed: %v", err)
	}
	fmt.Printf("authenticated user.me: email=%q name=%q\n", me.Email, me.Name)
}

func buildRouter(store *prismaAuthStore) *neo.Router {
	router := neo.NewRouter()
	auth := neo.NewRouter()
	protected := neo.NewRouter()

	auth.Register("login", neo.Query[LoginInput, AuthOutput](func(ctx context.Context, input LoginInput) (AuthOutput, error) {
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

	auth.Register("register", neo.Mutation[RegisterInput, AuthOutput](func(ctx context.Context, input RegisterInput) (AuthOutput, error) {
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

	protected.Use(requireUser)

	protected.Register("me", neo.Query[NoInput, User](func(ctx context.Context, _ NoInput) (User, error) {
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

	router.Nested("auth", auth)
	router.Nested("user", protected)
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

func (store *prismaAuthStore) deleteUserByEmail(ctx context.Context, email string) error {
	user, err := store.userByEmail(ctx, email)
	if prismadb.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
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
	for _, email := range []string{"kamil@example.com", "raezil@example.com"} {
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

func exampleDatasourceURL() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	exampleRoot, err := findExampleRoot(cwd)
	if err != nil {
		return "", err
	}

	dbPath := filepath.Join(exampleRoot, "prisma", "dev.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", fmt.Errorf("create prisma directory: %w", err)
	}

	return "file:" + filepath.ToSlash(dbPath), nil
}

func findExampleRoot(start string) (string, error) {
	for dir := start; ; dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, "prisma", "schema.prisma")) {
			return dir, nil
		}

		candidate := filepath.Join(dir, "examples", "prisma_auth")
		if fileExists(filepath.Join(candidate, "prisma", "schema.prisma")) {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}

	return "", fmt.Errorf("could not find examples/prisma_auth/prisma/schema.prisma from %s", start)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureSQLiteSchema(ctx context.Context, client *prismadb.PrismaClient) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS "User" (
			"id" TEXT NOT NULL PRIMARY KEY,
			"email" TEXT NOT NULL,
			"name" TEXT NOT NULL,
			"passwordHash" TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "User_email_key" ON "User"("email")`,
		`CREATE TABLE IF NOT EXISTS "AuthToken" (
			"id" TEXT NOT NULL PRIMARY KEY,
			"token" TEXT NOT NULL,
			"userID" TEXT NOT NULL,
			CONSTRAINT "AuthToken_userID_fkey" FOREIGN KEY ("userID") REFERENCES "User" ("id") ON DELETE CASCADE ON UPDATE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "AuthToken_token_key" ON "AuthToken"("token")`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "AuthToken_userID_key" ON "AuthToken"("userID")`,
	}

	for _, statement := range statements {
		if _, err := client.Prisma.ExecuteRaw(statement).Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}
