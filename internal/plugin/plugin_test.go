package plugin

import (
	"os"
	"testing"

	"dmed/internal/bundled"
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

func TestReloadReplacesPlugin(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/r.lua"
	writeFile(path, `
dmed.on_key("ctrl+r", function()
  dmed.set_text("old")
end)
dmed.command("first", "First", "", function() dmed.insert("1") end)
`)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	h := &fakeHost{text: "x"}
	if !m.HasBinding("ctrl+r") {
		t.Fatal("expected binding before reload")
	}

	// Rewrite the plugin on disk and reload it.
	writeFile(path, `
dmed.on_key("ctrl+r", function()
  dmed.set_text("new")
end)
dmed.command("second", "Second", "", function() dmed.insert("2") end)
`)
	if err := m.Reload(path); err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if len(m.Names()) != 1 || m.Names()[0] != "r.lua" {
		t.Fatalf("names after reload=%v", m.Names())
	}
	// Old command is gone, new one present.
	if got := len(m.Commands()); got != 1 || m.Commands()[0].ID != "second" {
		t.Fatalf("commands after reload=%+v", m.Commands())
	}
	if m.RunCommand(h, "first") {
		t.Fatal("stale command 'first' still runnable")
	}
	// New keybinding behavior applies.
	if !m.RunBinding(h, "ctrl+r") {
		t.Fatal("RunBinding returned false after reload")
	}
	if h.text != "new" {
		t.Fatalf("text after reload=%q", h.text)
	}
}

func TestReloadReportsBrokenSource(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/b.lua"
	writeFile(path, "dmed.on_key('ctrl+x', function() dmed.set_text('ok') end)")
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	writeFile(path, "not lua {{{")
	if err := m.Reload(path); err == nil {
		t.Fatal("expected an error for broken source")
	}
	// The old plugin stays registered after a failed reload.
	if len(m.Names()) != 1 {
		t.Fatalf("names after failed reload=%v", m.Names())
	}
}

func TestManagerRemove(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/r.lua"
	writeFile(path, `dmed.command("x", "X", "", function() dmed.insert("x") end)`)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if len(m.Commands()) != 1 {
		t.Fatalf("commands=%v", m.Commands())
	}
	m.Remove(path)
	if len(m.Commands()) != 0 {
		t.Fatalf("commands after remove=%v", m.Commands())
	}
	if m.RunCommand(&fakeHost{}, "x") {
		t.Fatal("removed command still runs")
	}
	if len(m.Names()) != 0 {
		t.Fatalf("names after remove=%v", m.Names())
	}
}

// emmetHost drives the bundled emmet plugin in tests.
type emmetHost struct {
	lines    []string
	cursorL  int
	cursorC  int
	inserted string
	status   string
}

func (h *emmetHost) Text() string       { return "" }
func (h *emmetHost) SetText(s string)   {}
func (h *emmetHost) LineCount() int     { return len(h.lines) }
func (h *emmetHost) Line(i int) string  { return h.lines[i] }
func (h *emmetHost) Cursor() (int, int) { return h.cursorL, h.cursorC }
func (h *emmetHost) SetCursor(l, c int) { h.cursorL, h.cursorC = l, c }
func (h *emmetHost) Insert(s string)    { h.inserted += s }
func (h *emmetHost) Status(msg string)  { h.status = msg }
func (h *emmetHost) Save()              {}

func TestBundledEmmetExpansion(t *testing.T) {
	src := bundled.Source("emmet.lua")
	if src == "" {
		t.Fatal("emmet.lua not embedded")
	}
	dir := t.TempDir()
	writeFile(dir+"/emmet.lua", src)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	h := &emmetHost{lines: []string{"div>ul>li*2"}, cursorL: 0, cursorC: len("div>ul>li*2")}
	if !m.RunCommand(h, "emmet_expand") {
		t.Fatalf("emmet_expand command not found; commands=%v", m.Commands())
	}
	want := "<div>\n  <ul>\n    <li></li>\n    <li></li>\n  </ul>\n</div>"
	if h.inserted != want {
		t.Fatalf("emmet output=%q want %q", h.inserted, want)
	}
}

func TestBundledStoreComplete(t *testing.T) {
	for _, p := range bundled.Store {
		if src := bundled.Source(p.File); src == "" {
			t.Errorf("bundled plugin %q has no embedded source", p.File)
		}
	}
}
