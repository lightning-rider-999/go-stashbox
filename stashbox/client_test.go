package stashbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientEndpointNormalisation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantURL string
		wantErr bool
	}{
		{"base host adds graphql", "https://stashdb.org", "https://stashdb.org/graphql", false},
		{"root path adds graphql", "https://stashdb.org/", "https://stashdb.org/graphql", false},
		{"already graphql kept", "https://stashdb.org/graphql", "https://stashdb.org/graphql", false},
		{"trailing slash on graphql kept", "https://stashdb.org/graphql/", "https://stashdb.org/graphql", false},
		{"http preserved", "http://localhost:9998", "http://localhost:9998/graphql", false},
		{"custom subpath preserved", "https://host/stashbox", "https://host/stashbox/graphql", false},
		// Query and fragment are meaningless for a GraphQL POST; they are stripped.
		{"query stripped from endpoint", "https://stashdb.org/?foo=bar", "https://stashdb.org/graphql", false},
		{"fragment stripped from endpoint", "https://stashdb.org/graphql#frag", "https://stashdb.org/graphql", false},
		{"query and fragment stripped", "https://host/stashbox?a=1&b=2#top", "https://host/stashbox/graphql", false},
		// Surrounding whitespace is trimmed before parsing.
		{"leading and trailing whitespace trimmed", "  https://stashdb.org  ", "https://stashdb.org/graphql", false},
		{"newline whitespace trimmed", "\thttps://stashdb.org\n", "https://stashdb.org/graphql", false},
		{"no scheme rejected", "stashdb.org", "", true},
		{"empty rejected", "", "", true},
		{"whitespace-only rejected", "   ", "", true},
		{"no host rejected", "https://", "", true},
		{"unsupported scheme rejected", "ftp://stashdb.org", "", true},
		// stash-box has no subscriptions; ws/wss are not GraphQL endpoints here.
		{"ws scheme rejected", "ws://stashdb.org", "", true},
		{"wss scheme rejected", "wss://stashdb.org", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(WithURL(tc.raw), WithAPIKey("k"))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got none", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := c.Endpoint(); got != tc.wantURL {
				t.Errorf("Endpoint() = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

func TestClientInjectsAPIKeyHeader(t *testing.T) {
	var gotKey, gotPath, gotMethod string
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		gotKey = r.Header.Get("ApiKey")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v0.10.0","hash":"abc","build_time":"now","build_type":"OFFICIAL"}}}`)
	}))
	defer srv.Close()

	c, err := NewClient(WithURL(srv.URL), WithAPIKey("secret-key-123"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c.GraphQL()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if seen != 1 {
		t.Fatalf("server saw %d requests, want 1", seen)
	}
	if gotKey != "secret-key-123" {
		t.Errorf("ApiKey header = %q, want %q", gotKey, "secret-key-123")
	}
	if gotPath != "/graphql" {
		t.Errorf("request path = %q, want /graphql", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
}

func TestClientNoAPIKeyOmitsHeader(t *testing.T) {
	var hadKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadKey = r.Header["Apikey"]
		if v := r.Header.Get("ApiKey"); v != "" {
			hadKey = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v","hash":"h","build_time":"b","build_type":"OFFICIAL"}}}`)
	}))
	defer srv.Close()

	c, err := NewClient(WithURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c.GraphQL()); err != nil {
		t.Fatal(err)
	}
	if hadKey {
		t.Error("ApiKey header present when no key configured")
	}
}

func TestClientWithHTTPClientPreservesTimeout(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	c, err := NewClient(WithURL("https://h/graphql"), WithAPIKey("k"), WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.HTTPClient().Timeout; got != 7*time.Second {
		t.Errorf("timeout = %v, want 7s (WithHTTPClient must not be overridden)", got)
	}
	// The returned client must still inject the ApiKey via a wrapped transport.
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("ApiKey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"version":{"version":"v","hash":"h","build_time":"b","build_type":"OFFICIAL"}}}`)
	}))
	defer srv.Close()
	c2, err := NewClient(WithURL(srv.URL), WithAPIKey("wrapped-key"), WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), c2.GraphQL()); err != nil {
		t.Fatal(err)
	}
	if gotKey != "wrapped-key" {
		t.Errorf("ApiKey header = %q via WithHTTPClient, want wrapped-key", gotKey)
	}
}

func TestClientDefaultTimeout(t *testing.T) {
	c, err := NewClient(WithURL("https://h"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPClient().Timeout == 0 {
		t.Error("default http client has no timeout; want a bounded default")
	}
	c2, err := NewClient(WithURL("https://h"), WithAPIKey("k"), WithTimeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.HTTPClient().Timeout; got != 3*time.Second {
		t.Errorf("WithTimeout = %v, want 3s", got)
	}
}

func TestClientEnvFallback(t *testing.T) {
	t.Setenv("STASHBOX_URL", "https://env.host:1234")
	t.Setenv("STASHBOX_API_KEY", "env-key")
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Endpoint(); got != "https://env.host:1234/graphql" {
		t.Errorf("Endpoint from env = %q", got)
	}
	if got := c.APIKey(); got != "env-key" {
		t.Errorf("APIKey from env = %q", got)
	}
	// Explicit option must win over env.
	c2, err := NewClient(WithURL("https://explicit.host"), WithAPIKey("explicit"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.Endpoint(); got != "https://explicit.host/graphql" {
		t.Errorf("explicit option did not win: %q", got)
	}
	if got := c2.APIKey(); got != "explicit" {
		t.Errorf("explicit key did not win: %q", got)
	}
}

func TestClientExplicitEmptyKeySuppressesEnv(t *testing.T) {
	t.Setenv("STASHBOX_API_KEY", "env-key")
	// WithAPIKey("") records that the key was set explicitly, so the env fallback
	// must not override it — the caller is opting into unauthenticated access.
	c, err := NewClient(WithURL("https://h"), WithAPIKey(""))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.APIKey(); got != "" {
		t.Errorf("explicit empty key was overridden by env: %q", got)
	}
}

func TestClientMissingURLErrors(t *testing.T) {
	t.Setenv("STASHBOX_URL", "")
	t.Setenv("STASHBOX_API_KEY", "")
	_, err := NewClient()
	if err == nil {
		t.Fatal("want error when no URL configured")
	}
	// The missing-URL condition is a configuration mistake the caller can fix, not
	// an opaque failure: it must be a recognisable sentinel so the CLI can map it
	// to a usage exit code and a library caller can errors.Is it.
	if !errors.Is(err, ErrNoURL) {
		t.Errorf("error %v is not ErrNoURL", err)
	}
}

func TestClientLoggerNeverNil(t *testing.T) {
	c, err := NewClient(WithURL("https://h"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Logger() == nil {
		t.Fatal("Logger() returned nil")
	}
	// A nil WithLogger must still leave a non-nil logger (slog.Default).
	c2, err := NewClient(WithURL("https://h"), WithAPIKey("k"), WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c2.Logger() == nil {
		t.Fatal("Logger() returned nil after WithLogger(nil)")
	}
	// A custom logger is honoured.
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	c3, err := NewClient(WithURL("https://h"), WithAPIKey("k"), WithLogger(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c3.Logger() != custom {
		t.Error("WithLogger did not set the supplied logger")
	}
}

func TestClientLogValueRedactsAPIKey(t *testing.T) {
	const secret = "TOP-SECRET-KEY-DO-NOT-LEAK"
	c, err := NewClient(WithURL("https://stashdb.org"), WithAPIKey(secret))
	if err != nil {
		t.Fatal(err)
	}
	// Render exactly how slog would when c is passed as an attr value.
	rendered := fmt.Sprintf("%v", c.LogValue())
	if strings.Contains(rendered, secret) {
		t.Fatalf("api key leaked into log value: %q", rendered)
	}
	if !strings.Contains(rendered, "REDACTED") {
		t.Errorf("expected REDACTED marker in %q", rendered)
	}

	// And through a real slog handler, the key must never appear.
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("client", "stashbox", c)
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("api key leaked through slog handler: %q", buf.String())
	}
}

func TestClientLogValueNoKeyNoMarker(t *testing.T) {
	c, err := NewClient(WithURL("https://stashdb.org"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf("%v", c.LogValue())
	if strings.Contains(rendered, "api_key") {
		t.Errorf("unconfigured key should not emit an api_key attr: %q", rendered)
	}
}

func TestClientLogValueMasksUserinfo(t *testing.T) {
	const password = "hunter2-do-not-leak"
	c, err := NewClient(WithURL("https://admin:"+password+"@stashdb.org:9999"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}

	// The endpoint itself still carries the credential (it must, to authenticate),
	// but LogValue must mask the whole userinfo component before logging.
	if !strings.Contains(c.Endpoint(), password) {
		t.Fatalf("test precondition: endpoint should carry the password, got %q", c.Endpoint())
	}

	rendered := fmt.Sprintf("%v", c.LogValue())
	if strings.Contains(rendered, password) {
		t.Fatalf("password leaked into log value: %q", rendered)
	}
	if strings.Contains(rendered, "admin") {
		t.Errorf("username leaked into log value: %q", rendered)
	}
	if !strings.Contains(rendered, "xxxxx@stashdb.org:9999") {
		t.Errorf("expected masked userinfo in %q", rendered)
	}

	// Through a real slog handler, neither the username nor the password appears.
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("client", "stashbox", c)
	if strings.Contains(buf.String(), password) {
		t.Fatalf("password leaked through slog handler: %q", buf.String())
	}
}
