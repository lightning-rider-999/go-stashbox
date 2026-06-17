package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveBuildInfo(t *testing.T) {
	t.Run("release build untouched", func(t *testing.T) {
		v, c, d := resolveBuildInfo("v1.2.3", "abc", "2026-01-01", nil, false)
		if v != "v1.2.3" || c != "abc" || d != "2026-01-01" {
			t.Errorf("release build should be untouched, got %q %q %q", v, c, d)
		}
	})
	t.Run("dev with no build info stays dev", func(t *testing.T) {
		v, _, _ := resolveBuildInfo("dev", "none", "unknown", nil, false)
		if v != "dev" {
			t.Errorf("v = %q, want dev", v)
		}
	})
}

func TestRunUnknownCommandExitsUsage(t *testing.T) {
	root := buildRootCommand()
	root.SetArgs([]string{"definitely-not-a-command"})
	root.SetOut(&nopWriter{})
	root.SetErr(&nopWriter{})

	err := root.Execute()
	if err == nil {
		t.Fatal("an unknown command should return an error")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("unknown command should classify as usage, got %+v", classifyExit(err))
	}
}

func TestRunReturnsUsageForBadOutputFormat(t *testing.T) {
	// run() drives the whole error path; a bad --output is caught by the
	// PersistentPreRunE before any client is built.
	t.Setenv("STASHBOX_URL", "https://example.invalid")
	root := buildRootCommand()
	root.SetArgs([]string{"misc", "version", "-o", "xml"})
	root.SetOut(&nopWriter{})
	root.SetErr(&nopWriter{})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("a bad --output should error")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("bad --output should classify as usage, got %+v", classifyExit(err))
	}
}

func TestHelpOrUnknown(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	c.SetOut(&nopWriter{})
	if err := helpOrUnknown(c, nil); err != nil {
		t.Errorf("no args should print help, got %v", err)
	}
	if err := helpOrUnknown(c, []string{"bogus"}); err == nil {
		t.Error("an arg should be an unknown-command usage error")
	}
}

func TestShortFor(t *testing.T) {
	if got := shortFor(commandSpec{OpName: "FindScene", Kind: "query"}); got != "FindScene (query)" {
		t.Errorf("shortFor = %q", got)
	}
	if got := shortFor(commandSpec{OpName: "SearchScene", Kind: "query", Deprecated: true}); got != "SearchScene (query) [deprecated]" {
		t.Errorf("shortFor deprecated = %q", got)
	}
}

// nopWriter discards writes; used to silence cobra output during tests.
type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }
