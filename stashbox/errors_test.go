package stashbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// errorClient runs a Version call against an httptest server that responds with
// the given status code and body, then classifies the resulting error.
func errorClient(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(WithURL(srv.URL), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := Version(context.Background(), c.GraphQL())
	return classify(callErr)
}

func TestErrorsGraphQLList(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"unknown field","path":["version"]}]}`)
	if err == nil {
		t.Fatal("want error")
	}
	var gqlErr *GraphQLError
	if !errors.As(err, &gqlErr) {
		t.Fatalf("want *GraphQLError via errors.As, got %T", err)
	}
	if len(gqlErr.Errors) != 1 || gqlErr.Errors[0].Message != "unknown field" {
		t.Errorf("unexpected errors payload: %+v", gqlErr.Errors)
	}
	if !strings.Contains(gqlErr.Error(), "unknown field") {
		t.Errorf("Error() = %q, want it to mention the message", gqlErr.Error())
	}
	// The underlying gqlerror.List must remain reachable through the chain.
	if _, ok := errors.AsType[gqlerror.List](err); !ok {
		t.Error("underlying gqlerror.List not reachable via errors.As")
	}
}

func TestErrorsAuthFromGraphQL(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"not authenticated","extensions":{"code":"UNAUTHENTICATED"}}]}`)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("auth-shaped GraphQL error not classified as ErrUnauthorized: %v", err)
	}
	// It is still a GraphQLError.
	if _, ok := errors.AsType[*GraphQLError](err); !ok {
		t.Errorf("auth GraphQL error should still be *GraphQLError, got %T", err)
	}
}

// TestErrorsAuthFromStashBoxLivePayload mirrors stash-box's actual on-the-wire
// shape for an auth failure. Verified firsthand against stashapp/stash-box:
// internal/auth/authorization.go declares
//
//	var ErrUnauthorized = errors.New("not authorized")
//
// which resolvers return; internal/api/server.go's SetErrorPresenter uses
// gqlgen's DefaultErrorPresenter and adds NO extensions.code. So the failure
// arrives as HTTP 200 with body {"errors":[{"message":"not authorized"}]} and
// no code. The classifier must still yield ErrUnauthorized (the previous matcher
// checked "unauthorized" without a space and missed "not authorized").
func TestErrorsAuthFromStashBoxLivePayload(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"not authorized"}]}`)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stash-box live 'not authorized' payload (HTTP 200, no code) not classified as ErrUnauthorized: %v", err)
	}
	// It is still a *GraphQLError carrying the original message.
	gqlErr, ok := errors.AsType[*GraphQLError](err)
	if !ok {
		t.Fatalf("want *GraphQLError, got %T", err)
	}
	if len(gqlErr.Errors) != 1 || gqlErr.Errors[0].Message != "not authorized" {
		t.Errorf("unexpected errors payload: %+v", gqlErr.Errors)
	}
	// The envelope must carry the UNAUTHORIZED code, matching the wrap chain.
	env := NewErrorEnvelope(err)
	if env.Code != "UNAUTHORIZED" {
		t.Errorf("env.Code = %q, want UNAUTHORIZED", env.Code)
	}
	if env.Retryable {
		t.Error("an auth failure must not be retryable")
	}
}

// TestErrorsAuthRequiresAuthentication covers the other message phrasing the
// matcher accepts, for stash-box instances or proxies that word the failure as
// "requires authentication" (still HTTP 200, no extensions.code).
func TestErrorsAuthRequiresAuthentication(t *testing.T) {
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"This endpoint requires authentication"}]}`)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("'requires authentication' message not classified as ErrUnauthorized: %v", err)
	}
}

func TestErrorsNonAuthCodeNotMisclassified(t *testing.T) {
	// A structured non-auth code is authoritative even when the message text
	// mentions "forbidden"; the error must NOT be classified as ErrUnauthorized.
	err := errorClient(t, 200, `{"data":null,"errors":[{"message":"forbidden word in input","extensions":{"code":"BAD_USER_INPUT"}}]}`)
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("non-auth code with 'forbidden' in message was misclassified as auth: %v", err)
	}
	if _, ok := errors.AsType[*GraphQLError](err); !ok {
		t.Errorf("want *GraphQLError, got %T", err)
	}
}

func TestErrorsAuthFromStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := errorClient(t, status, `{"errors":[{"message":"denied"}]}`)
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("status %d not classified as ErrUnauthorized: %v", status, err)
		}
		var te *TransportError
		if !errors.As(err, &te) {
			t.Errorf("status %d should surface a *TransportError, got %T", status, err)
		} else if te.StatusCode != status {
			t.Errorf("TransportError.StatusCode = %d, want %d", te.StatusCode, status)
		}
	}
}

func TestErrorsTransportNon2xx(t *testing.T) {
	err := errorClient(t, http.StatusInternalServerError, `{"errors":[{"message":"boom"}]}`)
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("500 should surface a *TransportError, got %T", err)
	}
	if te.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", te.StatusCode, http.StatusInternalServerError)
	}
	if !te.Retryable() {
		t.Error("500 should be retryable")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("500 must not be classified as auth")
	}
}

func TestErrorsRetryableClassification(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
	}
	for _, tc := range cases {
		err := errorClient(t, tc.status, `{"errors":[{"message":"x"}]}`)
		var te *TransportError
		if !errors.As(err, &te) {
			t.Fatalf("status %d should surface a *TransportError, got %T", tc.status, err)
		}
		if te.Retryable() != tc.retryable {
			t.Errorf("status %d: Retryable() = %v, want %v", tc.status, te.Retryable(), tc.retryable)
		}
	}
}

func TestErrorsNetworkFailure(t *testing.T) {
	c, err := NewClient(WithURL("http://127.0.0.1:1/graphql"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := Version(context.Background(), c.GraphQL())
	classified := classify(callErr)
	var te *TransportError
	if !errors.As(classified, &te) {
		t.Fatalf("connection refused should be a *TransportError, got %T", classified)
	}
	if te.StatusCode != 0 {
		t.Errorf("network failure StatusCode = %d, want 0", te.StatusCode)
	}
	// The original error must remain unwrappable.
	if errors.Unwrap(te) == nil {
		t.Error("TransportError must wrap the underlying network error")
	}
}

func TestErrorsContextCancelledNotRetryable(t *testing.T) {
	c, err := NewClient(WithURL("http://127.0.0.1:1/graphql"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, callErr := Version(ctx, c.GraphQL())
	classified := classify(callErr)
	var te *TransportError
	if !errors.As(classified, &te) {
		t.Fatalf("cancelled request should be a *TransportError, got %T", classified)
	}
	if te.Retryable() {
		t.Error("a user-cancelled request must not be retryable")
	}
}

func TestErrorsClassifyNil(t *testing.T) {
	if got := classify(nil); got != nil {
		t.Errorf("classify(nil) = %v, want nil", got)
	}
}

func TestErrorsWrapChainUnwraps(t *testing.T) {
	base := gqlerror.List{{Message: "x"}}
	wrapped := fmt.Errorf("outer: %w", classify(base))
	if _, ok := errors.AsType[*GraphQLError](wrapped); !ok {
		t.Fatal("wrapped GraphQLError not reachable via errors.As")
	}
}

func TestNewTransportError(t *testing.T) {
	te := NewTransportError(http.StatusServiceUnavailable, errors.New("down"))
	if te.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d", te.StatusCode)
	}
	if !te.Retryable() {
		t.Error("503 should be retryable")
	}
	if !strings.Contains(te.Error(), "503") {
		t.Errorf("Error() = %q, want it to mention the status", te.Error())
	}
}

func TestEnvelopeSurfacesHTTPGraphQLErrors(t *testing.T) {
	// A non-2xx response that still carries a structured GraphQL "errors" array.
	// genqlient surfaces it on *graphql.HTTPError.Response.Errors; classify wraps
	// that in a *TransportError, and NewErrorEnvelope must lift the messages into
	// env.GraphQLErrors rather than dropping them.
	err := errorClient(t, http.StatusInternalServerError, `{"errors":[{"message":"boom"}]}`)
	if err == nil {
		t.Fatal("want error")
	}

	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("500 should surface a *TransportError, got %T", err)
	}
	if te.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", te.StatusCode)
	}

	// The embedded *graphql.HTTPError must remain reachable through the chain,
	// with its Response.Errors intact.
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("embedded *graphql.HTTPError not reachable via errors.As, got %T", err)
	}
	if len(httpErr.Response.Errors) != 1 || httpErr.Response.Errors[0].Message != "boom" {
		t.Errorf("unexpected embedded GraphQL errors: %+v", httpErr.Response.Errors)
	}

	env := NewErrorEnvelope(err)
	if len(env.GraphQLErrors) != 1 || env.GraphQLErrors[0] != "boom" {
		t.Errorf("env.GraphQLErrors = %v, want [boom]", env.GraphQLErrors)
	}
	if env.Code != "TRANSPORT" {
		t.Errorf("env.Code = %q, want TRANSPORT", env.Code)
	}
	if !env.Retryable {
		t.Error("env.Retryable = false, want true for a 500")
	}
}

func TestErrorEnvelopeFromError(t *testing.T) {
	// GraphQL error -> messages populated.
	gqlErr := classify(gqlerror.List{{Message: "a"}, {Message: "b"}})
	env := NewErrorEnvelope(gqlErr)
	if len(env.GraphQLErrors) != 2 {
		t.Errorf("envelope GraphQLErrors = %v, want 2 messages", env.GraphQLErrors)
	}
	if env.Message == "" {
		t.Error("envelope Message empty")
	}
	if env.Code != "GRAPHQL" {
		t.Errorf("env.Code = %q, want GRAPHQL", env.Code)
	}

	// Auth error -> retryable false, code UNAUTHORIZED, message present.
	authEnv := NewErrorEnvelope(fmt.Errorf("wrap: %w", ErrUnauthorized))
	if authEnv.Retryable {
		t.Error("auth error should not be retryable")
	}
	if authEnv.Code != "UNAUTHORIZED" {
		t.Errorf("authEnv.Code = %q, want UNAUTHORIZED", authEnv.Code)
	}

	// Nil -> zero envelope, no panic.
	if env := NewErrorEnvelope(nil); env.Message != "" {
		t.Errorf("NewErrorEnvelope(nil).Message = %q, want empty", env.Message)
	}
}

func TestRedactSecretsInErrorMessage(t *testing.T) {
	const secret = "eyJSUPER.SECRET.JWT"
	cases := []struct {
		name string
		in   string
	}{
		{"apikey query", "Post \"https://stashdb.org/graphql?apikey=" + secret + "\": refused"},
		{"api_key query", "dial https://stashdb.org/graphql?api_key=" + secret + "&x=1 failed"},
		{"token query", "GET https://stashdb.org/?token=" + secret + " timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := NewTransportError(0, errors.New(tc.in))
			got := te.Error()
			if strings.Contains(got, secret) {
				t.Fatalf("secret leaked into error message: %q", got)
			}
			if !strings.Contains(got, "REDACTED") {
				t.Errorf("expected REDACTED marker in %q", got)
			}
		})
	}
}

func TestRedactSecretsPreservesNonSecret(t *testing.T) {
	// A benign query parameter must survive untouched.
	te := NewTransportError(0, errors.New("Post \"https://stashdb.org/graphql?page=2&limit=25\": refused"))
	got := te.Error()
	if !strings.Contains(got, "page=2") || !strings.Contains(got, "limit=25") {
		t.Errorf("benign params were altered: %q", got)
	}
}

// TestNormaliseURLErrorOmitsQuerySecret is the MAJOR-2 regression: a credential
// carried as a query parameter in STASHBOX_URL must not leak verbatim into the
// configuration error NewClient returns. The bad-scheme path runs redactURL,
// which now strips query secrets too — not only userinfo.
func TestNormaliseURLErrorOmitsQuerySecret(t *testing.T) {
	const secret = "SECRET"
	// htp:// is an unsupported scheme, so normaliseURL returns the bad-scheme
	// error with the (now redacted) URL embedded.
	_, err := NewClient(WithURL("htp://stashdb.org?apikey=" + secret))
	if err == nil {
		t.Fatal("want error for unsupported scheme")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("apikey query secret leaked into config error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "REDACTED") {
		t.Errorf("expected REDACTED marker in %q", err.Error())
	}
}

// TestNormaliseURLParseFailureOmitsQuerySecret exercises the parse-failure
// branch (a control character makes url.Parse fail): redactURL must still strip
// the query secret via its raw-string fallback rather than returning the URL
// untouched.
func TestNormaliseURLParseFailureOmitsQuerySecret(t *testing.T) {
	const secret = "SECRET"
	// A literal control byte in the URL makes url.Parse return an error.
	_, err := NewClient(WithURL("http://stashdb.org\x7f/graphql?token=" + secret))
	if err == nil {
		t.Fatal("want a parse error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("token query secret leaked through the parse-failure error: %q", err.Error())
	}
}

// TestNormaliseURLParseFailureOmitsUserinfo guards the parse-failure branch
// against a userinfo (user:password@host) leak — not only a query secret. A
// control byte makes url.Parse fail, so redactURL takes its raw-string fallback,
// which must still mask the embedded password. (Regression: the fallback once
// stripped only query parameters and let userinfo through, in both the config
// error and Client.LogValue.)
func TestNormaliseURLParseFailureOmitsUserinfo(t *testing.T) {
	const password = "hunter2-do-not-leak"
	_, err := NewClient(WithURL("http://admin:" + password + "@stashdb.org\x7f/graphql"))
	if err == nil {
		t.Fatal("want a parse error")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("password leaked through the parse-failure error: %q", err.Error())
	}
	if strings.Contains(err.Error(), "admin") {
		t.Errorf("username leaked through the parse-failure error: %q", err.Error())
	}
}

// TestRedactURLUnparseableMasksUserinfo unit-tests the raw-string fallback
// directly across a few authority shapes that url.Parse rejects, including a
// literal '@' in the password (the delimiter is the last '@' in the authority).
func TestRedactURLUnparseableMasksUserinfo(t *testing.T) {
	cases := []string{
		"http://admin:hunter2@stashdb.org\x7f/graphql",
		"http://admin:p@ss@stashdb.org\x7f/graphql",
		"https://user:secret@stashdb.org:9999\x7f/graphql?token=QS",
	}
	for _, in := range cases {
		got := redactURL(in)
		for _, leak := range []string{"hunter2", "secret", "p@ss", "QS"} {
			if strings.Contains(got, leak) {
				t.Errorf("redactURL(%q) leaked %q: %q", in, leak, got)
			}
		}
		if !strings.Contains(got, "xxxxx@") {
			t.Errorf("redactURL(%q) did not mask userinfo: %q", in, got)
		}
	}
}

// TestEnvelopePerMessageRedaction is the MINOR-4 regression: each individual
// GraphQL message surfaced in the envelope (not only the joined Message field)
// must be run through secret redaction.
func TestEnvelopePerMessageRedaction(t *testing.T) {
	const secret = "eyJLEAK.JWT.SECRET"
	gqlErr := classify(gqlerror.List{
		{Message: "failed calling https://stashdb.org/graphql?apikey=" + secret},
	})
	env := NewErrorEnvelope(gqlErr)
	if len(env.GraphQLErrors) != 1 {
		t.Fatalf("env.GraphQLErrors = %v, want one message", env.GraphQLErrors)
	}
	if strings.Contains(env.GraphQLErrors[0], secret) {
		t.Fatalf("secret leaked into env.GraphQLErrors[0]: %q", env.GraphQLErrors[0])
	}
	if !strings.Contains(env.GraphQLErrors[0], "REDACTED") {
		t.Errorf("expected REDACTED marker in %q", env.GraphQLErrors[0])
	}
	// And the joined Message stays redacted too.
	if strings.Contains(env.Message, secret) {
		t.Errorf("secret leaked into env.Message: %q", env.Message)
	}
}

// TestEnvelopeHTTPErrorPerMessageRedaction covers the same per-message scrub on
// the non-2xx path, where the messages come from graphql.HTTPError.Response.
func TestEnvelopeHTTPErrorPerMessageRedaction(t *testing.T) {
	const secret = "eyJLEAK.JWT.SECRET"
	err := errorClient(t, http.StatusInternalServerError,
		`{"errors":[{"message":"upstream https://x/graphql?token=`+secret+` failed"}]}`)
	if err == nil {
		t.Fatal("want error")
	}
	env := NewErrorEnvelope(err)
	if len(env.GraphQLErrors) != 1 {
		t.Fatalf("env.GraphQLErrors = %v, want one message", env.GraphQLErrors)
	}
	if strings.Contains(env.GraphQLErrors[0], secret) {
		t.Fatalf("secret leaked into env.GraphQLErrors[0]: %q", env.GraphQLErrors[0])
	}
}

func TestRedactURLMasksUserinfo(t *testing.T) {
	// A credential embedded in the configured URL must not leak through a
	// normalisation error.
	_, err := NewClient(WithURL("ftp://admin:hunter2@stashdb.org"), WithAPIKey("k"))
	if err == nil {
		t.Fatal("want error for unsupported scheme")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("password leaked through normalisation error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "xxxxx") {
		t.Errorf("expected masked userinfo in %q", err.Error())
	}
}

// TestRedactURLMasksBothUserinfoAndQuery exercises the single shared helper
// (NIT 7): one URL carrying both a userinfo credential and a query secret must
// have both masked in a single pass.
func TestRedactURLMasksBothUserinfoAndQuery(t *testing.T) {
	got := redactURL("https://admin:hunter2@stashdb.org/graphql?api_key=SECRET&page=2")
	if strings.Contains(got, "hunter2") {
		t.Errorf("password not masked: %q", got)
	}
	if strings.Contains(got, "admin") {
		t.Errorf("username not masked: %q", got)
	}
	if strings.Contains(got, "SECRET") {
		t.Errorf("query secret not masked: %q", got)
	}
	if !strings.Contains(got, "xxxxx@stashdb.org") {
		t.Errorf("expected masked userinfo, got %q", got)
	}
	if !strings.Contains(got, "api_key=REDACTED") {
		t.Errorf("expected redacted query secret, got %q", got)
	}
	// A benign query parameter survives.
	if !strings.Contains(got, "page=2") {
		t.Errorf("benign param dropped: %q", got)
	}
}
