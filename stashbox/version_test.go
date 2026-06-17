package stashbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/schema"
)

// versionClient spins an httptest server that answers the Version query with the
// given JSON data payload (the object placed under "data") and returns a client
// pointed at it.
func versionClient(t *testing.T, dataJSON string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":%s}`, dataJSON)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(WithURL(srv.URL), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVersionInfo(t *testing.T) {
	c := versionClient(t, `{"version":{"version":"v0.10.0","hash":"deadbeef","build_time":"2026-01-02T03:04:05Z","build_type":"official"}}`)
	info, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if info.Version != "v0.10.0" {
		t.Errorf("Version = %q, want v0.10.0", info.Version)
	}
	if info.Hash != "deadbeef" {
		t.Errorf("Hash = %q, want deadbeef", info.Hash)
	}
	if info.BuildTime != "2026-01-02T03:04:05Z" {
		t.Errorf("BuildTime = %q", info.BuildTime)
	}
	if info.BuildType != "official" {
		t.Errorf("BuildType = %q, want official", info.BuildType)
	}
}

// TestVersionNullObject covers the only defensive branch left after dropping
// go-stashapp's nil-version fallback (stash-box's version is String!): a null
// version *object* in an otherwise valid response is an error, not a panic.
func TestVersionNullObject(t *testing.T) {
	c := versionClient(t, `{"version":null}`)
	_, err := c.Version(context.Background())
	if err == nil {
		t.Fatal("want an error for a null version object")
	}
}

func TestCompatibility(t *testing.T) {
	// schema.SchemaVersion is the tag this library was generated against. Cases
	// are expressed relative to it so the test does not hard-code the constant.
	cases := []struct {
		name           string
		serverVersion  string
		wantRelation   VersionRelation
		wantCompatible bool
	}{
		{"exact match", schema.SchemaVersion, VersionEqual, true},
		{"match without v prefix", trimV(schema.SchemaVersion), VersionEqual, true},
		{"patch drift still compatible", bumpPatch(schema.SchemaVersion), VersionEqual, true},
		{"server newer minor", "v9.99.0", VersionServerNewer, false},
		{"server older minor", "v0.0.1", VersionServerOlder, false},
		{"empty version is unknown", "", VersionUnknown, false},
		{"dev hash-only build is unknown", "dev-abc123", VersionUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := versionClient(t, fmt.Sprintf(
				`{"version":{"version":%q,"hash":"h","build_time":"t","build_type":"official"}}`, tc.serverVersion))

			result, err := c.Compatibility(context.Background())
			if err != nil {
				t.Fatalf("Compatibility: %v", err)
			}
			if result.Relation != tc.wantRelation {
				t.Errorf("Relation = %v, want %v", result.Relation, tc.wantRelation)
			}
			if result.Compatible != tc.wantCompatible {
				t.Errorf("Compatible = %v, want %v", result.Compatible, tc.wantCompatible)
			}
			if result.SchemaVersion != schema.SchemaVersion {
				t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, schema.SchemaVersion)
			}
			if result.Server == nil || result.Server.Version != tc.serverVersion {
				t.Errorf("Server.Version = %+v, want %q", result.Server, tc.serverVersion)
			}

			// CheckCompatibility is a thin wrapper; its bool must agree.
			compatible, server, err := c.CheckCompatibility(context.Background())
			if err != nil {
				t.Fatalf("CheckCompatibility: %v", err)
			}
			if compatible != tc.wantCompatible {
				t.Errorf("CheckCompatibility compatible = %v, want %v", compatible, tc.wantCompatible)
			}
			if server == nil || server.Version != tc.serverVersion {
				t.Errorf("CheckCompatibility server = %+v, want version %q", server, tc.serverVersion)
			}
		})
	}
}

// TestCompatibilityTransportError confirms a transport failure surfaces as a
// typed error and a zero Compatibility, not a misleading "unknown" relation.
func TestCompatibilityTransportError(t *testing.T) {
	c, err := NewClient(WithURL("http://127.0.0.1:1/graphql"), WithAPIKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Compatibility(context.Background())
	if err == nil {
		t.Fatal("want a transport error")
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Errorf("want *TransportError, got %T", err)
	}
	if result.Server != nil || result.Compatible {
		t.Errorf("want zero Compatibility on error, got %+v", result)
	}

	// CheckCompatibility propagates the same error with a nil server.
	compatible, server, err := c.CheckCompatibility(context.Background())
	if err == nil || compatible || server != nil {
		t.Errorf("CheckCompatibility on transport error = (%v, %v, %v)", compatible, server, err)
	}
}

func TestVersionRelationString(t *testing.T) {
	cases := map[VersionRelation]string{
		VersionUnknown:      "unknown",
		VersionEqual:        "equal",
		VersionServerNewer:  "server-newer",
		VersionServerOlder:  "server-older",
		VersionRelation(99): "unknown",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("VersionRelation(%d).String() = %q, want %q", r, got, want)
		}
	}
}

// trimV drops a leading "v" so a case can assert the no-prefix tolerance.
func trimV(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}

// bumpPatch increments the patch component of a vMAJOR.MINOR.PATCH tag, leaving
// major.minor untouched so the result is patch-drift compatible. It assumes the
// schema tag is a well-formed three-part semver, which it is (v0.10.0).
func bumpPatch(v string) string {
	var maj, min, pat int
	if _, err := fmt.Sscanf(v, "v%d.%d.%d", &maj, &min, &pat); err != nil {
		return v
	}
	return fmt.Sprintf("v%d.%d.%d", maj, min, pat+1)
}
