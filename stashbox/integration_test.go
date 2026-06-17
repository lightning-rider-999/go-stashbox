//go:build integration

// Package stashbox integration test. It runs only under `-tags integration` and
// only when both STASHBOX_URL and STASHBOX_API_KEY are set in the environment.
// It is deliberately read-only and non-destructive: stash-box (StashDB) is a
// shared, community-run service, so this exercises just the version handshake
// and one small, bounded query — never a mutation, never an unbounded fan-out.
package stashbox_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

// integrationClient builds a client from the environment, skipping the test when
// the credentials/URL are not configured so a default `go test -tags integration`
// run is a no-op rather than a failure.
func integrationClient(t *testing.T) *stashbox.Client {
	t.Helper()
	if os.Getenv("STASHBOX_URL") == "" || os.Getenv("STASHBOX_API_KEY") == "" {
		t.Skip("set STASHBOX_URL and STASHBOX_API_KEY to run the integration smoke test")
	}
	c, err := stashbox.NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestIntegrationVersion performs the version handshake against the live server
// and checks its release against the generated schema. A version difference is
// logged, not failed: the client must still work across patch/minor drift.
func TestIntegrationVersion(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if info.Version == "" {
		t.Error("server reported an empty version")
	}
	t.Logf("server: version=%s hash=%s build_time=%s build_type=%s",
		info.Version, info.Hash, info.BuildTime, info.BuildType)

	compatible, server, err := c.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("CheckCompatibility: %v", err)
	}
	t.Logf("compatible=%v server=%s", compatible, server.Version)
}

// TestIntegrationSmallQuery runs one small, bounded read against the live server
// to confirm the transport, auth header, and a generated operation all work
// end to end. SearchPerformer with a tiny limit keeps the request small.
func TestIntegrationSmallQuery(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	limit := 1
	resp, err := stashbox.SearchPerformer(ctx, c.GraphQL(), "test", &limit)
	if err != nil {
		t.Fatalf("SearchPerformer: %v", err)
	}
	t.Logf("SearchPerformer returned %d result(s)", len(resp.SearchPerformer))
}
