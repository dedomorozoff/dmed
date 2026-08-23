package lsp

import (
	"path/filepath"
	"testing"
)

func TestURIToPathConversion(t *testing.T) {
	samplePath, _ := filepath.Abs("test.go")
	uri := pathToURI(samplePath)

	converted := uriToPath(uri)
	if filepath.Clean(converted) != filepath.Clean(samplePath) {
		t.Fatalf("URI roundtrip mismatch: original=%q uri=%q converted=%q", samplePath, uri, converted)
	}
}

func TestDiagnosticsStoreAndGet(t *testing.T) {
	c := &Client{
		diagnostics: make(map[string][]Diagnostic),
	}
	absPath, _ := filepath.Abs("main.go")
	c.diagnostics[absPath] = []Diagnostic{
		{Line: 10, Col: 5, Severity: 1, Message: "undefined symbol: foo"},
	}

	got := c.GetDiagnostics(absPath)
	if len(got) != 1 || got[0].Message != "undefined symbol: foo" {
		t.Fatalf("unexpected diagnostics: %+v", got)
	}
}
