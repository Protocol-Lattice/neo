package dbutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	prismadb "github.com/Protocol-Lattice/neo/examples/microservices_auth_prisma/db"
)

// DatasourceURL returns the SQLite datasource URL for this example, regardless
// of whether the command is run from the repository root or an example subdir.
func DatasourceURL() (string, error) {
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

// EnsureSQLiteSchema creates the local SQLite tables used by the example.
// It keeps the example runnable without a separate migration step.
func EnsureSQLiteSchema(ctx context.Context, client *prismadb.PrismaClient) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS "users" (
			"id" TEXT NOT NULL PRIMARY KEY,
			"email" TEXT NOT NULL,
			"name" TEXT NOT NULL,
			"passwordHash" TEXT NOT NULL,
			"createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			"updatedAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "users_email_key" ON "users"("email")`,
		`CREATE TABLE IF NOT EXISTS "auth_tokens" (
			"id" TEXT NOT NULL PRIMARY KEY,
			"token" TEXT NOT NULL,
			"userID" TEXT NOT NULL,
			"createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT "auth_tokens_userID_fkey" FOREIGN KEY ("userID") REFERENCES "users" ("id") ON DELETE CASCADE ON UPDATE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "auth_tokens_token_key" ON "auth_tokens"("token")`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "auth_tokens_userID_key" ON "auth_tokens"("userID")`,
		`CREATE TABLE IF NOT EXISTS "orders" (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"userID" TEXT NOT NULL,
			"sku" TEXT NOT NULL,
			"quantity" INTEGER NOT NULL,
			"createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT "orders_userID_fkey" FOREIGN KEY ("userID") REFERENCES "users" ("id") ON DELETE CASCADE ON UPDATE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS "orders_userID_idx" ON "orders"("userID")`,
	}

	for _, statement := range statements {
		if _, err := client.Prisma.ExecuteRaw(statement).Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}

func findExampleRoot(start string) (string, error) {
	for dir := start; ; dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, "prisma", "schema.prisma")) {
			return dir, nil
		}

		candidate := filepath.Join(dir, "examples", "microservices_auth_prisma")
		if fileExists(filepath.Join(candidate, "prisma", "schema.prisma")) {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}

	return "", fmt.Errorf("could not find examples/microservices_auth_prisma/prisma/schema.prisma from %s", start)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
