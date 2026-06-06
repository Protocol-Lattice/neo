package main

import (
	"log"
	"os"

	neo "github.com/Protocol-Lattice/neo"
)

type GetUserInput struct {
	ID int `json:"id"`
}

type CreateUserInput struct {
	Name string `json:"name"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type CreateOrderInput struct {
	UserID   int    `json:"userID"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type Order struct {
	ID       int    `json:"id"`
	UserID   int    `json:"userID"`
	UserName string `json:"userName"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

func main() {
	usersURL := env("USERS_URL", "http://localhost:8081/neo")
	ordersURL := env("ORDERS_URL", "http://localhost:8082/neo")

	gateway := neo.NewGateway()
	if err := gateway.Proxy("users", usersURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "getByID", Kind: neo.ProcedureKindQuery, Input: "GetUserInput", Output: "User"},
		neo.ProcedureMeta{Key: "create", Kind: neo.ProcedureKindMutation, Input: "CreateUserInput", Output: "User"},
	)); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Proxy("orders", ordersURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "create", Kind: neo.ProcedureKindMutation, Input: "CreateOrderInput", Output: "Order"},
	)); err != nil {
		log.Fatal(err)
	}

	log.Println("gateway listening on http://localhost:8080/neo")
	log.Println("try: curl 'http://localhost:8080/neo/users.getByID?input={\"id\":1}'")
	if err := gateway.ListenAndServe(neo.ServerOptions{
		Addr:   ":8080",
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
