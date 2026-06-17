package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "{\n  \"a\": 1\n}\n" {
		t.Errorf("writeJSON = %q", got)
	}
}

func TestWriteJSONNull(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "null" {
		t.Errorf("writeJSON(nil) = %q, want null", got)
	}
}

func TestWriteOutputUnknownFormatIsUsage(t *testing.T) {
	spec := commandSpec{OpName: "Version", ReturnType: "Version"}
	err := writeOutput(&bytes.Buffer{}, "xml", spec, json.RawMessage(`{"version":{}}`))
	if err == nil {
		t.Fatal("expected an error for an unknown format")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("unknown format should classify as usage, got %+v", classifyExit(err))
	}
}

func TestWriteTableList(t *testing.T) {
	// A result-wrapper return whose sole array field holds the items.
	spec := commandSpec{OpName: "QueryPerformers", ReturnType: "QueryPerformersResultType"}
	data := json.RawMessage(`{"queryPerformers":{"count":2,"performers":[{"id":"1","name":"Ann"},{"id":"2","name":"Bea"}]}}`)
	var buf bytes.Buffer
	if err := writeTable(&buf, spec, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"id", "name", "Ann", "Bea", "1", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteTableSingleObject(t *testing.T) {
	spec := commandSpec{OpName: "Version", ReturnType: "Version"}
	data := json.RawMessage(`{"version":{"version":"v0.10.0","hash":"abc"}}`)
	var buf bytes.Buffer
	if err := writeTable(&buf, spec, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "v0.10.0") {
		t.Errorf("kv table output unexpected:\n%s", out)
	}
}

func TestStreamItemsBareList(t *testing.T) {
	spec := commandSpec{OpName: "FindDrafts", ReturnType: "Draft"}
	data := json.RawMessage(`{"findDrafts":[{"id":"1"},{"id":"2"}]}`)
	items, ok, err := streamItems(spec, data)
	if err != nil || !ok {
		t.Fatalf("streamItems ok=%v err=%v", ok, err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

func TestStreamItemsNonList(t *testing.T) {
	spec := commandSpec{OpName: "FindScene", ReturnType: "Scene"}
	data := json.RawMessage(`{"findScene":{"id":"1"}}`)
	_, ok, err := streamItems(spec, data)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a single object must not be reported as list-shaped")
	}
}

func TestIsValidOutputFormat(t *testing.T) {
	for _, f := range []string{"", "json", "table"} {
		if !isValidOutputFormat(f) {
			t.Errorf("%q should be valid", f)
		}
	}
	for _, f := range []string{"xml", "ndjson", "yaml"} {
		if isValidOutputFormat(f) {
			t.Errorf("%q should be invalid", f)
		}
	}
}
