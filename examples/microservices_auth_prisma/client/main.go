package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

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
	ctx := context.Background()
	gatewayURL := env("GATEWAY_URL", "http://localhost:8090/neo")
	client := NewTypedClient(gatewayURL)

	if _, err := client.Orders.Create.Mutate(ctx, CreateOrderInput{SKU: "neo-sticker", Quantity: 2}); err != nil {
		fmt.Printf("unauthenticated orders.create: %v\n", err)
	}

	registered, err := client.Auth.Register.Mutate(ctx, RegisterInput{
		Email:    fmt.Sprintf("raezil+%d@example.com", time.Now().UnixNano()),
		Name:     "Raezil",
		Password: "secret123",
	})
	if err != nil {
		log.Fatalf("auth.register failed: %v", err)
	}
	fmt.Printf("auth.register: token=%t user.email=%q\n", registered.Token != "", registered.User.Email)

	login, err := client.Auth.Login.Query(ctx, LoginInput{
		User:     registered.User.Email,
		Password: "secret123",
	})
	if err != nil {
		log.Fatalf("auth.login failed: %v", err)
	}
	fmt.Printf("auth.login: token=%t user.email=%q\n", login.Token != "", login.User.Email)

	authenticated := NewTypedClient(
		gatewayURL,
		neo.WithHeader("Authorization", "Bearer "+login.Token),
	)

	me, err := authenticated.Auth.Me.Query(ctx, NoInput{})
	if err != nil {
		log.Fatalf("auth.me failed: %v", err)
	}
	fmt.Printf("auth.me: email=%q name=%q\n", me.Email, me.Name)

	order, err := authenticated.Orders.Create.Mutate(ctx, CreateOrderInput{SKU: "neo-sticker", Quantity: 2})
	if err != nil {
		log.Fatalf("orders.create failed: %v", err)
	}
	fmt.Printf("orders.create: id=%d user.email=%q sku=%q quantity=%d\n", order.ID, order.UserEmail, order.SKU, order.Quantity)
}

func env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
