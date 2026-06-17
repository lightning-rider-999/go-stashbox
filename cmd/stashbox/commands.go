package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

// commandSpec is one row of the generated operation table. The generator (genops
// EmitCommands) writes a []commandSpec literal into gen_commands.go;
// buildRootCommand turns each spec into a leaf cobra command. The field set and
// names are FIXED by genops.EmitCommands (see cli.go in the generator): it emits
// a literal `{Path: …, OpName: …, Query: stashbox.<OpName>_Operation, Kind: …,
// InputType: …, ReturnType: …, Destructive: …, JobReturning: …, Deprecated: …}`,
// so this struct must declare exactly those exported fields with those types.
//
// Query is the genqlient operation document constant (stashbox.<OpName>_Operation,
// a const string), so the GraphQL text lives in exactly one place and a server
// upgrade that drifts it is a red build rather than a silent skew.
//
// Destructive and JobReturning are part of the shared command-table contract but
// are ALWAYS false here: go-stashbox is a read-only client compiled from a
// query-only schema view, so no operation mutates or enqueues a job. The fields
// stay declared (the generated literal sets them, and the conformance gate
// asserts they are uniformly false) but drive no behaviour — there is no
// destructive gate and no --wait path in this CLI.
type commandSpec struct {
	// Path is the cobra command path, resource-then-verb (["scene", "get"]).
	Path []string
	// OpName is the exported operation name, also the query const stem.
	OpName string
	// Query is the genqlient operation document for this field.
	Query string
	// Kind is the GraphQL operation kind; always "query" for this client.
	Kind string
	// InputType is the base type of the "input" argument, or "" if the operation
	// takes no single `input` argument (it may still take scalar args like id).
	InputType string
	// ReturnType is the base named type the operation returns.
	ReturnType string
	// Destructive flags a data-destroying operation. Always false here.
	Destructive bool
	// JobReturning flags an operation that enqueues an async job. Always false here.
	JobReturning bool
	// Deprecated flags a field carrying @deprecated in the schema.
	Deprecated bool
}

// buildRootCommand assembles the full cobra tree from generatedCommands. It
// creates a group command for every Path prefix (scene, performer, ...) and a
// leaf command per spec under it. The leaf's RunE reads variables from --input
// (file or stdin) and the convenience flags, runs the operation as raw GraphQL
// through the shared SDK transport, and writes the response data to stdout.
//
// Persistent flags on the root configure the client and output:
//
//	--url        stash-box base URL (falls back to STASHBOX_URL in the client)
//	--api-key    stash-box API key (falls back to STASHBOX_API_KEY in the client)
//	--output/-o  output format: json (default), table
//	--input      variables source: a JSON file path, or "-" for stdin
//
// Security note on --api-key: passing the key on the command line exposes it to
// any other user via the process listing (ps/proc) and writes it into shell
// history, so the STASHBOX_API_KEY environment variable is the preferred source
// and the flag is a convenience fallback. cobra never echoes a string flag's
// value in a usage/error message (only the flag name), so a malformed invocation
// will not leak the key into stderr or the structured error envelope.
//
// A PersistentPreRunE validates --output against outputFormats for every leaf
// before any client is built or any request is sent, so a bad format is a usage
// error (exit 2) caught up front.
func buildRootCommand() *cobra.Command {
	// Resolve placeholder build vars from the embedded module build info before
	// they are read into the --version template, so a `go install module@version`
	// binary self-reports its real version/commit/date instead of "dev".
	info, ok := debug.ReadBuildInfo()
	version, commit, date = resolveBuildInfo(version, commit, date, info, ok)

	root := &cobra.Command{
		Use:   "stashbox",
		Short: "Agent-first, read-only CLI for the stash-box GraphQL API",
		Long: "stashbox is a machine-readable command-line client for a stash-box " +
			"instance (StashDB and any other). Every stash-box query is exposed as a " +
			"resource-and-verb command (e.g. `stashbox scene get`, `stashbox performer " +
			"query`). It is read-only: there are no mutations, no async jobs, and no " +
			"destructive confirmation gate. Output is JSON by default.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// ArbitraryArgs bypasses cobra's root-only "unknown command" arg check so
		// an unrecognised top-level command reaches helpOrUnknown and is reported as
		// a usage error (exit 2), consistent with the resource groups.
		Args:              cobra.ArbitraryArgs,
		RunE:              helpOrUnknown,
		PersistentPreRunE: validateOutputFlag,
		// Version is the injected binary version (main.version), which makes cobra
		// register the built-in --version flag. This is the CLI binary's own
		// version, distinct from the `stashbox misc version` GraphQL op, which
		// reports the connected stash-box server's version.
		Version: version,
	}
	root.SetVersionTemplate(
		"stashbox {{.Version}} (commit " + commit + ", built " + date + ")\n",
	)
	wrapCobraUsageErrors(root)

	root.PersistentFlags().String("url", "", "stash-box base URL (default $STASHBOX_URL)")
	root.PersistentFlags().String("api-key", "", "stash-box API key (default $STASHBOX_API_KEY)")
	root.PersistentFlags().StringP("output", "o", "json", "output format: "+strings.Join(outputFormats, ", "))
	root.PersistentFlags().String("input", "", "variables source: JSON file path, or \"-\" for stdin")

	// groups caches intermediate group commands by their joined prefix so a
	// resource group (scene) is created once and shared by all its leaves.
	groups := map[string]*cobra.Command{}

	// Sort by the space-joined path. The join key is precomputed once per spec
	// rather than recomputed inside the comparator.
	type keyedSpec struct {
		key  string
		spec commandSpec
	}
	keyed := make([]keyedSpec, len(generatedCommands))
	for i, spec := range generatedCommands {
		keyed[i] = keyedSpec{key: strings.Join(spec.Path, " "), spec: spec}
	}
	slices.SortFunc(keyed, func(a, b keyedSpec) int {
		return cmp.Compare(a.key, b.key)
	})

	for _, ks := range keyed {
		spec := ks.spec
		parent := ensureGroups(root, groups, spec.Path[:len(spec.Path)-1])
		leaf := newLeafCommand(spec)
		parent.AddCommand(leaf)
	}

	// catalog is a built-in (not a generated GraphQL operation): it serves the
	// embedded operation catalog without touching the server.
	root.AddCommand(newCatalogCommand())
	return root
}

// helpOrUnknown is the RunE for the root and resource-group commands. With no
// arguments it prints help and exits 0; an unrecognised subcommand becomes a
// usage error (exit 2) rather than cobra's default of a silent help dump on a
// zero exit, so an agent that mistypes a command sees a non-zero status.
func helpOrUnknown(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return newUsageError(fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath()))
	}
	return c.Help()
}

// usageNoArgs is cobra.NoArgs with the rejection wrapped as a usage error.
// cobra's Args validators are invoked from Execute and their error is returned
// verbatim (not via SetFlagErrorFunc, which only covers flag parsing), so without
// this wrapper an extra positional argument would classify as the reserved
// ExitInternal instead of ExitUsage.
func usageNoArgs(cmd *cobra.Command, args []string) error {
	return newUsageError(cobra.NoArgs(cmd, args))
}

// validateOutputFlag is the root PersistentPreRunE: it checks the resolved
// --output value against outputFormats before the command's RunE builds a client
// or sends a request. An unknown format is wrapped in newUsageError so it
// classifies as exit 2, matching writeOutput's own rejection but catching it
// before any request is sent.
func validateOutputFlag(cmd *cobra.Command, _ []string) error {
	format, _ := cmd.Flags().GetString("output")
	if isValidOutputFormat(format) {
		return nil
	}
	return newUsageError(fmt.Errorf(
		"unknown output format %q: valid formats are %s",
		format, strings.Join(outputFormats, ", "),
	))
}

// ensureGroups walks the prefix segments, creating and caching a group command
// for each, and returns the command the leaf should attach to. An empty prefix
// returns the root.
func ensureGroups(root *cobra.Command, groups map[string]*cobra.Command, prefix []string) *cobra.Command {
	parent := root
	for i := range prefix {
		key := strings.Join(prefix[:i+1], " ")
		g, ok := groups[key]
		if !ok {
			g = &cobra.Command{
				Use:           prefix[i],
				Short:         prefix[i] + " operations",
				SilenceUsage:  true,
				SilenceErrors: true,
				RunE:          helpOrUnknown,
			}
			groups[key] = g
			parent.AddCommand(g)
		}
		parent = g
	}
	return parent
}

// clientResolver builds the *stashbox.Client a leaf command runs against. The
// seam exists so a test can drive the full RunE against an in-memory client
// without setting environment variables. Production passes clientFromFlags; a
// test passes a closure returning its own client.
type clientResolver func(cmd *cobra.Command) (*stashbox.Client, error)

// newLeafCommand builds the leaf cobra command for one operation spec, resolving
// its client from the --url/--api-key flags.
func newLeafCommand(spec commandSpec) *cobra.Command {
	return newLeafCommandResolver(spec, clientFromFlags)
}

// newLeafCommandWithClient builds a leaf bound to a fixed client, for tests.
func newLeafCommandWithClient(spec commandSpec, c *stashbox.Client) *cobra.Command {
	return newLeafCommandResolver(spec, func(*cobra.Command) (*stashbox.Client, error) {
		return c, nil
	})
}

// newLeafCommandResolver builds the leaf cobra command for one operation spec
// using resolve to obtain the client. Every leaf is a query, so the RunE is a
// single straight path: read --input/flags into variables, build the client, run
// the operation, render the response. There is no destructive gate, no
// subscription stream, and no --wait branch — this is a read-only client.
func newLeafCommandResolver(spec commandSpec, resolve clientResolver) *cobra.Command {
	leaf := &cobra.Command{
		Use:   spec.Path[len(spec.Path)-1],
		Short: shortFor(spec),
		// A leaf takes no positional arguments — its inputs come from --input and
		// the convenience flags. Rejecting extras (e.g. `scene get junk`) makes a
		// typo a usage error (exit 2) instead of a silently ignored argument.
		Args: usageNoArgs,
	}
	if spec.Deprecated {
		leaf.Deprecated = "deprecated in the stash-box schema; prefer the current operation"
	}
	addConvenienceFlags(leaf, spec)

	leaf.RunE = func(cmd *cobra.Command, _ []string) error {
		vars, err := resolveVariables(cmd, spec)
		if err != nil {
			return err
		}

		client, err := resolve(cmd)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("output")
		return runOperation(cmd.Context(), client, spec, vars, format, cmd.OutOrStdout())
	}
	return leaf
}

// shortFor renders a one-line description for a leaf. Every operation is a query,
// so the kind is the only tag; a deprecated op is marked so the hazard is visible
// in help output.
func shortFor(spec commandSpec) string {
	desc := fmt.Sprintf("%s (%s)", spec.OpName, spec.Kind)
	if spec.Deprecated {
		return desc + " [deprecated]"
	}
	return desc
}

// clientFromFlags builds a *stashbox.Client from the root --url/--api-key flags,
// each falling back to its environment variable inside NewClient when the flag is
// empty.
func clientFromFlags(cmd *cobra.Command) (*stashbox.Client, error) {
	url, _ := cmd.Flags().GetString("url")
	apiKey, _ := cmd.Flags().GetString("api-key")

	var opts []stashbox.Option
	if url != "" {
		opts = append(opts, stashbox.WithURL(url))
	}
	if apiKey != "" {
		opts = append(opts, stashbox.WithAPIKey(apiKey))
	}
	return stashbox.NewClient(opts...)
}

// graphqlVars adapts a map of raw-JSON variables to the genqlient request
// variable shape. genqlient marshals Request.Variables, so a
// map[string]json.RawMessage round-trips each value verbatim.
func graphqlVars(vars map[string]json.RawMessage) any {
	if len(vars) == 0 {
		return map[string]json.RawMessage{}
	}
	return vars
}

// requestFor builds the genqlient request for a spec and its variables.
func requestFor(spec commandSpec, vars map[string]json.RawMessage) *graphql.Request {
	return &graphql.Request{
		OpName:    spec.OpName,
		Query:     spec.Query,
		Variables: graphqlVars(vars),
	}
}
