package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

// resolveVariables builds the GraphQL variables object for one operation.
//
// The variables are a JSON object whose keys are the operation's own variable
// names. stash-box queries take one of two shapes: a single `input` object (e.g.
// queryPerformers(input: PerformerQueryInput!)), or a handful of scalar
// arguments (findScene(id: ID!), findTagOrAlias(name: String!),
// searchPerformers(term, limit, page, per_page)). Two sources feed the map, in
// this precedence:
//
//  1. --input <file> or --input - (stdin) supplies the FULL variables object as
//     JSON. Its values are carried as json.RawMessage and never decoded into a
//     typed Go struct, so an input object round-trips byte-for-byte: a present
//     field stays, an omitted field stays omitted, and an explicit null stays
//     null. That fidelity is the point — the raw JSON is always authoritative.
//  2. Convenience flags (--id, --name, --username, --term, --limit) are a small
//     read-only shorthand for the scalar-argument queries. They merge UNDER the
//     --input keys: any key --input already set wins.
//
// resolveVariables applies a convenience flag only for an argument the operation
// actually declares (looked up in the embedded catalog), so a flag can never
// inject a variable the operation does not accept. An operation with no --input
// and no applicable flags is sent with empty variables.
func resolveVariables(cmd *cobra.Command, spec commandSpec) (map[string]json.RawMessage, error) {
	vars, err := readInputVariables(cmd)
	if err != nil {
		return nil, err
	}

	if err := applyConvenienceFlags(cmd, spec, vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// readInputVariables reads the --input source (file path, or "-" for stdin) into
// a variables map. Values stay json.RawMessage so they are never re-encoded. An
// empty or absent --input yields an empty, non-nil map ready for flag merging.
func readInputVariables(cmd *cobra.Command) (map[string]json.RawMessage, error) {
	input, _ := cmd.Flags().GetString("input")
	if input == "" {
		return map[string]json.RawMessage{}, nil
	}

	var (
		data []byte
		err  error
	)
	if input == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(input)
	}
	if err != nil {
		// A bad --input path (or unreadable stdin) is the caller's mistake:
		// classify it as a usage error (exit 2), not an internal failure.
		return nil, newUsageError(fmt.Errorf("reading --input: %w", err))
	}
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}

	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, newUsageError(fmt.Errorf("--input must be a JSON object of variables: %w", err))
	}
	if vars == nil {
		return map[string]json.RawMessage{}, nil
	}
	return vars, nil
}

// convenienceFlag describes one read-only shorthand flag: the cobra flag name,
// its usage text, the operation argument it binds to, and whether the argument
// is a JSON number (emitted as a bare number) rather than a JSON string.
type convenienceFlag struct {
	flag    string
	arg     string
	usage   string
	numeric bool
}

// convenienceFlags are the scalar-argument shorthands the CLI offers, each
// registered on a leaf only when the operation declares the matching argument
// (see addConvenienceFlags). They map one-to-one onto the scalar arguments
// stash-box's query root fields actually take — id, name, username, term, limit
// — so a flag can never name an argument the operation does not accept. The
// dominant `input` object argument is supplied via --input, not a flag.
var convenienceFlags = []convenienceFlag{
	{flag: "id", arg: "id", usage: "convenience: select by ID (binds the `id` argument)"},
	{flag: "name", arg: "name", usage: "convenience: select by name (binds the `name` argument)"},
	{flag: "username", arg: "username", usage: "convenience: select by username (binds the `username` argument)"},
	{flag: "term", arg: "term", usage: "convenience: search term (binds the `term` argument)"},
	{flag: "limit", arg: "limit", usage: "convenience: result limit (binds the `limit` argument)", numeric: true},
}

// addConvenienceFlags registers the read-only shorthand flags on a leaf, but ONLY
// for arguments the operation actually declares. Anything the op does not declare
// is simply not added, which keeps the surface defensive: an unknown shorthand is
// a usage error rather than a silently dropped flag.
func addConvenienceFlags(leaf *cobra.Command, spec commandSpec) {
	args := operationArgNames(spec.OpName)
	for _, f := range convenienceFlags {
		if args[f.arg] {
			leaf.Flags().String(f.flag, "", f.usage)
		}
	}
}

// applyConvenienceFlags merges any set convenience flags into vars, under the
// --input keys (an --input key is never overwritten). A flag whose argument the
// operation does not declare is ignored at registration time, so this only ever
// sees applicable flags.
func applyConvenienceFlags(cmd *cobra.Command, spec commandSpec, vars map[string]json.RawMessage) error {
	args := operationArgNames(spec.OpName)
	for _, f := range convenienceFlags {
		if !args[f.arg] || !cmd.Flags().Changed(f.flag) {
			continue
		}
		// A pre-set --input key wins and short-circuits.
		if _, ok := vars[f.arg]; ok {
			continue
		}
		val, _ := cmd.Flags().GetString(f.flag)
		encoded, err := encodeFlagValue(f, val)
		if err != nil {
			return err
		}
		vars[f.arg] = encoded
	}
	return nil
}

// encodeFlagValue encodes a convenience flag's string value as the JSON type the
// argument expects: a bare number for a numeric argument (limit: Int), a JSON
// string otherwise (id/name/username/term). A numeric flag value that is not a
// valid integer is a usage error.
func encodeFlagValue(f convenienceFlag, val string) (json.RawMessage, error) {
	if f.numeric {
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, newUsageError(fmt.Errorf("--%s must be an integer, got %q", f.flag, val))
		}
		return json.RawMessage(strconv.Itoa(n)), nil
	}
	encoded, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("encoding --%s: %w", f.flag, err)
	}
	return encoded, nil
}

// operationArgNames returns the set of argument names the operation declares,
// read from the embedded catalog — the same source of truth the command table is
// generated from, so the convenience flags can never offer an argument the
// operation does not actually accept. An unknown OpName yields an empty set.
func operationArgNames(opName string) map[string]bool {
	entry, ok := catalogEntry(opName)
	if !ok {
		return map[string]bool{}
	}
	names := make(map[string]bool, len(entry.Args))
	for _, a := range entry.Args {
		names[a.Name] = true
	}
	return names
}
