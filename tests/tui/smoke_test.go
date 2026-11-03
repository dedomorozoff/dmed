package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const settleWait = 20 * time.Second

// writeFile seeds a file inside the session directory.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// WaitFile polls a file on disk until it holds want, so an asynchronous save
// is checked for what it wrote rather than for how long it took.
func (s *Session) WaitFile(rel, want string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	var got string
	for {
		if b, err := os.ReadFile(filepath.Join(s.dir, rel)); err == nil {
			got = string(b)
			if strings.Contains(got, want) {
				return
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s never held %q (last read %q) within %v\nscreen:\n%s",
				rel, want, got, d, s.Screen())
		}
		time.Sleep(pollEvery)
	}
}

// TestTypeThenSave writes through the real input path and the real save: the
// keystroke arrives as pty bytes, the frame repaints, and the proof is the
// file on disk — not a model call.
func TestTypeThenSave(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "seed.txt", "one\n")

	s := Start(t, Options{Dir: dir, Args: []string{"seed.txt"}})
	s.WaitFor("one", settleWait)

	s.Type("X")
	s.Press("ctrl+s")
	s.WaitFile("seed.txt", "Xone", settleWait)

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}

// TestFinderOpensProjectFile drives the fuzzy finder over an opened folder and
// asserts on the buffer body, because the tree panel already shows the name.
func TestFinderOpensProjectFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha.txt", "ALPHA-BODY\n")
	writeFile(t, dir, "beta.txt", "BETA-BODY\n")

	s := Start(t, Options{Dir: dir, Args: []string{"."}})
	s.WaitFor("beta.txt", settleWait)

	s.Press("ctrl+o")
	s.Type("beta")
	s.Press("enter")
	s.WaitFor("BETA-BODY", settleWait)

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}

// TestTerminalRoundTripThroughThePalette docks a real shell inside the editor
// and proves that text goes in and comes back. Alt+T is the shorter route, but
// ConPTY drops ESC+letter, so the palette reaches the command instead — the
// very action the binding runs.
func TestTerminalRoundTripThroughThePalette(t *testing.T) {
	dir := t.TempDir()
	s := Start(t, Options{Dir: dir})

	s.Press("ctrl+p")
	// Filtered: with an empty query the command is far down a list of dozens.
	s.Type("term")
	s.WaitFor("View: Toggle Terminal", settleWait)

	s.Press("enter")
	s.WaitAbsent("View: Toggle Terminal", settleWait)

	s.Type("echo dmed-tui-ok")
	s.Press("enter")
	// Twice: the shell echoes what was typed, then prints the result. Only the
	// second one can have come back through the editor's own pty.
	s.WaitForAtLeast("dmed-tui-ok", 2, settleWait)

	// The shell ending must not end the editor. Alt+T would close the panel
	// from the keyboard, but ConPTY drops ESC+letter, so the exit is asked of
	// the shell itself — and Ctrl+Q stays with the shell afterwards, which is
	// why this scenario is the one that does not check the quit path.
	s.Type("exit")
	s.Press("enter")
	time.Sleep(500 * time.Millisecond)
	if !s.Running() {
		t.Fatalf("the shell taking down the editor\nscreen:\n%s", s.Screen())
	}
}

// TestFindOverlayOpensFiltersAndCloses drives the in-file find, the pane whose
// keys are handled inside its own overlay handler rather than the shared
// tables, and checks that searching never touches the file.
func TestFindOverlayOpensFiltersAndCloses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "seed.txt", "alpha\nbeta\ngamma\n")

	s := Start(t, Options{Dir: dir, Args: []string{"seed.txt"}})
	s.WaitFor("gamma", settleWait)

	s.Press("ctrl+f")
	s.WaitFor("search:", settleWait)

	s.Type("gam")
	s.WaitFor("search: gam", settleWait)

	s.Press("enter")
	s.Press("esc")
	s.WaitAbsent("search:", settleWait)

	if got := s.File("seed.txt"); got != "alpha\nbeta\ngamma\n" {
		t.Errorf("finding text rewrote the file: %q", got)
	}

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}

// TestCtrlCDoesNotKillTheEditor is the guarantee main.go spells out: Ctrl+C
// must never interrupt an editor that holds unsaved buffers. The unit tests
// cannot reach that filter — it lives in main, outside the model — so the
// assertion belongs to a harness that runs the real binary.
func TestCtrlCDoesNotKillTheEditor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "seed.txt", "one\n")

	s := Start(t, Options{Dir: dir, Args: []string{"seed.txt"}})
	s.WaitFor("one", settleWait)

	s.Press("ctrl+c")
	s.Press("ctrl+c")
	if !s.Running() {
		t.Fatalf("Ctrl+C killed the editor\nscreen:\n%s", s.Screen())
	}
	s.WaitFor("one", settleWait)

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}
