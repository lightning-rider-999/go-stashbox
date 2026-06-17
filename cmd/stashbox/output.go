package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// outputFormats is the set of values accepted by --output, in help order. It is
// the single source of truth for the valid set: writeOutput's switch, the
// unknown-format error message, and validateOutputFlag all read it.
//
// json is the agent-facing default; table is a human convenience. There is no
// ndjson/yaml here (the read/write sibling client carries them); the read-only
// surface keeps the format set small and the renderers simple.
var outputFormats = []string{"json", "table"}

// writeOutput renders an operation's response data to w in the requested format.
// data is the GraphQL response data object, shaped {"<rootField>": <result>};
// spec.ReturnType tells the table renderer how to find the primary list.
//
// Secret redaction runs first, for every format: a stash-box User exposes its
// owner's own api_key as a plain string (User.api_key, @isUserOwner), so a
// `stashbox user me` / `user get` / `user query` response can carry the
// caller's credential. That value must never reach stdout or a log, regardless
// of the rendering, so redactSecrets scrubs it (and any credential carried in a
// URL query parameter) before anything is written.
//
// json is the default and is always emitted: there is no TTY detection, so an
// agent gets stable machine-readable output whether or not a terminal is
// attached.
func writeOutput(w io.Writer, format string, spec commandSpec, data json.RawMessage) error {
	data, err := redactSecrets(data)
	if err != nil {
		return fmt.Errorf("redacting secrets: %w", err)
	}

	switch format {
	case "", "json":
		return writeJSON(w, data)
	case "table":
		return writeTable(w, spec, data)
	default:
		// A bad --output is the caller's mistake: classify it as a usage error
		// (exit 2), not an internal failure (exit 1).
		return newUsageError(fmt.Errorf("unknown output format %q: valid formats are %s", format, strings.Join(outputFormats, ", ")))
	}
}

// writeJSON pretty-prints raw JSON to w with a 2-space indent and a trailing
// newline. The CLI is agent-first, so JSON is the default output regardless of
// whether stdout is a terminal. A null or empty data field renders as the
// literal "null" so the output is always a parseable document.
func writeJSON(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("formatting response JSON: %w", err)
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// streamItems locates the primary list of a response for the table renderer.
//
// The response data is {"<rootField>": <result>}. The single root field is
// unwrapped first. Detection then splits on the operation's return type:
//
//   - result-wrapper return (spec.ReturnType ends in "ResultType" or "Result"):
//     the result is an object such as {count, performers:[...]}. The items are
//     the elements of its single array-valued field; the scalar wrapper fields
//     like count are metadata and are skipped. With no array field, no list is
//     reported.
//   - bare list return (e.g. findDrafts -> [Draft]!): the unwrapped result is
//     itself a JSON array; its elements are the items.
//
// Any other shape (a single object, a scalar, null) reports ok=false so the
// caller falls back to a single-object / one-cell rendering. A malformed payload
// surfaces as a non-nil error rather than being silently treated as not
// list-shaped.
func streamItems(spec commandSpec, data json.RawMessage) ([]any, bool, error) {
	result, err := unwrapValue(data)
	if err != nil {
		return nil, false, err
	}

	// Bare list return: the result is already an array.
	if arr, ok := result.([]any); ok {
		return arr, true, nil
	}

	// Result-wrapper return: find the single array-valued field. stash-box names
	// its query wrappers QueryPerformersResultType / QueryExistingPerformerResult
	// / FingerprintClustersResult, so both suffixes are matched.
	if strings.HasSuffix(spec.ReturnType, "ResultType") || strings.HasSuffix(spec.ReturnType, "Result") {
		if obj, ok := result.(map[string]any); ok {
			if arr, ok := soleArrayField(obj); ok {
				return arr, true, nil
			}
		}
	}
	return nil, false, nil
}

// soleArrayField returns the array value of the object's array-valued field. A
// well-formed stash-box result wrapper has exactly one such field (performers,
// scenes, ...) alongside scalar metadata (count). When more than one array field
// is present the first by sorted key wins, which keeps the choice deterministic
// for an unexpected schema shape.
func soleArrayField(obj map[string]any) ([]any, bool) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if arr, ok := obj[k].([]any); ok {
			return arr, true
		}
	}
	return nil, false
}

// unwrapValue strips the single {"<rootField>": <result>} wrapper that every
// GraphQL response data object carries and returns the decoded inner value. When
// data is not a single-field object it is returned decoded as-is. A JSON decode
// failure is surfaced as the error rather than swallowed.
func unwrapValue(data json.RawMessage) (any, error) {
	v, err := decodeAny(data)
	if err != nil {
		return nil, err
	}
	if obj, ok := v.(map[string]any); ok && len(obj) == 1 {
		for _, inner := range obj {
			return inner, nil
		}
	}
	return v, nil
}

// decodeAny decodes JSON into a generic value with number preservation enabled,
// so a large integer scalar stays a json.Number and survives re-encoding
// verbatim instead of being rounded through float64. The cell renderer handles
// json.Number alongside float64.
func decodeAny(data json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// nestedPlaceholder stands in for a value that a table cell cannot show flat.
const nestedPlaceholder = "{…}"

// writeTable renders a best-effort aligned text table for human eyes. For a list
// result it prints one row per item with a column per scalar key (the union
// across items, sorted); nested objects and arrays render as a placeholder
// rather than inlined JSON, and a missing key renders blank. For a single object
// it prints a two-column key/value table. It is deliberately simple and tolerant
// of missing or extra keys — the machine format is json.
func writeTable(w io.Writer, spec commandSpec, data json.RawMessage) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	items, ok, err := streamItems(spec, data)
	if err != nil {
		return err
	}
	var rows [][]string
	if ok {
		rows = listTableRows(items)
	} else {
		// Single value: key/value table when it is an object, else one cell.
		single, err := unwrapValue(data)
		if err != nil {
			return err
		}
		if obj, ok := single.(map[string]any); ok {
			rows = kvTableRows(obj)
		} else {
			rows = [][]string{{cell(single)}}
		}
	}

	if _, err := io.WriteString(tw, joinRows(rows)); err != nil {
		return err
	}
	return tw.Flush()
}

// joinRows renders rows as tab-separated lines, each newline-terminated.
func joinRows(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteByte('\n')
	}
	return b.String()
}

// listTableRows builds a header of scalar columns and one row per item. Nested
// objects/arrays render as a placeholder and a missing key renders blank. With
// no scalar columns it falls back to one single-cell row per item.
func listTableRows(items []any) [][]string {
	cols := scalarColumns(items)
	if len(cols) == 0 {
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{cell(it)})
		}
		return rows
	}
	rows := make([][]string, 0, len(items)+1)
	rows = append(rows, cols)
	for _, it := range items {
		obj, _ := it.(map[string]any)
		row := make([]string, len(cols))
		for i, c := range cols {
			if obj == nil {
				continue
			}
			if v, ok := obj[c]; ok {
				row[i] = cell(v)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// kvTableRows builds a two-column key/value table for one object.
func kvTableRows(obj map[string]any) [][]string {
	rows := make([][]string, 0, len(obj)+1)
	rows = append(rows, []string{"KEY", "VALUE"})
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rows = append(rows, []string{k, cell(obj[k])})
	}
	return rows
}

// scalarColumns returns the sorted union of top-level keys whose value is a
// scalar in at least one item. Keys that are only ever nested objects/arrays are
// dropped from the header entirely.
func scalarColumns(items []any) []string {
	seen := map[string]bool{}
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range obj {
			if isScalar(v) {
				seen[k] = true
			}
		}
	}
	cols := make([]string, 0, len(seen))
	for k := range seen {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// isScalar reports whether v is a flat value (string, number, bool, null) as
// opposed to a nested map or slice.
func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// cell formats a single value for a table cell. Scalars print plainly; nested
// objects and arrays collapse to a placeholder so a row stays one line.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any, []any:
		return nestedPlaceholder
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case float64:
		// A generic decode without UseNumber yields float64; render integers
		// without a trailing .0 for a cleaner table.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// isValidOutputFormat reports whether format is one writeOutput can render. The
// empty string is accepted because it selects the json default. validateOutputFlag
// calls this so a bad --output fails as a usage error (exit 2) before any request
// is sent.
func isValidOutputFormat(format string) bool {
	return format == "" || slices.Contains(outputFormats, format)
}
