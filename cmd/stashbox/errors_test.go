package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

func TestClassifyExit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ExitCode
	}{
		{"nil is ok", nil, ExitOK},
		{"usage error", newUsageError(errors.New("bad flag")), ExitUsage},
		{"no URL is usage", fmt.Errorf("wrapped: %w", stashbox.ErrNoURL), ExitUsage},
		{"unauthorized is auth", fmt.Errorf("x: %w", stashbox.ErrUnauthorized), ExitAuth},
		{"transport error", stashbox.NewTransportError(http.StatusBadGateway, errors.New("boom")), ExitTransport},
		{"transport with 401 is auth (joined)", stashbox.Classify(&graphql.HTTPError{StatusCode: http.StatusUnauthorized}), ExitAuth},
		{
			"graphql not-found",
			&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "scene not found"}}},
			ExitNotFound,
		},
		{
			"graphql validation",
			&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "id is required"}}},
			ExitValidation,
		},
		{
			"graphql server fault",
			&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "internal boom"}}},
			ExitServerFault,
		},
		{"unknown is internal", errors.New("unclassifiable failure"), ExitInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyExit(tc.err)
			if got != tc.want {
				t.Errorf("classifyExit(%v) = %+v, want %+v", tc.err, got, tc.want)
			}
		})
	}
}

// TestClassifyExitNeverEmitsWriteSideCodes asserts no classification path
// produces a write-side exit code (8-11): those are defined in the taxonomy but
// must never be emitted by this read-only client.
func TestClassifyExitNeverEmitsWriteSideCodes(t *testing.T) {
	samples := []error{
		nil,
		newUsageError(errors.New("x")),
		stashbox.ErrNoURL,
		stashbox.ErrUnauthorized,
		stashbox.NewTransportError(500, errors.New("x")),
		&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "not found"}}},
		&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "invalid"}}},
		&stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "boom"}}},
		errors.New("unclassifiable failure"),
	}
	for _, e := range samples {
		code := classifyExit(e)
		if code.Code >= 8 {
			t.Errorf("classifyExit(%v) = %q (%d): read-only client must never emit a write-side code (>=8)", e, code.Name, code.Code)
		}
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	gqlErr := &stashbox.GraphQLError{Errors: gqlerror.List{&gqlerror.Error{Message: "scene not found"}}}
	writeErrorEnvelope(&buf, ExitNotFound, gqlErr)

	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Error("envelope must be newline-terminated")
	}
	if strings.Count(strings.TrimSpace(line), "\n") != 0 {
		t.Error("envelope must be a single line")
	}

	var env stashbox.ErrorEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if env.Code != ExitNotFound.Name {
		t.Errorf("envelope code = %q, want %q", env.Code, ExitNotFound.Name)
	}
	if len(env.GraphQLErrors) != 1 || env.GraphQLErrors[0] != "scene not found" {
		t.Errorf("envelope graphqlErrors = %v, want [scene not found]", env.GraphQLErrors)
	}
}

// TestWriteErrorEnvelopeRedactsURLSecret confirms the envelope inherits the
// stashbox package's redaction: an ApiKey query secret embedded in a transport
// error's wrapped URL is masked, never written to stderr.
func TestWriteErrorEnvelopeRedactsURLSecret(t *testing.T) {
	var buf bytes.Buffer
	te := stashbox.NewTransportError(0, errors.New(`dial https://stashdb.org/graphql?apikey=SUPERSECRET failed`))
	writeErrorEnvelope(&buf, ExitTransport, te)

	if strings.Contains(buf.String(), "SUPERSECRET") {
		t.Errorf("envelope leaked the API key: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "REDACTED") {
		t.Errorf("envelope did not redact the API key: %s", buf.String())
	}
}
