package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLargeProjectStaysResponsive is the pty half of the stress check: seed a
// project that looks like something an agent just generated, open it the way
// a user would, and drive the finder over the whole index. The screen has to
// keep painting and the keystrokes have to keep landing.
func TestLargeProjectStaysResponsive(t *testing.T) {
	const (
		pkgs     = 8
		perPkg   = 80
		rootMark = "STRESS_ROOT_NEEDLE"
	)
	dir := t.TempDir()
	writeFile(t, dir, "aaa-needle.go", "package main\n// "+rootMark+"\n")

	for p := 0; p < pkgs; p++ {
		sub := filepath.Join(dir, fmt.Sprintf("pkg%02d", p))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		for f := 0; f < perPkg; f++ {
			writeFile(t, sub, fmt.Sprintf("f%03d.go", f), fmt.Sprintf("package pkg%02d\n", p))
		}
	}

	s := Start(t, Options{Dir: dir, Args: []string{"."}})
	s.WaitFor("aaa-needle.go", settleWait)

	start := time.Now()
	s.Press("ctrl+o")
	s.Type("needle")
	s.Press("enter")
	s.WaitFor(rootMark, settleWait)
	t.Logf("finder opened a file in a %d-file project in %v", pkgs*perPkg+1, time.Since(start))

	if got := s.File("aaa-needle.go"); !strings.Contains(got, rootMark) {
		t.Fatalf("finder opened the wrong file: %q", got)
	}

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}
