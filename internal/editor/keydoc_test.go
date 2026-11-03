package editor

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"dmed/internal/config"
)

// The generated cheatsheet is only honest if the names it prints are the names
// the tables bind. These tests are that check.

// helpIndexOf reports the row whose keys column shows the given chord, or -1.
// A test asks for a chord rather than a whole column, so the order the aliases
// are listed in stays a detail of the renderer instead of an assertion.
func helpIndexOf(rows []helpRow, chord string) int {
	want := displayChord(chord)
	for i, r := range rows {
		if contains(strings.Split(r.keys, " / "), want) {
			return i
		}
		if showsDigitInRange(r.keys, chord) {
			return i
		}
	}
	return -1
}

// showsDigitInRange reports whether a column prints the chord as part of a
// collapsed family: tab.jump is nine chords and one range, Alt+1..9.
func showsDigitInRange(keys, chord string) bool {
	last := chord[len(chord)-1]
	if len(chord) < 2 || last < '0' || last > '9' {
		return false
	}
	prefix := strings.TrimSuffix(displayChord(chord), string(last))
	for _, k := range strings.Split(keys, " / ") {
		if !strings.HasPrefix(k, prefix) || !strings.Contains(k, "..") {
			continue
		}
		head, tail, ok := strings.Cut(k[len(prefix):], "..")
		if !ok {
			continue
		}
		lo, err1 := strconv.Atoi(head)
		hi, err2 := strconv.Atoi(tail)
		if err1 == nil && err2 == nil && int(last-'0') >= lo && int(last-'0') <= hi {
			return true
		}
	}
	return false
}

// TestResolveKeysIdentity is the migration proof: resolving the map with nothing
// in the [keys] section must not change one fact about the tables. Every chord
// keeps its action, the panes keep theirs, and the inverse index — which the
// hand-written tables could not express — has to hold exactly the chords that are
// bound, including the aliases.
func TestResolveKeysIdentity(t *testing.T) {
	k := ResolveKeys(config.Config{})

	for name, table := range map[string]map[string]bindingDef{
		"global": globalBindings,
		"editor": editorBindings,
	} {
		resolved := k.global
		if name == "editor" {
			resolved = k.editor
		}
		if len(resolved) != len(table) {
			t.Fatalf("%s: resolved %d chords, table has %d", name, len(resolved), len(table))
		}
		for chord, d := range table {
			if got := resolved[chord]; got.act != d.act || got.flags != d.flags {
				t.Fatalf("%s: chord %q resolved to act %q/flags %d, want %q/%d",
					name, chord, got.act, got.flags, d.act, d.flags)
			}
			// The same chord must appear under exactly that action in the index.
			if !contains(k.chords(d.act), chord) {
				t.Fatalf("%s: %s does not list chord %q", name, d.act, chord)
			}
		}
	}
	// The pane stage, chord by chord: an untouched resolution is the declaration.
	for p, table := range k.pane {
		if len(table) != len(paneDefaults[p]) {
			t.Fatalf("pane %s: resolved %d chords, the declaration binds %d",
				p, len(table), len(paneDefaults[p]))
		}
		for chord, act := range table {
			if want := paneDefaults[p][chord]; want != act {
				t.Fatalf("pane %s: chord %q resolves to %q, not to the %q it ships as",
					p, chord, act, want)
			}
		}
	}
	// The index holds nothing the stages did not bind, and holds everything they
	// bound. Compared as sets rather than as a total, because the pane stage and
	// the tables can hand the same chord to the same command and the index lists a
	// command's chords once.
	want := map[keyAct]map[string]bool{}
	bind := func(act keyAct, chord string) {
		if want[act] == nil {
			want[act] = map[string]bool{}
		}
		want[act][chord] = true
	}
	for _, table := range []map[string]bindingDef{globalBindings, editorBindings} {
		for chord, d := range table {
			bind(d.act, chord)
		}
	}
	for _, table := range paneDefaults {
		for chord, act := range table {
			bind(act, chord)
		}
	}
	if len(k.acts) != len(want) {
		t.Fatalf("the index lists %d commands, the stages bind %d", len(k.acts), len(want))
	}
	for act, chords := range want {
		got := k.acts[act]
		if len(got) != len(chords) {
			t.Fatalf("%s: the index holds %q, the stages bind %d chords", act, got, len(chords))
		}
		for _, chord := range got {
			if !chords[chord] {
				t.Fatalf("%s: the index holds chord %q that no stage binds", act, chord)
			}
		}
	}

	// Aliases, spelled out: these are the pairs the tables have always carried
	// twice, now named once.
	for act, want := range map[keyAct][]string{
		actHelpToggle:    {"ctrl+e", "f1"},
		actPaletteOpen:   {"ctrl+p", "f2"},
		actDelimiterJump: {"ctrl+]", "ctrl+}"},
		actSearchNext:    {"f3"},
	} {
		if got := k.chords(act); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: chords %q, want %q", act, got, want)
		}
	}
	if n := len(k.chords(actTabJump)); n != 9 {
		t.Fatalf("tab.jump must carry nine chords, got %d", n)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestDisplayChordSpelling(t *testing.T) {
	cases := []struct{ chord, want string }{
		{"ctrl+s", "Ctrl+S"},
		{"ctrl+shift+f5", "Ctrl+Shift+F5"},
		{"alt+down", "Alt+↓"},
		{"shift+tab", "Shift+Tab"},
		{"ctrl+alt+left", "Ctrl+Alt+←"},
		{"pgdown", "PgDn"},
		{"delete", "Del"},
		{"ctrl+\\", "Ctrl+\\"},
		{"alt+`", "Alt+`"},
		{"ctrl++", "Ctrl++"},
		{"alt+1..9", "Alt+1..9"},
	}
	for _, c := range cases {
		if got := displayChord(c.chord); got != c.want {
			t.Errorf("displayChord(%q) = %q, want %q", c.chord, got, c.want)
		}
	}
}

// Alt+` is a bound chord, so a generated table that fenced every keys cell with
// one backtick would print a span that closes in the middle of the key.
func TestCodeSpanFencesABacktickChord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Ctrl+S", "`Ctrl+S`"},
		{"Alt+` / Ctrl+Tab", "``Alt+` / Ctrl+Tab``"},
		{"`Tab", "`` `Tab ``"},
		{"``", "``` `` ```"},
	}
	for _, c := range cases {
		if got := codeSpan(c.in); got != c.want {
			t.Errorf("codeSpan(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	md, err := DumpKeys("md")
	if err != nil {
		t.Fatalf("DumpKeys: %v", err)
	}
	if !strings.Contains(md, "``Alt+` / Ctrl+Tab``") {
		t.Errorf("the MRU row must be fenced with a double backtick, got:\n%s", md)
	}
	for _, line := range strings.Split(md, "\n") {
		cell := strings.Split(strings.TrimPrefix(line, "| "), " |")
		if len(cell) != 2 || cell[0] == "" {
			continue
		}
		// A keys cell is its fence, its text and the same fence: an odd number
		// of runs means a chord broke the span open.
		if !balancedFence(cell[0]) {
			t.Errorf("keys cell %q is not a closed code span in line %q", cell[0], line)
		}
	}
}

// balancedFence reports whether the cell is one code span: it opens with a run
// of backticks and the same run closes it, with nothing else of that length
// inside.
func balancedFence(cell string) bool {
	cell = strings.TrimSpace(cell)
	if !strings.HasPrefix(cell, "`") {
		return true // a cell that is not fenced at all
	}
	n := 0
	for n < len(cell) && cell[n] == '`' {
		n++
	}
	if len(cell) < 2*n || !strings.HasSuffix(cell, strings.Repeat("`", n)) {
		return false
	}
	fence := strings.Repeat("`", n)
	return !strings.Contains(strings.TrimSuffix(strings.TrimPrefix(cell, fence), fence), fence)
}

// TestDumpKeysActionsRoundTrips: the actions list is what a user is told to read
// before writing a [keys] section, so it has to be valid input as well as valid
// output. Feeding every printed name and chord back through the resolver proves
// the spellings survive checkChord (a modifier written in the wrong order would
// be refused), that the digit family expands, and that nothing is listed under a
// name the map does not know.
func TestDumpKeysActionsRoundTrips(t *testing.T) {
	out, err := DumpKeys("actions")
	if err != nil {
		t.Fatalf("DumpKeys: %v", err)
	}
	overrides := map[string][]string{}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, chords, ok := strings.Cut(line, " ")
		if !ok || strings.TrimSpace(chords) == "" {
			t.Fatalf("line %q is not an action and its chords", line)
		}
		if _, dup := overrides[name]; dup {
			t.Fatalf("%s is listed twice: a user cannot tell which line the map reads", name)
		}
		names = append(names, name)
		overrides[name] = strings.Fields(chords)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("the list must be sorted, it is read by eye: %q", names)
	}
	if len(names) == 0 {
		t.Fatal("no actions listed")
	}

	k := ResolveKeys(config.Config{Keys: config.KeysConfig{Overrides: overrides}})
	if len(k.refusals) > 0 {
		t.Fatalf("the printed chords are not accepted as input: %q", k.refusals)
	}
	// Restating every default is a no-op: the map the dump describes is the map
	// the editor ships with. The stages are compared apart because one chord is
	// legitimately bound in both — F6 steps in the debug panel and splits outside
	// it — and a merged view would report the other table's answer as drift.
	for _, s := range []struct {
		name     string
		defaults map[string]bindingDef
		got      map[string]bindingDef
	}{
		{"global", globalBindings, k.global},
		{"editor", editorBindings, k.editor},
	} {
		if len(s.got) != len(s.defaults) {
			t.Errorf("%s: %d chords after the round trip, %d before", s.name, len(s.got), len(s.defaults))
		}
		for chord, d := range s.defaults {
			if g, ok := s.got[chord]; !ok || g.act != d.act {
				t.Errorf("%s/%s: dump says %s, resolved map says %+v ok=%v", s.name, chord, d.act, g, ok)
			}
		}
	}
	// tab.jump is the one family the dump collapses, and the collapse is the only
	// place a range appears: expanding it must give the nine chords back.
	if nine := k.chords("tab.jump"); len(nine) != 9 {
		t.Errorf("tab.jump: 9 chords expected through the range, got %q", nine)
	}
}

func TestDumpKeysRejectsAnUnknownFormat(t *testing.T) {
	if _, err := DumpKeys("yaml"); err == nil || !strings.Contains(err.Error(), "md, roff or actions") {
		t.Fatalf("an unknown format must name the three it knows, got %v", err)
	}
}

func TestCollapseRunTurnsADigitFamilyIntoARange(t *testing.T) {
	nine := make([]string, 0, 9)
	for d := '1'; d <= '9'; d++ {
		nine = append(nine, "alt+"+string(d))
	}
	if got := collapseRun(nine); len(got) != 1 || got[0] != "alt+1..9" {
		t.Fatalf("nine digit chords must collapse to one range, got %q", got)
	}
	// Anything that is not a consecutive digit run under one prefix is left
	// alone: an alias pair and a broken run.
	for _, keep := range [][]string{{"f1", "ctrl+e"}, {"alt+1", "alt+3"}, {"ctrl+1", "alt+2", "shift+3"}} {
		if got := collapseRun(keep); strings.Join(got, ",") != strings.Join(keep, ",") {
			t.Fatalf("%q must not collapse, got %q", keep, got)
		}
	}
}

// TestHelpRowsAreTheTables closes the hole the hand-copied list allowed: a
// generated row may only show chords its actions are actually bound to, and a
// chord that is bound under a documented action may not go missing.
func TestHelpRowsAreTheTables(t *testing.T) {
	m := New()
	rows := m.helpRows()
	k := m.keys

	for _, s := range helpSpecs {
		if len(s.acts) == 0 || s.keys != "" {
			continue // a separator, or a column drawn by hand
		}
		first := k.chords(s.acts[0])
		if len(first) == 0 {
			t.Fatalf("%s: an action in the help has no chord at all", s.acts[0])
		}
		at := helpIndexOf(rows, first[0])
		if at < 0 {
			t.Fatalf("%v: no row shows %q", s.acts, first[0])
		}
		if rows[at].desc != s.desc {
			t.Fatalf("%v: row is %q, want %q", s.acts, rows[at].desc, s.desc)
		}
		for _, act := range s.acts {
			for _, chord := range k.chords(act) {
				if helpIndexOf(rows, chord) < 0 {
					t.Fatalf("%s: chord %q is bound but no row shows it", act, chord)
				}
			}
		}
	}
}
