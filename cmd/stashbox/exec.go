package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

// runOperation executes one operation as raw GraphQL and writes its response
// data to out. It deliberately bypasses genqlient's typed operation functions:
// the request carries the operation document (spec.Query) and the caller's raw
// JSON variables, and the response data is captured as json.RawMessage. This
// preserves the present/absent/null three-state of an input object that typed Go
// structs erase, and keeps the runtime free of a per-operation dispatch table —
// the generated command spec carries everything the request needs.
//
// Variables come from the resolved --input/stdin JSON plus the convenience flags
// (see input.go's resolveVariables), bound as raw JSON so they round-trip to the
// wire verbatim. An operation with no input and no required arguments is sent
// with empty variables. A transport or GraphQL error is mapped into the SDK's
// typed error model (via stashbox.Classify) so the CLI reports a stable,
// agent-readable classification and exit code.
//
// format selects the output rendering (--output); writeOutput redacts secrets
// and renders the response data. An empty format defaults to json.
func runOperation(ctx context.Context, c *stashbox.Client, spec commandSpec, vars map[string]json.RawMessage, format string, out io.Writer) error {
	var data json.RawMessage
	req := requestFor(spec, vars)
	resp := &graphql.Response{Data: &data}

	if err := c.GraphQL().MakeRequest(ctx, req, resp); err != nil {
		return stashbox.Classify(err)
	}
	return writeOutput(out, format, spec, data)
}
