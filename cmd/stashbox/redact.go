package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// redactedValue is the placeholder a masked secret is replaced with, in both a
// bare-field and a query-parameter context.
const redactedValue = "REDACTED"

// secretFieldKeys are the response JSON object keys whose string value is a
// credential and must be masked before output. stash-box's User type exposes the
// owner's own key as User.api_key (@isUserOwner), so a `stashbox user me` /
// `user get` / `user query` response can carry it. Matching is exact (the wire
// field name), with a defensive camelCase alias in case an instance or a future
// schema spells it apiKey on the output side.
var secretFieldKeys = map[string]bool{
	"api_key": true,
	"apiKey":  true,
}

// secretQueryKeys are the case-insensitive URL query-parameter names whose value
// is masked wherever it appears in a string. It mirrors the stashbox package's
// own redaction vocabulary, so a credential carried in a URL embedded in a
// response value (defence-in-depth — stash-box authenticates via the ApiKey
// header, not a query parameter, but a self-hosted instance could still pre-sign
// a URL) is scrubbed on stdout exactly as it would be on stderr.
var secretQueryKeys = []string{"apikey", "api_key", "token", "access_token"}

// redactSecrets scrubs credentials out of a GraphQL response payload before the
// output layer prints it. Two leaks are covered:
//
//   - A bare secret field: a User.api_key string in the response object (and,
//     defensively, an apiKey field), replaced with REDACTED.
//   - A credential in a URL query parameter inside any string value.
//
// The payload is decoded to a generic value, walked, and re-encoded. A decode or
// encode failure is returned so a malformed payload surfaces as an error rather
// than being printed unredacted. An empty payload round-trips unchanged.
func redactSecrets(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return data, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	redactValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// redactValue walks a decoded JSON value in place, masking credentials. A secret
// is identified by its object KEY (see secretFieldKeys), so masking happens at the
// map level: the whole value of a secret-named field is replaced outright, whatever
// its JSON shape (string, object, or array — defence-in-depth, since a credential
// is a credential regardless of how an instance nests it). Every other string is
// scrubbed for embedded URL query secrets (see redactQueryKeys); maps and slices
// recurse into their non-secret children.
func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if secretFieldKeys[k] {
				// A secret-named field is a credential whatever its JSON shape. Mask
				// the whole value outright rather than recursing, which would leave a
				// non-string secret (e.g. {"api_key":{"nested":"..."}}) unredacted,
				// since the recursion below only scrubs leaf strings.
				t[k] = redactedValue
				continue
			}
			if s, ok := child.(string); ok {
				t[k] = redactQueryKeys(s)
				continue
			}
			redactValue(child)
		}
	case []any:
		for i, child := range t {
			if s, ok := child.(string); ok {
				t[i] = redactQueryKeys(s)
				continue
			}
			redactValue(child)
		}
	}
}

// redactQueryKeys replaces the value of any sensitive query parameter (see
// secretQueryKeys) found in s with REDACTED, matching both "?key=v" and "&key=v"
// forms. It scans the raw string rather than parsing a URL, since a value may
// embed a URL amid other text.
func redactQueryKeys(s string) string {
	for _, key := range secretQueryKeys {
		s = maskParam(s, key)
	}
	return s
}

// maskParam masks the value of the query parameter named key (case-insensitive)
// wherever it appears in s, preserving the "?"/"&" separator and the key text.
// The value runs until the next "&", whitespace, quote, or end of string.
func maskParam(s, key string) string {
	lower := strings.ToLower(s)
	key = strings.ToLower(key)
	var b strings.Builder
	for i := 0; i < len(s); {
		if (s[i] == '?' || s[i] == '&') && hasParamAt(lower, i+1, key) {
			start := i + 1 + len(key) + 1 // past sep, key, and '='
			b.WriteByte(s[i])
			b.WriteString(s[i+1 : start])
			b.WriteString(redactedValue)
			j := start
			for j < len(s) && !isValueDelimiter(s[j]) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// hasParamAt reports whether lower (already lowercased) contains key immediately
// followed by '=' starting at index i.
func hasParamAt(lower string, i int, key string) bool {
	end := i + len(key)
	if end >= len(lower) {
		return false
	}
	return lower[i:end] == key && lower[end] == '='
}

// isValueDelimiter reports whether c ends a query-parameter value in a string
// that may embed a URL among prose.
func isValueDelimiter(c byte) bool {
	switch c {
	case '&', ' ', '\t', '\n', '"', '\'', ')', '}', ']', ',', ';':
		return true
	default:
		return false
	}
}
