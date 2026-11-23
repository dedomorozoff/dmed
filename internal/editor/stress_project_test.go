package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	stressPkgs     = 9
	stressPerPkg   = 210
	stressSkipNode = 80
)

// makeStressProject seeds a project shaped like a generated one: hundreds of
// Go files spread over several packages, plus trees the walking tools must
// ignore. It is deliberately just files on disk — the point is how dmed
// indexes and scans them, not how fast the test can call os.WriteFile.
func makeStressProject(t testing.TB, root string) {
	t.Helper()
	for p := 0; p < stressPkgs; p++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%02d", p))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		for f := 0; f < stressPerPkg; f++ {
			path := filepath.Join(dir, fmt.Sprintf("f%03d.go", f))
			body := fmt.Sprintf("package pkg%02d\n// file %03d with STRESS_PKG_MARKER\n", p, f)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
	}
	node := filepath.Join(root, "node_modules", "generated")
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", node, err)
	}
	for i := 0; i < stressSkipNode; i++ {
		path := filepath.Join(node, fmt.Sprintf("skip%03d.js", i))
		if err := os.WriteFile(path, []byte("// must never be indexed\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "poster.png"), []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatalf("write poster.png: %v", err)
	}
}

// TestLargeProjectIndexAndScan is the unit half of the stress check: after an
// agent creates a project with hundreds of files, the finder index, GLOB cap,
// LIST_DIR cap and fuzzy search all have to stay correct and bounded.
func TestLargeProjectIndexAndScan(t *testing.T) {
	root := t.TempDir()
	makeStressProject(t, root)
	skip := newProjectSkipFor(nil)

	start := time.Now()
	files := collectFiles(root, nil)
	t.Logf("indexed %d files in %v", len(files), time.Since(start))
	want := stressPkgs * stressPerPkg
	if len(files) != want {
		t.Fatalf("finder indexed %d files, want %d", len(files), want)
	}
	for _, f := range files {
		if strings.Contains(f, "node_modules") || strings.HasSuffix(f, ".png") {
			t.Fatalf("skipped file leaked into the finder index: %s", f)
		}
	}

	globText := globIn(root, "**/*.go", skip)
	if !strings.HasPrefix(globText, fmt.Sprintf("[GLOB **/*.go] %d match(es)", maxGlobHits)) {
		t.Fatalf("GLOB must cap at %d matches, got: %s", maxGlobHits, strings.Split(globText, "\n")[0])
	}
	if strings.Contains(globText, "node_modules") {
		t.Fatalf("GLOB returned a skipped tree: %s", globText)
	}

	env := toolEnv{root: root, skip: skip}
	listed := env.listDirWith(filepath.Join(root, "pkg00"), skip)
	if !strings.Contains(listed, fmt.Sprintf("… %d more entries omitted", stressPerPkg-maxListEntries)) {
		t.Fatalf("LIST_DIR must report omitted entries: %s", listed)
	}

	hits := searchFiles(files, "f042")
	if len(hits) == 0 || !strings.Contains(hits[0], "f042.go") {
		t.Fatalf("fuzzy search over %d files missed f042: %v", len(files), hits)
	}
}

// BenchmarkLargeProjectIndex keeps the cost of opening a generated project
// visible: collectFiles is what the fuzzy finder builds when it first opens.
func BenchmarkLargeProjectIndex(b *testing.B) {
	root := b.TempDir()
	makeStressProject(b, root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = collectFiles(root, nil)
	}
}
