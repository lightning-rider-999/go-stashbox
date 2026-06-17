package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactSecretsApiKeyField confirms a User.api_key string in a response is
// masked before output — the credential a `stashbox user me` response carries.
func TestRedactSecretsApiKeyField(t *testing.T) {
	data := json.RawMessage(`{"me":{"id":"1","name":"alice","api_key":"SECRETKEY123"}}`)
	out, err := redactSecrets(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "SECRETKEY123") {
		t.Errorf("api_key leaked through redaction: %s", out)
	}
	if !strings.Contains(string(out), redactedValue) {
		t.Errorf("api_key was not replaced with %s: %s", redactedValue, out)
	}
	// A non-secret field is untouched.
	if !strings.Contains(string(out), `"alice"`) {
		t.Errorf("redaction corrupted a non-secret field: %s", out)
	}
}

func TestRedactSecretsCamelCaseAlias(t *testing.T) {
	data := json.RawMessage(`{"user":{"apiKey":"SECRET"}}`)
	out, err := redactSecrets(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "SECRET") {
		t.Errorf("apiKey alias leaked: %s", out)
	}
}

func TestRedactSecretsNested(t *testing.T) {
	data := json.RawMessage(`{"queryUsers":{"users":[{"name":"a","api_key":"K1"},{"name":"b","api_key":"K2"}]}}`)
	out, err := redactSecrets(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "K1") || strings.Contains(string(out), "K2") {
		t.Errorf("nested api_key leaked: %s", out)
	}
}

// TestRedactSecretsObjectValuedSecretKey confirms a secret-named field is masked
// whatever its JSON shape — an object or array value, not only a bare string. The
// live User.api_key is a scalar String, so this is defence-in-depth against an
// instance (or a future schema) that nests a credential under a secret key.
func TestRedactSecretsObjectValuedSecretKey(t *testing.T) {
	cases := map[string]json.RawMessage{
		"object value": json.RawMessage(`{"me":{"api_key":{"nested":"supersecret"}}}`),
		"array value":  json.RawMessage(`{"me":{"api_key":["supersecret","more"]}}`),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := redactSecrets(data)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "supersecret") || strings.Contains(string(out), "more") {
				t.Errorf("non-string secret leaked through redaction: %s", out)
			}
			if !strings.Contains(string(out), redactedValue) {
				t.Errorf("secret field was not replaced with %s: %s", redactedValue, out)
			}
		})
	}
}

func TestRedactSecretsURLQueryParam(t *testing.T) {
	data := json.RawMessage(`{"image":{"url":"https://cdn.example/img.jpg?apikey=JWTSECRET&w=100"}}`)
	out, err := redactSecrets(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "JWTSECRET") {
		t.Errorf("URL query secret leaked: %s", out)
	}
	if !strings.Contains(string(out), "w=100") {
		t.Errorf("redaction dropped a non-secret query param: %s", out)
	}
}

// sampleJWT is a representative pre-signed credential. Its presence anywhere in
// redacted output is a leak.
const sampleJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ4In0.sig"

// TestRedactSecretsRealisticPayload is the integration-shaped guarantee: a
// realistic queryScenes-shaped response whose media URLs carry the instance
// credential as ?apikey=<JWT> comes out of redactSecrets with the JWT gone,
// apikey=REDACTED in its place, and the rest of every URL — path and sibling
// query parameters — intact. (When redaction was relocated out of the conformance
// suite into this package, this realistic-payload case moved with it; the
// fine-grained edge cases are the unit tests above.)
func TestRedactSecretsRealisticPayload(t *testing.T) {
	payload := json.RawMessage(`{
		"queryScenes": {
			"count": 1,
			"scenes": [
				{
					"id": "42",
					"title": "anything",
					"images": [
						{"url": "https://cdn.stashdb.org/scene/42/cover.jpg?apikey=` + sampleJWT + `"},
						{"url": "https://cdn.stashdb.org/scene/42/thumb.jpg?apikey=` + sampleJWT + `&w=200"}
					]
				}
			]
		}
	}`)

	out, err := redactSecrets(payload)
	if err != nil {
		t.Fatalf("redactSecrets: %v", err)
	}
	s := string(out)

	if strings.Contains(s, "eyJ") {
		t.Errorf("redacted output still contains a JWT token:\n%s", s)
	}
	if strings.Contains(s, "apikey="+sampleJWT) {
		t.Errorf("redacted output still contains apikey=<jwt>:\n%s", s)
	}
	if !strings.Contains(s, "apikey="+redactedValue) {
		t.Errorf("redacted output is missing apikey=%s:\n%s", redactedValue, s)
	}

	// The rest of each URL survives after decoding.
	var got struct {
		QueryScenes struct {
			Scenes []struct {
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"scenes"`
		} `json:"queryScenes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted payload: %v", err)
	}
	if len(got.QueryScenes.Scenes) != 1 || len(got.QueryScenes.Scenes[0].Images) != 2 {
		t.Fatalf("redacted payload lost structure: %s", out)
	}
	cover := got.QueryScenes.Scenes[0].Images[0].URL
	if !strings.HasPrefix(cover, "https://cdn.stashdb.org/scene/42/cover.jpg") {
		t.Errorf("cover URL path was mangled: %q", cover)
	}
	if !strings.Contains(cover, "apikey="+redactedValue) {
		t.Errorf("cover URL apikey not redacted: %q", cover)
	}
	thumb := got.QueryScenes.Scenes[0].Images[1].URL
	if !strings.HasPrefix(thumb, "https://cdn.stashdb.org/scene/42/thumb.jpg") {
		t.Errorf("thumb URL path was mangled: %q", thumb)
	}
	// The sibling w=200 query parameter must survive.
	if !strings.Contains(thumb, "w=200") {
		t.Errorf("thumb lost its sibling w=200 parameter: %q", thumb)
	}
}

func TestRedactSecretsEmpty(t *testing.T) {
	out, err := redactSecrets(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty input should round-trip empty, got %q", out)
	}
}

func TestRedactSecretsMalformedIsError(t *testing.T) {
	if _, err := redactSecrets(json.RawMessage(`{not json`)); err == nil {
		t.Error("malformed JSON should surface as an error, not silently pass")
	}
}
