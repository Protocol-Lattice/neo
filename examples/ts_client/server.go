package main

import (
	"context"
	"log"
	"net/http"
	"sync"

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
	router := neo.NewRouter()
	users := neo.NewRouter()

	router.UseCORS(neo.CORSOptions{
		AllowedOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "Accept"},
	})

	var (
		mu     sync.RWMutex
		nextID = 2
		store  = map[int]User{
			1: {ID: 1, Name: "Kamil"},
		}
	)

	router.Register("healthcheck", neo.Query[NoInput, HealthcheckOutput](func(context.Context, NoInput) (HealthcheckOutput, error) {
		return HealthcheckOutput{OK: true, Service: "neo"}, nil
	}))

	users.Register("getByID", neo.Query[GetUserInput, User](func(_ context.Context, input GetUserInput) (User, error) {
		mu.RLock()
		defer mu.RUnlock()

		user, ok := store[input.ID]
		if !ok {
			return User{}, neo.NewError(neo.CodeNotFound, "user not found")
		}

		return user, nil
	}))

	users.Register("create", neo.Mutation[CreateUserInput, User](func(_ context.Context, input CreateUserInput) (User, error) {
		if input.Name == "" {
			return User{}, neo.NewError(neo.CodeBadRequest, "name is required")
		}

		mu.Lock()
		user := User{ID: nextID, Name: input.Name}
		store[user.ID] = user
		nextID++
		mu.Unlock()

		router.Events().Publish("user.changes", UserEvent{
			Topic: "user.changes",
			Name:  "user.created",
			Data:  user,
		})

		return user, nil
	}))

	users.RegisterSubscription("changes", neo.Subscription[NoInput, UserEvent](func(ctx context.Context, _ NoInput) (<-chan UserEvent, error) {
		raw := router.Events().Subscribe(ctx, "user.changes")
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

	router.Nested("user", users)

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	log.Println("Neo Go server listening on http://localhost:8080/neo")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
