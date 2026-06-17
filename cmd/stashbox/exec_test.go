package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

// newTestClient builds a stashbox.Client pointed at a test server that returns
// the given GraphQL response body for every request.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*stashbox.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := stashbox.NewClient(stashbox.WithURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestRunOperationSuccess(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"findScene":{"id":"42","title":"Test Scene"}}}`)
	})

	spec := commandSpec{OpName: "FindScene", Query: "query FindScene {}", Kind: "query", ReturnType: "Scene"}
	var out bytes.Buffer
	if err := runOperation(context.Background(), c, spec, nil, "json", &out); err != nil {
		t.Fatalf("runOperation: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	scene, _ := got["findScene"].(map[string]any)
	if scene["title"] != "Test Scene" {
		t.Errorf("rendered output = %s, want a findScene with title Test Scene", out.String())
	}
}

func TestRunOperationGraphQLErrorClassifies(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		// HTTP 200 with an errors array -> a *stashbox.GraphQLError.
		_, _ = io.WriteString(w, `{"errors":[{"message":"scene not found"}],"data":null}`)
	})

	spec := commandSpec{OpName: "FindScene", Query: "query FindScene {}", Kind: "query", ReturnType: "Scene"}
	err := runOperation(context.Background(), c, spec, nil, "json", io.Discard)
	if err == nil {
		t.Fatal("expected an error from a GraphQL errors response")
	}
	if got := classifyExit(err); got != ExitNotFound {
		t.Errorf("classifyExit = %+v, want ExitNotFound", got)
	}
}

func TestRunOperationTransportErrorClassifies(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "upstream is down")
	})

	spec := commandSpec{OpName: "Version", Query: "query Version {}", Kind: "query", ReturnType: "Version"}
	err := runOperation(context.Background(), c, spec, nil, "json", io.Discard)
	if err == nil {
		t.Fatal("expected an error from a 502 response")
	}
	if got := classifyExit(err); got != ExitTransport {
		t.Errorf("classifyExit = %+v, want ExitTransport", got)
	}
}

// TestLeafRunEEndToEnd drives a full leaf command RunE (variables -> client ->
// render) through a fixed in-memory client, the seam tests use instead of env
// vars.
func TestLeafRunEEndToEnd(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Echo the variables back so the test can assert they reached the wire.
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"id":"99"`) {
			t.Errorf("request did not carry id=99: %s", body)
		}
		_, _ = io.WriteString(w, `{"data":{"findScene":{"id":"99"}}}`)
	})

	spec := commandSpec{
		Path: []string{"scene", "get"}, OpName: "FindScene",
		Query: "query FindScene($id: ID!) {}", Kind: "query", ReturnType: "Scene",
	}
	leaf := newLeafCommandWithClient(spec, c)
	leaf.Flags().String("input", "", "")
	leaf.Flags().String("output", "json", "")

	var out bytes.Buffer
	leaf.SetOut(&out)
	leaf.SetIn(strings.NewReader(`{"id":"99"}`))
	if err := leaf.Flags().Set("input", "-"); err != nil {
		t.Fatal(err)
	}

	if err := leaf.RunE(leaf, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), `"id": "99"`) {
		t.Errorf("rendered output = %s, want findScene id 99", out.String())
	}
}
