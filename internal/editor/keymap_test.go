package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
)

// A [keys] section addresses a command by name, so the resolution is the one
// place a chord is decided. These tests are the contract: what moves, what
// refuses to move, and what the editor says when it refuses.

// keysOf builds a configuration carrying one [keys] section, in the shape the
// loader produces: an action name and the chords written for it.
func keysOf(lines ...string) config.Config {
	ov := map[string][]string{}
	for _, line := range lines {
		action, value, _ := strings.Cut(line, "=")
		ov[strings.ToLower(strings.TrimSpace(action))] = splitChords(strings.TrimSpace(value))
	}
	return config.Config{Keys: config.KeysConfig{Overrides: ov}}
}

// splitChords mirrors config.parseList, which the config package keeps to
// itself.
func splitChords(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func TestProtectedChordsRefuseARebind(t *testing.T) {
	k := ResolveKeys(config.Config{})
	for _, chord := range []string{"esc", "enter", "tab", "shift+tab", "backspace", "delete",
		"up", "down", "left", "right", "pgup", "pgdown", "home", "end", "ctrl+q"} {
		if !k.isProtected(chord) {
			t.Errorf("%q must be protected", chord)
		}
	}
	for _, chord := range []string{"ctrl+s", "ctrl+p", "f1", "alt+t"} {
		if k.isProtected(chord) {
			t.Errorf("%q must be rebindable", chord)
		}
	}
}

// TestResolveKeysMovesACommand is the user-visible half of the feature: the old
// chord stops answering and the new one runs the same closure.
func TestResolveKeysMovesACommand(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "a.txt", "line1\n")

	m := New(path)
	m.width, m.height = 80, 24
	m.keys = ResolveKeys(keysOf("file.save = ctrl+1"))

	if _, ok := m.keys.bound("ctrl+s"); ok {
		t.Error("ctrl+s must stop saving once the action moved")
	}
	if d, ok := m.keys.bound("ctrl+1"); !ok || d.act != actFileSave {
		t.Fatalf("ctrl+1 must run file.save, got %+v", d)
	}

	m.cur().buf.Insert('X')
	m = press(m, tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl})
	got, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(got), "X") {
		t.Fatalf("the remapped chord did not save: %q err=%v", got, err)
	}

	// The old chord is not merely absent from the map — pressing it must leave
	// the file alone, or a "move" would silently be an extra alias.
	m.cur().buf.Insert('Y')
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if got, _ = os.ReadFile(path); strings.Contains(string(got), "Y") {
		t.Fatalf("ctrl+s still saves after the move: %q", got)
	}
	m = press(m, tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl})
	if got, _ = os.ReadFile(path); !strings.Contains(string(got), "Y") {
		t.Fatalf("ctrl+1 stopped saving: %q", got)
	}

	// F1 follows the map rather than the defaults it was written from.
	i := helpIndexOf(m.helpRows(), "ctrl+1")
	if i < 0 {
		t.Fatal("help must show ctrl+1")
	}
	if strings.Contains(m.helpRows()[i].keys, "Ctrl+S") {
		t.Errorf("help must drop the chord the action left: %q", m.helpRows()[i].keys)
	}
}

func TestResolveKeysUnbindsAnAction(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.keys = ResolveKeys(keysOf("file.save ="))
	if _, ok := m.keys.bound("ctrl+s"); ok {
		t.Error("ctrl+s must be unbound")
	}
	if n := len(m.keys.chords(actFileSave)); n != 0 {
		t.Errorf("the unbound action still lists %d chords", n)
	}
	// An action with no chord has no row: help must not advertise a key that
	// does nothing.
	if helpIndexOf(m.helpRows(), "ctrl+s") >= 0 {
		t.Error("the save row must disappear with the binding")
	}
}

// TestResolveKeysKeepsThePackageTables guards the invariant the whole design
// rests on: the defaults are immutable data, a resolution is a copy over them.
func TestResolveKeysKeepsThePackageTables(t *testing.T) {
	ResolveKeys(keysOf("file.save = ctrl+1", "tab.jump = ctrl+2..9"))
	for _, chord := range []string{"ctrl+1", "ctrl+2", "ctrl+9"} {
		if _, ok := globalBindings[chord]; ok {
			t.Errorf("the default global table was written through: %s", chord)
		}
		if _, ok := editorBindings[chord]; ok {
			t.Errorf("the default editor table was written through: %s", chord)
		}
	}
	if d, ok := defaultDefs()["ctrl+s"]; !ok || d.act != actFileSave {
		t.Error("ctrl+s must still be bound in the defaults")
	}
	for _, chord := range []string{"alt+1", "alt+9"} {
		if d, ok := editorBindings[chord]; !ok || d.act != actTabJump {
			t.Errorf("the default tab jump %q was damaged", chord)
		}
	}
}

func TestResolveKeysRefusesWhatWouldStrandTheUser(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"the exit cannot be unbound", "app.quit =", "protected"},
		{"a protected chord stays", "file.save = ctrl+q", "protected"},
		{"a chord another action owns", "file.save = ctrl+p", "palette.open"},
		{"an action that does not exist", "no.such.action = ctrl+1", "no such action"},
		{"a chord no terminal can report", "file.save = shift+g", "arrives as the key itself"},
		{"a chord spelled backwards", "file.save = shift+ctrl+s", "in that order"},
		{"a key that is not a key", "file.save = ctrl+hyper", "not a key"},
	}
	for _, c := range cases {
		k := ResolveKeys(keysOf(c.line))
		if len(k.refusals) == 0 {
			t.Errorf("%s: %q was accepted", c.name, c.line)
			continue
		}
		// One line can produce several refusals; any of them may carry the reason.
		all := strings.Join(k.refusals, " | ")
		if !strings.Contains(all, c.want) {
			t.Errorf("%s: refusals %q do not mention %q", c.name, all, c.want)
		}
	}
}

// TestResolveKeysKeepsAProtectedChordWorking: a line may add aliases to a
// protected command; all it loses is the power to take the chord away.
func TestResolveKeysKeepsAProtectedChordWorking(t *testing.T) {
	k := ResolveKeys(keysOf("app.quit = ctrl+1"))
	if d, ok := k.global["ctrl+q"]; !ok || d.act != actAppQuit {
		t.Error("ctrl+q must stay bound: it is the guaranteed exit")
	}
	if d, ok := k.global["ctrl+1"]; !ok || d.act != actAppQuit {
		t.Error("the alias must still be added")
	}
}

// TestResolveKeysSwapsTwoChords: the two lines name each other's key, so a
// single pass refuses the first over a chord the second has not released yet.
// A swap is the most obvious thing a user with a Vim background will write.
func TestResolveKeysSwapsTwoChords(t *testing.T) {
	k := ResolveKeys(keysOf("file.save = ctrl+k", "lsp.hover = ctrl+s"))
	if len(k.refusals) != 0 {
		t.Fatalf("a swap was refused: %q", k.refusals)
	}
	if d, ok := k.bound("ctrl+k"); !ok || d.act != actFileSave {
		t.Errorf("ctrl+k must run file.save, got %+v ok=%v", d, ok)
	}
	if d, ok := k.bound("ctrl+s"); !ok || d.act != actLspHover {
		t.Errorf("ctrl+s must run lsp.hover, got %+v ok=%v", d, ok)
	}
}

// A protected chord written again is the user agreeing with the default, not an
// attempt to move it — so it must not be reported as a refusal.
func TestResolveKeysAcceptsAProtectedChordRestated(t *testing.T) {
	k := ResolveKeys(keysOf("cursor.clear-extra = esc"))
	if len(k.refusals) != 0 {
		t.Errorf("keeping Esc as it is was refused: %q", k.refusals)
	}
	if d, ok := k.bound("esc"); !ok || d.act != actCursorClearExtra {
		t.Errorf("esc must stay on cursor.clear-extra, got %+v ok=%v", d, ok)
	}
}

// TestResolveKeysRestatesAFallthroughChord: F6 and F7 ship in both tables under
// two different actions, because the global one declines outside the debug panel
// and the text pane then splits. A line naming such a chord restates one half of
// that pair, so it must neither be refused as a conflict nor delete the other
// half — otherwise copying the shipped keys back into the file, which is exactly
// what `dmed --dump-keys actions` invites, would silently unbind a command.
func TestResolveKeysRestatesAFallthroughChord(t *testing.T) {
	k := ResolveKeys(keysOf("split.vertical = f6"))
	if len(k.refusals) != 0 {
		t.Fatalf("restating the default was refused: %q", k.refusals)
	}
	if d, ok := k.global["f6"]; !ok || d.act != actDebugStepOver {
		t.Errorf("the debug stage must keep f6, got %+v ok=%v", d, ok)
	}
	if d, ok := k.editor["f6"]; !ok || d.act != actSplitVertical {
		t.Errorf("the text stage must keep f6, got %+v ok=%v", d, ok)
	}
	// The pairing is machinery, not a claim a third action may make.
	k = ResolveKeys(keysOf("file.save = f6"))
	if all := strings.Join(k.refusals, " "); !strings.Contains(all, "f6 already runs debug.step-over") {
		t.Errorf("a foreign claim on a fallthrough chord must still be refused, got %q", all)
	}
}

// TestResolveKeysConflictIsDeterministic: one chord runs one command, so two
// lines asking for the same chord have to resolve the same way every start —
// map iteration order cannot be what decides it.
func TestResolveKeysConflictIsDeterministic(t *testing.T) {
	for i := 0; i < 20; i++ {
		k := ResolveKeys(keysOf("debug.panel = ctrl+9", "file.save = ctrl+9"))
		if d, ok := k.global["ctrl+9"]; !ok || d.act != actDebugPanel {
			t.Fatalf("debug.panel sorts first and must win, got %+v ok=%v", d, ok)
		}
		if !strings.Contains(strings.Join(k.refusals, " "), "already runs debug.panel") {
			t.Fatalf("the loser must be reported, got %q", k.refusals)
		}
	}
}

func TestExpandChordRange(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alt+1..9", "alt+1 alt+2 alt+3 alt+4 alt+5 alt+6 alt+7 alt+8 alt+9"},
		{"Ctrl+1..3", "ctrl+1 ctrl+2 ctrl+3"},
		{"ctrl+s", "ctrl+s"},
		{"alt+9..1", "alt+9..1"},   // reversed: left for checkChord to reject
		{"alt+a..c", "alt+a..c"},   // not digits
		{"alt+1..12", "alt+1..12"}, // wider than a digit run
		{"1..9", "1..9"},           // no prefix for the range to repeat
	}
	for _, c := range cases {
		if got := strings.Join(expandChord(c.in), " "); got != c.want {
			t.Errorf("expandChord(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCheckChordAcceptsWhatTheTablesBind: every built-in chord must pass the
// check a user's chord passes, or the parser refuses spellings dmed itself uses.
func TestCheckChordAcceptsWhatTheTablesBind(t *testing.T) {
	for _, table := range []map[string]bindingDef{globalBindings, editorBindings} {
		for chord := range table {
			for _, c := range expandChord(chord) {
				if why := checkChord(c); why != "" {
					t.Errorf("the default chord %q is refused: %s", chord, why)
				}
			}
		}
	}
}

func TestCheckChordReasons(t *testing.T) {
	accepted := []string{"esc", "tab", "f5", "up", "ctrl+[", "alt+`", "ctrl++",
		"delete", "shift+tab", "ctrl+shift+g", "alt+shift+down", "ctrl+9"}
	for _, in := range accepted {
		if why := checkChord(in); why != "" {
			t.Errorf("checkChord(%q) = %q, want it accepted", in, why)
		}
	}
	rejected := map[string]string{
		"":              "empty",
		"ctrl+":         "no key after the modifiers",
		"ctrl+ctrl+s":   "written twice",
		"shift+alt+s":   "in that order",
		"ctrl+hyper":    "not a key",
		"ctrl+f13":      "not a key",
		"shift+g":       "arrives as the key itself",
		"ctrl+alt+alt+": "written twice",
	}
	for in, want := range rejected {
		if why := checkChord(in); !strings.Contains(why, want) {
			t.Errorf("checkChord(%q) = %q, want it to mention %q", in, why, want)
		}
	}
}

// TestRemappedTabJumpStillEscapesThePicturePane: the guard that lets alt+digit
// through a pane claiming every key asks the map, so it follows a rebind.
func TestRemappedTabJumpStillEscapesThePicturePane(t *testing.T) {
	m := New()
	m.keys = ResolveKeys(keysOf("tab.jump = ctrl+1..4"))
	for _, chord := range []string{"ctrl+1", "ctrl+4"} {
		if !m.keys.isTabJump(chord) {
			t.Errorf("%s must be exempt as a tab jump", chord)
		}
	}
	if m.keys.isTabJump("alt+1") {
		t.Error("alt+1 left the tab family and lost the exemption")
	}
}

// TestRefusalsReachTheStatusLine: a binding that silently does not move is the
// failure this feature exists to avoid, so a refusal has to reach the screen —
// at startup and on every reload of the file that said it.
func TestRefusalsReachTheStatusLine(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if msg := m.keysRefused(); msg != "" {
		t.Errorf("an untouched config must say nothing, got %q", msg)
	}

	dir := t.TempDir()
	writeTemp(t, dir, ".dmed.conf", "[keys]\napp.quit =\n")
	m = New(dir)
	m.width, m.height = 80, 24
	msg := m.keysRefused()
	if !strings.Contains(msg, "ctrl+q") || !strings.HasPrefix(msg, "1 ") {
		t.Fatalf("status must name the count and the chord, got %q", msg)
	}
	// The reload route reads the same file and says the same thing.
	next, _ := m.Update(FileChangedMsg{Path: filepath.Join(dir, ".dmed.conf")})
	m = next.(Model)
	if !strings.Contains(m.msg, "ctrl+q") {
		t.Errorf("reloading a config with a refused line must report it, msg = %q", m.msg)
	}
}
