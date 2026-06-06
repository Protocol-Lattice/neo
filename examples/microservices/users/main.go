package main

import (
	"context"
	"log"
	"sync"

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

func main() {
	router := neo.NewRouter()
	store := newUserStore()

	router.Register("getByID", neo.Query(func(ctx context.Context, input GetUserInput) (User, error) {
		user, ok := store.get(input.ID)
		if !ok {
			return User{}, neo.NewError(neo.CodeNotFound, "user not found")
		}
		return user, nil
	}))

	router.Register("create", neo.Mutation(func(ctx context.Context, input CreateUserInput) (User, error) {
		if input.Name == "" {
			return User{}, neo.NewError(neo.CodeBadRequest, "name is required")
		}
		return store.create(input.Name), nil
	}))

	log.Println("users service listening on http://localhost:8081/neo")
	if err := router.ListenAndServe(neo.ServerOptions{
		Addr:   ":8081",
		Prefix: "/neo/",
	}); err != nil {
		log.Fatal(err)
	}
}

type userStore struct {
	mu     sync.Mutex
	nextID int
	users  map[int]User
}

func newUserStore() *userStore {
	return &userStore{
		nextID: 1,
		users: map[int]User{
			1: {ID: 1, Name: "Kamil"},
		},
	}
}

func (s *userStore) get(id int) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]
	return user, ok
}

func (s *userStore) create(name string) User {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	user := User{ID: s.nextID, Name: name}
	s.users[user.ID] = user
	return user
}
