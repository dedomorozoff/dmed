// Package tui_test drives the built dmed binary inside a real pseudo-terminal
// and asserts on the screen it paints, the files it writes and the way it
// exits. The unit tests reach the model; this reaches the frame. See
// "Verifying the TUI" in AGENTS.md for what a pty can and cannot carry.
//
// The transport is the editor's own internal/ptyterm — ConPTY on Windows,
// creack/pty elsewhere — and the screen is read back through vt10x, the same
// emulator the built-in terminal panel uses. No Python, no winpty.
//
// What a pty does and does not carry: a control byte (Ctrl+S) and a static
// escape sequence (Enter, Down) arrive exactly as written, so every scenario
// is built from those. ESC+letter does not — ConPTY's VT input parser consumes
// the pair as an unrecognized sequence and drops it, which is why alt chords
// are not driven here; the command palette reaches the same commands through
// Ctrl+P, and the unit tests cover the alt bindings themselves.
package tui_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hinshun/vt10x"

	"dmed/internal/ptyterm"
)

// The harness always passes a real window size. Headless ptys can report a
// zero one, which is why the editor guards against a non-positive
// WindowSizeMsg — that guard stays, the harness simply never provokes it.
const (
	harnessCols = 120
	harnessRows = 30

	firstFrameWait = 10 * time.Second
	pollEvery      = 20 * time.Millisecond
)

// Options configures one editor session.
type Options struct {
	Dir  string   // working directory for the child; empty is a fresh temp dir
	Args []string // editor arguments: a file, a directory, nothing
	Env  []string // extra KEY=value entries, appended after the harness defaults
	Cols int      // window columns; 0 is harnessCols
	Rows int      // window rows; 0 is harnessRows
}

// Session is a running editor in a pty with a virtual screen beside it.
type Session struct {
	t   *testing.T
	dir string

	term *ptyterm.Terminal
	vt   vt10x.Terminal

	mu      sync.Mutex
	sawByte bool

	exitErr chan error
}

// Start builds (or reuses) the binary, spawns it in a pty and returns a
// session attached to its screen.
func Start(t *testing.T, o Options) *Session {
	t.Helper()
	if testing.Short() {
		t.Skip("TUI harness spawns a real terminal; skipped with -short")
	}
	bin, err := binaryPath()
	if err != nil {
		t.Fatalf("harness cannot build dmed: %v", err)
	}

	cols, rows := o.Cols, o.Rows
	if cols <= 0 {
		cols = harnessCols
	}
	if rows <= 0 {
		rows = harnessRows
	}
	dir := o.Dir
	if dir == "" {
		dir = t.TempDir()
	}

	// The child must not see the developer's own configuration: HOME and
	// USERPROFILE point at the session directory so ~/.dmed.conf,
	// ~/.dmed_session.json and ~/.dmed/plugins stay out of the picture, and
	// the UI language is pinned because the assertions read English labels.
	env := []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"DMED_LANG=en",
		"HOME=" + dir,
		"USERPROFILE=" + dir,
	}
	env = append(env, o.Env...)

	term, err := ptyterm.Start(ptyterm.Options{
		Command: bin, Args: o.Args, Dir: dir, Env: env,
		Width: cols, Height: rows,
	})
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}

	vt := vt10x.New(vt10x.WithSize(cols, rows))
	s := &Session{t: t, dir: dir, term: term, vt: vt, exitErr: make(chan error, 1)}
	t.Cleanup(s.Close)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := term.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.sawByte = true
				s.mu.Unlock()
				_, _ = vt.Write(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()
	go func() { s.exitErr <- term.Wait() }()

	// The editor is not ready when the process starts: the first frame takes
	// seconds (plugins, the session, the chroma registry), and a keystroke
	// written into the pty before the console input exists is simply lost. So
	// the harness waits for the painted status bar and for the frame to settle
	// before any scenario touches the keyboard.
	s.WaitReady(defaultSettle)
	return s
}

// statusMarker is the tail of the status bar's panel-icon row, the one string
// every layout paints and no other row contains.
const statusMarker = "✦  ◉  ❯"

const readyWait = 20 * time.Second

// defaultSettle is how long the screen must hold still before the harness
// treats the frame as finished.
const defaultSettle = 150 * time.Millisecond

// WaitReady blocks until the editor has painted its status bar and the screen
// has stopped changing for settle. It is what makes a scenario deterministic:
// pressing a chord against a starting-up editor loses the keystroke.
func (s *Session) WaitReady(settle time.Duration) {
	s.t.Helper()
	// A child that never wrote a single byte means the pty or the binary is
	// broken on this machine, which is an environment fact: skip. Once
	// anything has been painted, a missing status bar is a regression: fail.
	if !s.waitFirstOutput(firstFrameWait) {
		s.t.Skipf("terminal produced no output in %v", firstFrameWait)
	}
	deadline := time.Now().Add(readyWait)
	for !strings.Contains(s.Screen(), statusMarker) {
		if time.Now().After(deadline) {
			s.t.Fatalf("the editor never painted its status bar within %v\n%s", readyWait, s.Screen())
		}
		time.Sleep(pollEvery)
	}
	// The startup sequence repaints a few times after the first frame (the
	// terminal title, the session, the syntax pass); a change resets the clock.
	stable := time.Now()
	last := s.Screen()
	for time.Since(stable) < settle {
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen kept repainting for over %v\n%s", readyWait, last)
		}
		time.Sleep(pollEvery)
		now := s.Screen()
		if now != last {
			stable = time.Now()
			last = now
		}
	}
}

func (s *Session) waitFirstOutput(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := s.sawByte
		s.mu.Unlock()
		if got {
			return true
		}
		time.Sleep(pollEvery)
	}
	return false
}

// Dir is the session's working directory, also the home the child was given.
func (s *Session) Dir() string { return s.dir }

// Type writes literal text to the editor.
func (s *Session) Type(text string) {
	s.t.Helper()
	if _, err := s.term.Write([]byte(text)); err != nil {
		s.t.Fatalf("write %q to the editor: %v", text, err)
	}
}

// Press sends each named chord as the bytes a terminal would send for it.
// Control bytes and static sequences arrive exactly as written; alt chords do
// not survive ConPTY, so a scenario that needs a command reachable by alt
// presses the palette instead.
func (s *Session) Press(chords ...string) {
	s.t.Helper()
	for _, name := range chords {
		b, ok := encodeChord(name)
		if !ok {
			s.t.Fatalf("the harness has no encoding for chord %q", name)
		}
		if _, err := s.term.Write(b); err != nil {
			s.t.Fatalf("press %q: %v", name, err)
		}
	}
}

// Rows is the painted screen, one entry per row with the padding of each line
// trimmed. The trailing-space trim matters: lipgloss pads a style's
// background to the full width, and the padding is not content.
//
// String is not wrapped in vt.Lock: unlike Cell and Size, it takes the state
// lock itself, and locking around it deadlocks against the reader goroutine.
func (s *Session) Rows() []string {
	dump := s.vt.String()
	lines := strings.Split(dump, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Screen is Rows joined back into one string, for substring assertions.
func (s *Session) Screen() string { return strings.Join(s.Rows(), "\n") }

// WaitFor polls the screen until it holds sub, and returns the whole screen.
// The editor repaints asynchronously after a keystroke, so reading once right
// after writing is how a test becomes a flake.
func (s *Session) WaitFor(sub string, d time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for {
		screen := s.Screen()
		if strings.Contains(screen, sub) {
			return screen
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("screen never showed %q within %v\n%s", sub, d, screen)
		}
		time.Sleep(pollEvery)
	}
}

// WaitForAtLeast polls the screen until sub appears n times. The terminal
// scenario needs it: a shell echoes the typed command and then prints its
// output, so a token that appears twice is proof it went in and came back.
func (s *Session) WaitForAtLeast(sub string, n int, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	var got int
	for {
		got = strings.Count(s.Screen(), sub)
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("screen showed %q %d times, wanted %d, within %v\n%s",
				sub, got, n, d, s.Screen())
		}
		time.Sleep(pollEvery)
	}
}

// WaitAbsent polls the screen until sub is gone.
func (s *Session) WaitAbsent(sub string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for {
		if !strings.Contains(s.Screen(), sub) {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("screen still shows %q after %v\n%s", sub, d, s.Screen())
		}
		time.Sleep(pollEvery)
	}
}

// File reads a file under the session directory, so a save is checked on disk
// and not only in the tab that painted it.
func (s *Session) File(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, rel))
	if err != nil {
		s.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// WaitExit blocks until the editor process ends and fails the test if it ends
// with an error. The quit path is a normal exit; a crash is a regression.
func (s *Session) WaitExit(d time.Duration) {
	s.t.Helper()
	select {
	case err := <-s.exitErr:
		if err != nil {
			s.t.Fatalf("dmed exited with %v\nscreen:\n%s", err, s.Screen())
		}
	case <-time.After(d):
		s.t.Fatalf("dmed did not exit within %v\nscreen:\n%s", d, s.Screen())
	}
}

// Running reports whether the editor is still up.
func (s *Session) Running() bool {
	select {
	case <-s.exitErr:
		return false
	default:
		return true
	}
}

func (s *Session) Close() {
	_ = s.term.Close()
}

// binaryPath is the dmed the harness runs. DMED_BIN names an already built one
// (the CI jobs build the binary for their own platform before running this),
// otherwise the package is compiled into a temp dir so a local run never
// touches the dmed.exe sitting in the workspace.
var (
	buildOnce sync.Once
	builtPath string
	builtErr  error
)

func binaryPath() (string, error) {
	buildOnce.Do(func() { builtPath, builtErr = buildBinary() })
	return builtPath, builtErr
}

func buildBinary() (string, error) {
	if p := os.Getenv("DMED_BIN"); p != "" {
		abs := p
		if !filepath.IsAbs(abs) {
			root, err := repoRoot()
			if err != nil {
				return "", err
			}
			abs = filepath.Join(root, p)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("DMED_BIN %s: %w", abs, err)
		}
		return abs, nil
	}

	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	name := "dmed"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir, err := os.MkdirTemp("", "dmed-tui-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	// GOPROXY and GOSUMDB come from the environment: the Makefile and the CI
	// workflows export them, and the module cache is already warm here.
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	return bin, nil
}

// repoRoot is the module root, two levels above tests/tui.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(wd, "..", ".."))
}
