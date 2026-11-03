package editor

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
)

// What lives at this layer: the tab stop and the indent unit of one tab, the
// bytes a save writes, the cache that follows a rename, and the reload. The
// .editorconfig grammar and precedence are tested in internal/config, so every
// file here declares root = true — an ancestor of the test directory must never
// reach into these expectations.

var (
	tabChord      = tea.KeyPressMsg{Code: tea.KeyTab}
	shiftTabChord = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	saveChord     = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
)

func writeEditorConfig(t *testing.T, dir, body string) string {
	t.Helper()
	return writeTemp(t, dir, config.EditorConfigFile, "root = true\n\n"+body)
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestEditorConfigDrivesTheIndentUnit is the visible half of the feature: a
// project that asks for two spaces must not get a tab character out of Tab, and
// Shift+Tab must take the same unit back.
func TestEditorConfigDrivesTheIndentUnit(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*.txt]\nindent_style = space\nindent_size = 2\n")
	m := New(writeTemp(t, dir, "a.txt", "one\n"))
	m.width, m.height = 80, 24

	if got := m.indentUnit(m.cur()); got != "  " {
		t.Fatalf("indentUnit = %q, want two spaces", got)
	}
	m = press(m, tabChord)
	mustText(t, m.cur().buf.Text(), "  one\n")
	m = press(m, shiftTabChord)
	mustText(t, m.cur().buf.Text(), "one\n")
}

// TestTabStopIsPerFile: the columns a file is measured in come from that file,
// not from the editor's one number, and a file the config says nothing about
// keeps dmed's own answer.
func TestTabStopIsPerFile(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*.txt]\ntab_width = 8\n")
	m := New(writeTemp(t, dir, "a.txt", "x\n"), writeTemp(t, dir, "b.md", "y\n"))
	def := m.cfg.FileOpts()

	if got := m.tabStop(&m.tabs[0]); got != 8 {
		t.Fatalf("tabStop(txt) = %d, want 8", got)
	}
	if got := m.tabStop(&m.tabs[1]); got != def.TabWidth {
		t.Fatalf("tabStop(md) = %d, want the default %d", got, def.TabWidth)
	}
	// tab_width alone: the unit stays a tab, and one level is one tab.
	if got := m.indentUnit(&m.tabs[0]); got != "\t" {
		t.Fatalf("indentUnit(txt) = %q, want a tab", got)
	}
}

// TestTabWidthDefaultsToIndentSizeAndBack covers the two directions of the rule
// that the editorconfig spec calls "tab_width is the fallback for indent_size":
// a space file with only a tab stop indents to that stop, and a tab file with
// only an indent size displays tabs that wide.
func TestTabWidthDefaultsToIndentSizeAndBack(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[space.go]\nindent_style = space\ntab_width = 3\n"+
		"[tab.go]\nindent_style = tab\nindent_size = 5\n")
	m := New(writeTemp(t, dir, "space.go", "x\n"), writeTemp(t, dir, "tab.go", "y\n"))

	if got := m.indentUnit(&m.tabs[0]); got != "   " {
		t.Fatalf("space.go indentUnit = %q, want three spaces", got)
	}
	if got := m.tabStop(&m.tabs[1]); got != 5 {
		t.Fatalf("tab.go tabStop = %d, want 5 (indent_size is its fallback)", got)
	}
	if got := m.indentUnit(&m.tabs[1]); got != "\t" {
		t.Fatalf("tab.go indentUnit = %q, want a tab", got)
	}
}

// TestSaveWritesTheFilesOwnPolicy: insert_final_newline and
// trim_trailing_whitespace are properties of the bytes being written, so they
// appear at the save and never in the document.
func TestSaveWritesTheFilesOwnPolicy(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*]\ninsert_final_newline = false\ntrim_trailing_whitespace = true\n")
	p := writeTemp(t, dir, "a.txt", "alpha  \tb\nbeta\n")
	m := New(p)
	m.width, m.height = 80, 24

	m = press(m, saveChord)
	if got := readBack(t, p); got != "alpha  \tb\nbeta" {
		t.Fatalf("saved %q, want the trimmed text with no final newline", got)
	}
	if m.cur().buf.Dirty() {
		t.Fatal("a save that applied the policy must leave the buffer clean")
	}
}

// TestTrimAtSaveIsUndoable is the honest half of applying a rule the file asked
// for: the undo step puts the spaces back, so Ctrl+Z after a save is not a lie.
func TestTrimAtSaveIsUndoable(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*]\ntrim_trailing_whitespace = true\n")
	p := writeTemp(t, dir, "a.txt", "alpha  \nbeta\n")
	m := New(p)
	m.width, m.height = 80, 24

	m = press(m, saveChord)
	if got := readBack(t, p); got != "alpha\nbeta\n" {
		t.Fatalf("saved %q", got)
	}
	m = press(m, undoChord)
	mustText(t, m.cur().buf.Text(), "alpha  \nbeta\n")
	if !m.cur().buf.Dirty() {
		t.Fatal("undoing the trim must make the buffer differ from the file again")
	}
}

// TestSaveOfACRLFFileDropsNoCarriageReturn: the buffer holds newlines as "\n",
// so the final newline is trimmed before the endings are re-written. Trimming
// after would leave a bare "\r" as the last byte of the file.
func TestSaveOfACRLFFileDropsNoCarriageReturn(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*]\ninsert_final_newline = false\n")
	p := writeTemp(t, dir, "a.txt", "alpha\r\nbeta\r\n")
	m := New(p)
	m.width, m.height = 80, 24
	if m.cur().lineEnding != "crlf" {
		t.Fatalf("lineEnding = %q, want crlf", m.cur().lineEnding)
	}

	m = press(m, saveChord)
	if got := readBack(t, p); got != "alpha\r\nbeta" {
		t.Fatalf("saved %q, want CRLF lines and no trailing carriage return", got)
	}
}

// TestFinalNewlineIsAddedWhenTheFileAsksForOne: the buffer always carries its
// own, so the only way to see this rule is a file that does not end with one —
// and a save must end it.
func TestFinalNewlineIsAddedWhenTheFileAsksForOne(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*]\ninsert_final_newline = true\n")
	p := writeTemp(t, dir, "a.txt", "alpha")
	m := New(p)
	m.width, m.height = 80, 24

	m = press(m, saveChord)
	if got := readBack(t, p); got != "alpha\n" {
		t.Fatalf("saved %q, want a final newline", got)
	}
}

// TestFileOptsCacheFollowsThePath is why the cache key is the path and not a
// "known" flag: Save As moves the file into another directory, whose rules the
// tab must pick up rather than keep.
func TestFileOptsCacheFollowsThePath(t *testing.T) {
	dir := t.TempDir()
	deep := dir + string(os.PathSeparator) + "sub"
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, config.EditorConfigFile, "root = true\n")
	writeEditorConfig(t, deep, "[*.txt]\nindent_style = space\nindent_size = 2\n")

	m := New(writeTemp(t, deep, "a.txt", "one\n"))
	m.width, m.height = 80, 24
	if got := m.indentUnit(m.cur()); got != "  " {
		t.Fatalf("indentUnit = %q, want two spaces", got)
	}
	m.cur().path = writeTemp(t, dir, "moved.txt", "one\n")
	if got := m.indentUnit(m.cur()); got != m.cfg.FileOpts().TypedUnit() {
		t.Fatalf("after the move indentUnit = %q, want the default", got)
	}
}

// TestEditorConfigReloadDropsTheCache is the hot-reload path: the watcher hands
// the .editorconfig in as a message, and the next read walks again.
func TestEditorConfigReloadDropsTheCache(t *testing.T) {
	dir := t.TempDir()
	ec := writeEditorConfig(t, dir, "[*.txt]\nindent_style = space\nindent_size = 2\n")
	m := New(writeTemp(t, dir, "a.txt", "one\n"))
	m.width, m.height = 80, 24
	if got := m.indentUnit(m.cur()); got != "  " {
		t.Fatalf("indentUnit = %q, want two spaces", got)
	}

	writeEditorConfig(t, dir, "[*.txt]\nindent_style = space\nindent_size = 4\n")
	next, _ := m.Update(FileChangedMsg{Path: ec})
	m = next.(Model)
	if got := m.indentUnit(m.cur()); got != "    " {
		t.Fatalf("after the reload indentUnit = %q, want four spaces", got)
	}
}

// TestApplyFileSaveIsTheSavePolicyAlone: the two whitespace rules as a pure
// function, because that is the shape the tool layer needs.
func TestApplyFileSaveIsTheSavePolicyAlone(t *testing.T) {
	trim := config.FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true,
		TrimTrailingWS: true, InsertFinalNewline: true}
	final := config.FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true, InsertFinalNewline: true}
	none := config.FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true}

	cases := []struct {
		name string
		opts config.FileOpts
		in   string
		want string
	}{
		{"trim", trim, "a  \nb\t\nc  \n", "a\nb\nc\n"},
		{"add final", final, "a\nb", "a\nb\n"},
		{"empty stays empty", final, "", ""},
		{"drop final", none, "a\nb\n", "a\nb"},
		{"crlf keeps its endings", none, "a\r\nb\r\n", "a\r\nb"},
		{"crlf without the newline", none, "a\r\n", "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyFileSave(tc.opts, tc.in); got != tc.want {
				t.Fatalf("applyFileSave(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAgentContentFollowsTheFilesOwnPolicy is the second half of the rule that
// an agent's proposal is reviewed as the bytes that land: the policy is applied
// where the Change is made, not after the human has approved it. Orig is the
// one side that must stay untouched — it is what Applier.Validate compares
// against the disk.
func TestAgentContentFollowsTheFilesOwnPolicy(t *testing.T) {
	dir := t.TempDir()
	writeEditorConfig(t, dir, "[*]\ntrim_trailing_whitespace = true\ninsert_final_newline = false\n")
	writeTemp(t, dir, "a.txt", "alpha   \nbeta\n")

	env := newToolEnv(dir, configAI{}, fileFinisher(dir, config.FileOpts{
		TabWidth: 4, IndentSize: 4, UseTabs: true, InsertFinalNewline: true}))

	msg, ch := env.edit("a.txt", "alpha   \nbeta   \nchanged\n")
	if ch == nil {
		t.Fatalf("edit: %s", msg)
	}
	if ch.New != "alpha\nbeta\nchanged" {
		t.Fatalf("Change.New = %q, want the file's own save policy applied", ch.New)
	}
	if ch.Orig != "alpha   \nbeta\n" {
		t.Fatalf("Change.Orig = %q, want the bytes as they are on disk", ch.Orig)
	}

	// A proposal that differs from the file only in whitespace it does not want
	// is no proposal at all.
	if msg, ch := env.edit("a.txt", "alpha\nbeta"); ch != nil {
		t.Fatalf("expected no change, got %q", ch.New)
	} else if !strings.Contains(msg, "no change") {
		t.Fatalf("edit: %s", msg)
	}

	// REPLACE takes the same policy, so the reviewed diff is the landed file —
	// including the line nobody asked it to touch, which is exactly why the
	// policy runs before the human sees the diff and not after.
	if _, ch := env.replace("a.txt", "beta", "gamma   "); ch == nil {
		t.Fatal("replace: expected a change")
	} else if ch.New != "alpha\ngamma" {
		t.Fatalf("Change.New = %q, want both trailing runs gone", ch.New)
	}
}
