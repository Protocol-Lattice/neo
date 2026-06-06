package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	neo "github.com/Protocol-Lattice/neo"
	"google.golang.org/grpc/test/bufconn"
)

type SumInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

type SumOutput struct {
	Sum int `json:"sum"`
}

func main() {
	ctx := context.Background()

	router := neo.NewRouter()
	router.Register("sum", neo.Query(func(ctx context.Context, input SumInput) (SumOutput, error) {
		return SumOutput{Sum: input.A + input.B}, nil
	}))

	client, cleanup := newBufConnClient(router)
	defer cleanup()

	output, err := neo.CallTyped[SumInput, SumOutput](
		ctx,
		client.Query.Procedure("sum"),
		SumInput{A: 40, B: 2},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("sum: %d\n", output.Sum)
}

func newBufConnClient(router *neo.Router) (*neo.Client, func()) {
	const bufferSize = 1 << 20

	listener := bufconn.Listen(bufferSize)

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	server := &http.Server{Handler: mux}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		},
	}

	cleanup := func() {
		transport.CloseIdleConnections()
		if err := server.Close(); err != nil {
			log.Printf("close bufconn server: %v", err)
		}
		_ = listener.Close()

		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			log.Printf("bufconn server stopped: %v", err)
		}
	}

	client := neo.NewClient(
		"http://neo.local/neo",
		neo.WithHTTPClient(&http.Client{Transport: transport}),
	)

	return client, cleanup
}
