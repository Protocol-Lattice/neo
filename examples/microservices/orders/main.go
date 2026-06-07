package main

import (
	"context"
	"log"
	"os"
	"sync/atomic"

	neo "github.com/Protocol-Lattice/neo"
)

type GetUserInput struct {
	ID int `json:"id"`
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
	users := neo.NewClient(usersURL, neo.WithBinaryCodec())

	var nextID atomic.Int64
	router := neo.NewRouter()

	router.Register("create", neo.Mutation(func(ctx context.Context, input CreateOrderInput) (Order, error) {
		if input.UserID <= 0 {
			return Order{}, neo.NewError(neo.CodeBadRequest, "userID is required")
		}
		if input.SKU == "" {
			return Order{}, neo.NewError(neo.CodeBadRequest, "sku is required")
		}
		if input.Quantity <= 0 {
			return Order{}, neo.NewError(neo.CodeBadRequest, "quantity must be positive")
		}

		user, err := neo.CallTyped[GetUserInput, User](
			ctx,
			users.Query.Procedure("getByID"),
			GetUserInput{ID: input.UserID},
		)
		if err != nil {
			return Order{}, err
		}

		return Order{
			ID:       int(nextID.Add(1)),
			UserID:   user.ID,
			UserName: user.Name,
			SKU:      input.SKU,
			Quantity: input.Quantity,
		}, nil
	}))

	log.Println("orders service listening on http://localhost:8082/neo")
	if err := router.ListenAndServe(neo.ServerOptions{
		Addr:   ":8082",
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
