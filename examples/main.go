package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	neo "github.com/Protocol-Lattice/neo"
)

type NoInput struct{}

type HealthcheckOutput struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
}

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

type UserEvent struct {
	Topic string `json:"topic"`
	Name  string `json:"name"`
	Data  User   `json:"data"`
}

func main() {
	ctx := context.Background()

	app := neo.NewRouter()
	users := neo.NewRouter()

	app.Use(loggingMiddleware("app"), timingMiddleware)
	users.Use(loggingMiddleware("users"))

	app.Register("healthcheck", neo.Query[NoInput, HealthcheckOutput](func(ctx context.Context, input NoInput) (HealthcheckOutput, error) {
		return HealthcheckOutput{
			OK:      true,
			Service: "neo",
		}, nil
	}))

	users.Register("getByID", neo.Query[GetUserInput, User](func(ctx context.Context, input GetUserInput) (User, error) {
		return User{ID: input.ID, Name: "Kamil"}, nil
	}))

	users.Register("create", neo.Mutation[CreateUserInput, User](func(ctx context.Context, input CreateUserInput) (User, error) {
		if input.Name == "" {
			return User{}, fmt.Errorf("input.name must be a non-empty string")
		}

		created := User{ID: 2, Name: input.Name}

		app.Events().Publish("user.changes", UserEvent{
			Topic: "user.changes",
			Name:  "user.created",
			Data:  created,
		})

		return created, nil
	}))

	users.RegisterSubscription("changes", neo.Subscription[NoInput, UserEvent](func(ctx context.Context, input NoInput) (<-chan UserEvent, error) {
		raw := app.Events().Subscribe(ctx, "user.changes")
		out := make(chan UserEvent)

		go func() {
			defer close(out)
			for event := range raw {
				userEvent, ok := event.(UserEvent)
				if !ok {
					continue
				}

				select {
				case <-ctx.Done():
					return
				case out <- userEvent:
				}
			}
		}()

		return out, nil
	}))

	app.Nested("user", users)

	mux := http.NewServeMux()
	app.ServeHTTP(mux, "/neo/")

	server := httptest.NewServer(mux)
	defer server.Close()

	log.Printf("neo server listening on %s/neo", server.URL)

	client := NewTypedClient(server.URL+"/neo", neo.WithBinaryCodec())

	health, err := client.Healthcheck.Query(ctx, NoInput{})
	if err != nil {
		log.Fatalf("healthcheck query failed: %v", err)
	}
	fmt.Printf("healthcheck: %#v\n", health)

	foundUser, err := client.User.GetByID.Query(ctx, GetUserInput{ID: 1})
	if err != nil {
		log.Fatalf("user.getByID query failed: %v", err)
	}
	fmt.Printf("user.getByID: %#v\n", foundUser)

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	changes, err := client.User.Changes.Subscribe(streamCtx, NoInput{})
	if err != nil {
		log.Fatalf("user.changes subscription failed: %v", err)
	}

	createdUser, err := client.User.Create.Mutate(ctx, CreateUserInput{Name: "Raezil"})
	if err != nil {
		log.Fatalf("user.create mutation failed: %v", err)
	}
	fmt.Printf("user.create: %#v\n", createdUser)

	select {
	case change := <-changes:
		fmt.Printf("user.changes event from mutation: %#v\n", change)
	case <-time.After(time.Second):
		log.Fatal("timed out waiting for user.changes event")
	}
}

func loggingMiddleware(name string) neo.Middleware {
	return func(next neo.Handler) neo.Handler {
		return func(ctx context.Context, input any) (any, error) {
			log.Printf("middleware=%s input=%#v", name, input)
			return next(ctx, input)
		}
	}
}

func timingMiddleware(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		started := time.Now()
		out, err := next(ctx, input)
		log.Printf("duration=%s error=%v", time.Since(started), err)
		return out, err
	}
}
