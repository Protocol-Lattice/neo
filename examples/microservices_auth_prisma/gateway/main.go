package main

import (
	"log"
	"os"

	neo "github.com/Protocol-Lattice/neo"
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

func main() {
	authURL := env("AUTH_URL", "http://localhost:8091/neo")
	ordersURL := env("ORDERS_URL", "http://localhost:8092/neo")

	gateway := neo.NewGateway()
	if err := gateway.Proxy("auth", authURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "login", Kind: neo.ProcedureKindQuery, Input: "LoginInput", Output: "AuthOutput"},
		neo.ProcedureMeta{Key: "register", Kind: neo.ProcedureKindMutation, Input: "RegisterInput", Output: "AuthOutput"},
		neo.ProcedureMeta{Key: "me", Kind: neo.ProcedureKindQuery, Input: "NoInput", Output: "User"},
	)); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Proxy("orders", ordersURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "create", Kind: neo.ProcedureKindMutation, Input: "CreateOrderInput", Output: "Order"},
	)); err != nil {
		log.Fatal(err)
	}

	addr := env("GATEWAY_ADDR", ":8090")
	log.Printf("gateway listening on http://localhost%s/neo", addr)
	log.Println("try: curl 'http://localhost:8090/neo/auth.login?input={\"user\":\"kamil@example.com\",\"password\":\"secret123\"}'")
	if err := gateway.ListenAndServe(neo.ServerOptions{
		Addr:   addr,
		Prefix: "/neo/",
	}); err != nil {
		log.Fatal(err)
	}
}

func env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
