package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRealGoplsHoverReferencesSymbols drives a real gopls over the wire — the
// mock-free check that the three request shapes parse the replies of a real
// server. Skips without gopls on PATH, like the real-Delve and real-Xdebug
// tests skip without their interpreters.
func TestRealGoplsHoverReferencesSymbols(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	src := "package main\n\nimport \"fmt\"\n\n// Greeter says hello.\ntype Greeter struct{ name string }\n\nfunc (g Greeter) Greet() string {\n\treturn fmt.Sprintf(\"hi %s\", g.name)\n}\n\nfunc main() {\n\tvar g Greeter\n\t_ = g.Greet()\n\tg.name = \"x\"\n\t_ = g\n}\n"
	if err := os.WriteFile(main, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/tmp\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Start("gopls", nil, dir, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()

	if err := c.EnsureOpened(main, "go", src); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	// Hover over Greeter (line 5, col 7, 0-indexed): the doc comment comes
	// back as markup. Wait out the cold start like a completion would.
	var h *Hover
	for i := 0; i < 30; i++ {
		h, err = c.Hover(main, 5, 7)
		if err == nil && h != nil && h.Contents != "" {
			break
		}
	}
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if h == nil || h.Contents == "" {
		t.Fatal("hover over Greeter returned nothing")
	}
	if !contains(h.Contents, "Greeter") {
		t.Fatalf("hover contents %q does not name the symbol", h.Contents)
	}

	// References of Greeter: the declaration, the var and the field uses in
	// main. gopls must report at least the uses on lines 13 and 15.
	var refs []Location
	for i := 0; i < 30; i++ {
		refs, err = c.References(main, 5, 7, true)
		if err == nil && len(refs) >= 2 {
			break
		}
	}
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if len(refs) < 2 {
		t.Fatalf("references of Greeter = %d, want the uses in main", len(refs))
	}

	// DocumentSymbols: the two declarations plus the method, in file order.
	var syms []DocumentSymbol
	for i := 0; i < 30; i++ {
		syms, err = c.DocumentSymbols(main)
		if err == nil && len(syms) >= 3 {
			break
		}
	}
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	names := make([]string, 0, len(syms))
	for _, s := range syms {
		names = append(names, s.Name)
	}
	// gopls reports Greeter, Greet, main (and possibly the field).
	if !containsAll(names, "Greeter", "main") {
		t.Fatalf("symbols = %v, want Greeter and main", names)
	}
	if !sorted(syms) {
		t.Fatalf("symbols not in document order: %v", names)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func containsAll(list []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, s := range list {
			if s == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sorted(syms []DocumentSymbol) bool {
	lines := make([]int, len(syms))
	for i, s := range syms {
		lines[i] = s.Line
	}
	return reflect.DeepEqual(lines, append([]int(nil), lines...)) && isSorted(lines)
}

func isSorted(lines []int) bool {
	for i := 1; i < len(lines); i++ {
		if lines[i] < lines[i-1] {
			return false
		}
	}
	return true
}
