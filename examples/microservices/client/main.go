package main

import (
	"context"
	"fmt"
	"log"
	"os"

	neo "github.com/Protocol-Lattice/neo"
)

type GetUserInput struct {
	ID int `json:"id"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type CreateUserInput struct {
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
	ctx := context.Background()
	gatewayURL := env("GATEWAY_URL", "http://localhost:8080/neo")
	client := NewTypedClient(gatewayURL, neo.WithBinaryCodec())

	user, err := client.Users.GetByID.Query(ctx, GetUserInput{ID: 1})
	if err != nil {
		log.Fatalf("users.getByID failed: %v", err)
	}

	order, err := client.Orders.Create.Mutate(ctx, CreateOrderInput{UserID: user.ID, SKU: "neo-sticker", Quantity: 2})
	if err != nil {
		log.Fatalf("orders.create failed: %v", err)
	}

	fmt.Printf("user:  %#v\n", user)
	fmt.Printf("order: %#v\n", order)
}

func env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
