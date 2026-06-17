package stashbox_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

// ExampleNewClient builds a client from explicit options. The URL is normalised
// to address the GraphQL endpoint, so passing the base UI URL is enough.
func ExampleNewClient() {
	c, err := stashbox.NewClient(
		stashbox.WithURL("https://stashdb.org"),
		stashbox.WithAPIKey("your-api-key"),
		stashbox.WithTimeout(30*time.Second),
	)
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	fmt.Println(c.Endpoint())
	// Output: https://stashdb.org/graphql
}

// ExampleNewClient_environment shows that the URL and API key fall back to the
// STASHBOX_URL and STASHBOX_API_KEY environment variables when no option sets
// them. Here an explicit option is still used so the example is deterministic,
// and a trailing slash is normalised away.
func ExampleNewClient_environment() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org/"))
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	fmt.Println(c.Endpoint())
	// Output: https://stashdb.org/graphql
}

// ExampleClient_GraphQL runs a generated operation. Client.GraphQL returns the
// genqlient client that every generated function takes as its second argument.
// The call is wired exactly as it would run against a live server; it is not
// executed here, so no Output line asserts a server response.
func ExampleClient_GraphQL() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}

	ctx := context.Background()

	// Find a performer by id. The id is the operation's own typed input.
	resp, err := stashbox.FindPerformer(ctx, c.GraphQL(), "performer-uuid")
	if err != nil {
		// See ExampleNewErrorEnvelope for classifying the error.
		return
	}
	if resp.FindPerformer != nil {
		fmt.Println(resp.FindPerformer.Id, resp.FindPerformer.Name)
	}
}

// ExampleClient_Version performs the version handshake: it asks the server for
// its build identity. The call is shown without execution against a live
// server, so it carries no Output line.
func ExampleClient_Version() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}

	info, err := c.Version(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("stash-box %s (%s)\n", info.Version, info.Hash)
}

// ExampleClient_CheckCompatibility reports whether the server's release matches
// the schema version this library was generated against. A mismatch is not an
// error: it returns compatible=false with the server's reported version so the
// caller decides how to proceed.
func ExampleClient_CheckCompatibility() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}

	compatible, server, err := c.CheckCompatibility(context.Background())
	if err != nil {
		return
	}
	if !compatible {
		fmt.Printf("server %s differs from the generated schema\n", server.Version)
	}
}

// ExampleBatch runs work over a slice of items with bounded concurrency. At
// most three calls run at once; the first error cancels the rest and is
// returned. There is no retry, by design — a well-behaved client against a
// shared, community-run stash-box must not mask a failure.
func ExampleBatch() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}

	ids := []string{"1", "2", "3", "4", "5"}

	err = stashbox.Batch(context.Background(), 3, ids, func(ctx context.Context, id string) error {
		_, opErr := stashbox.FindPerformer(ctx, c.GraphQL(), id)
		return opErr
	})
	if err != nil {
		fmt.Println("batch failed:", err)
	}
}

// ExampleBatchResults collects a result per item, returned in input order
// regardless of completion order. The concurrency, cancellation, and no-retry
// behaviour match Batch.
func ExampleBatchResults() {
	doubled, err := stashbox.BatchResults(
		context.Background(),
		4,
		[]int{1, 2, 3, 4},
		func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		},
	)
	if err != nil {
		return
	}
	fmt.Println(doubled)
	// Output: [2 4 6 8]
}

// ExampleNewErrorEnvelope classifies a returned error and inspects the error
// types the library produces. A *GraphQLError carries the server's message list;
// a *TransportError carries the HTTP status; ErrUnauthorized is matched with
// errors.Is. stash-box returns an auth failure as the bare message "not
// authorized" at HTTP 200, which Classify still maps to ErrUnauthorized.
func ExampleNewErrorEnvelope() {
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}

	_, err = c.Version(context.Background())
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, stashbox.ErrUnauthorized):
		fmt.Println("check the API key")
	default:
		var gqlErr *stashbox.GraphQLError
		var transportErr *stashbox.TransportError
		switch {
		case errors.As(err, &gqlErr):
			fmt.Println("server rejected the query:", gqlErr.Messages())
		case errors.As(err, &transportErr):
			fmt.Println("transport failure, HTTP status:", transportErr.StatusCode)
		}
	}

	// NewErrorEnvelope maps any error to the JSON shape the CLI emits.
	env := stashbox.NewErrorEnvelope(err)
	fmt.Println("retryable:", env.Retryable)
}

// ExampleClassify maps a raw GraphQL error from a generated operation into the
// typed error model. Here the "not authorized" message stash-box returns is
// classified as ErrUnauthorized even though it carries no extensions.code.
func ExampleClassify() {
	// In real code, err is whatever a generated operation returned.
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		return
	}
	_, opErr := stashbox.Me(context.Background(), c.GraphQL())
	if opErr == nil {
		return
	}
	if errors.Is(stashbox.Classify(opErr), stashbox.ErrUnauthorized) {
		fmt.Println("not authorized — set STASHBOX_API_KEY")
	}
}

// ExampleWithHTTPClient supplies a custom *http.Client. Its own timeout and
// settings are kept; only the transport is wrapped so the ApiKey header is
// still injected.
func ExampleWithHTTPClient() {
	hc := &http.Client{Timeout: 10 * time.Second}

	c, err := stashbox.NewClient(
		stashbox.WithURL("https://stashdb.org"),
		stashbox.WithAPIKey("your-api-key"),
		stashbox.WithHTTPClient(hc),
		stashbox.WithLogger(slog.Default()),
	)
	if err != nil {
		return
	}
	_ = c.HTTPClient()
}
