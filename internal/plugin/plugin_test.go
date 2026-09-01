package plugin

import (
	"os"
	"testing"
)

// fakeHost is a minimal Host implementation for tests.
type fakeHost struct {
	text    string
	cursorL int
	cursorC int
	status  string
	saves   int
}

func (f *fakeHost) Text() string       { return f.text }
func (f *fakeHost) SetText(s string)   { f.text = s }
func (f *fakeHost) LineCount() int     { return len([]rune(f.text)) }
func (f *fakeHost) Line(i int) string  { _ = i; return "" }
func (f *fakeHost) Cursor() (int, int) { return f.cursorL, f.cursorC }
func (f *fakeHost) SetCursor(l, c int) { f.cursorL, f.cursorC = l, c }
func (f *fakeHost) Insert(s string)    { f.text += s }
func (f *fakeHost) Status(msg string)  { f.status = msg }
func (f *fakeHost) Save()              { f.saves++ }

func writePlug(t *testing.T, dir, name, src string) {
	t.Helper()
	path := dir + "/" + name
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestManagerLoadAndKeybinding(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "test.lua", `
dmed.on_key("ctrl+k", function()
  dmed.set_text("hi from lua")
end)
`)
	m := New()
	errs := m.Load(dir)
	if len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if len(m.Names()) != 1 || m.Names()[0] != "test.lua" {
		t.Fatalf("names=%v", m.Names())
	}
	if !m.HasBinding("ctrl+k") {
		t.Fatal("expected binding ctrl+k")
	}
	h := &fakeHost{text: "x"}
	if !m.RunBinding(h, "ctrl+k") {
		t.Fatal("RunBinding returned false")
	}
	if h.text != "hi from lua" {
		t.Fatalf("text=%q", h.text)
	}
}

func TestManagerCommandAndBufferAPI(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "cmd.lua", `
dmed.command("hello", "Greet", "Insert a greeting", function()
  dmed.insert("!" )
  dmed.status("done")
end)
dmed.on("ready", function()
  dmed.set_cursor(3, 5)
end)
`)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	cmds := m.Commands()
	if len(cmds) != 1 || cmds[0].ID != "hello" || cmds[0].Title != "Greet" {
		t.Fatalf("commands=%+v", cmds)
	}
	h := &fakeHost{text: "abc"}
	if !m.RunCommand(h, "hello") {
		t.Fatal("command not run")
	}
	if h.text != "abc!" || h.status != "done" {
		t.Fatalf("text=%q status=%q", h.text, h.status)
	}
	m.Emit(h, "ready")
	if h.cursorL != 3 || h.cursorC != 5 {
		t.Fatalf("cursor after ready event=%d,%d", h.cursorL, h.cursorC)
	}
}

func TestLoadErrorsAreReported(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "bad.lua", "this is not lua {{{")
	m := New()
	errs := m.Load(dir)
	if len(errs) == 0 {
		t.Fatal("expected a load error")
	}
	if len(m.Names()) != 0 {
		t.Fatalf("bad plugin must not be registered, got %v", m.Names())
	}
}
