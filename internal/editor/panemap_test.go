package editor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The pane ledger is data the handlers read at run time and a human edits by hand,
// so these two tests are the pair that keeps it true: one reads the declarations,
// one reads the source that dispatches on them.

// TestPaneCommandsAreWellFormed checks the declaration on its own terms: a command
// that could never fire, or that two lines of config would mean differently, is a
// defect in the ledger rather than a taste.
func TestPaneCommandsAreWellFormed(t *testing.T) {
	tableActs := map[keyAct]bool{}
	for _, table := range []map[string]bindingDef{globalBindings, editorBindings} {
		for _, d := range table {
			tableActs[d.act] = true
		}
	}
	for _, p := range sortedPaneIDs() {
		seenAct := map[keyAct]bool{}
		seenChord := map[string]keyAct{}
		for _, c := range paneCommands[p] {
			if seenAct[c.act] {
				t.Errorf("pane %s: command %q is declared twice", p, c.act)
			}
			seenAct[c.act] = true
			// One name, one stage. A pane command that shared a table action's name
			// would be answered by whichever branch the resolver asked first, and a
			// [keys] line could not say which one it meant.
			if tableActs[c.act] {
				t.Errorf("pane %s: %q is already the name of a table action", p, c.act)
			}
			if !strings.HasPrefix(string(c.act), string(p)+".") {
				t.Errorf("pane %s: command %q does not start with the pane's own prefix %q",
					p, c.act, string(p)+".")
			}
			if len(c.chords) == 0 {
				t.Errorf("%s: a command with no chord can never run", c.act)
			}
			for _, chord := range c.chords {
				if why := checkChord(chord); why != "" {
					t.Errorf("%s: chord %q cannot arrive as written: %s", c.act, chord, why)
				}
				if other, taken := seenChord[chord]; taken {
					t.Errorf("pane %s: chord %q is declared for both %s and %s", p, chord, other, c.act)
				}
				seenChord[chord] = c.act
			}
		}
	}
}

func sortedPaneIDs() []paneID {
	out := make([]paneID, 0, len(paneCommands))
	for p := range paneCommands {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestKeysSectionRekeysAPane is the reason the ledger exists: a line in
// .dmed.conf moves the key inside the prompt, and the prompt's code does not move.
func TestKeysSectionRekeysAPane(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "a.txt", "one\n")

	m := New(path)
	m.width, m.height = 80, 24
	m.keys = ResolveKeys(keysOf("find.regex = ctrl+shift+y"))
	if len(m.keys.refusals) != 0 {
		t.Fatalf("refusals: %v", m.keys.refusals)
	}

	m.startSearch()
	m = press(m, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl | tea.ModShift})
	if !m.searchRegex {
		t.Fatal("ctrl+shift+y must toggle the find prompt's regex mode")
	}
	// Tab shipped with the command and the line did not restate it, so it left with
	// the command. What it must not do is type a tab character into the query: that
	// is the one job of the paneUnbound arm, and the reason an unbound chord and an
	// unknown key are not the same answer.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab, Text: "\t"})
	if !m.searchRegex {
		t.Fatal("tab still toggles the regex mode: the command was supposed to move")
	}
	if got := string(m.searchQuery); got != "" {
		t.Fatalf("the unbound chord typed %q into the prompt", got)
	}
}

// TestKeysSectionRefusesWhatAPaneCannotAnswer covers the three ways a pane line can
// be wrong, and that a refused line leaves the panel exactly as it shipped.
func TestKeysSectionRefusesWhatAPaneCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "a.txt", "one\n")

	m := New(path)
	m.width, m.height = 80, 24
	m.keys = ResolveKeys(keysOf(
		"find.close = ctrl+x",
		"find.next = ctrl+g",
		"replace.nope = f4",
	))
	joined := strings.Join(m.keys.refusals, "\n")
	for _, want := range []string{
		"esc is protected",
		"ctrl+x is protected",
		"ctrl+g answers globally as view.git",
		"replace.nope: no such action",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusals %q do not mention %q", joined, want)
		}
	}

	m.startSearch()
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc, Text: "\x1b"})
	if m.searchOpen {
		t.Fatal("escape must still close the prompt: a refused line changes nothing")
	}
}

// TestPaneChordsReachTheCommandTheyDeclare is the shipped behaviour, restated as
// data: every declared chord of every migrated pane must resolve to the command that
// declares it. It is the test a hand-written switch could not have, because there the
// chord and the arm were the same line of code.
func TestPaneChordsReachTheCommandTheyDeclare(t *testing.T) {
	m := New(writeTemp(t, t.TempDir(), "a.txt", "one\n"))
	m.width, m.height = 80, 24
	for _, p := range sortedPaneIDs() {
		for _, c := range paneCommands[p] {
			for _, chord := range c.chords {
				if got := m.paneCmd(p, chord); got != c.act {
					t.Errorf("pane %s: %q reaches %q, want %q", p, chord, got, c.act)
				}
			}
		}
	}
}

// paneSite is one pane as the source shows it: the command labels its migrated
// handlers carry, collected across every function that dispatches for it.
type paneSite struct {
	labels map[string]bool
}

// TestPaneHandlersDispatchOnCommands reads the package's own source. A declaration
// the handler does not answer, or an arm the declaration does not name, is exactly
// the drift a [keys] line would hit: the config would rewrite a key that the panel
// ignores, or refuse to rewrite one the panel still matches by hand.
func TestPaneHandlersDispatchOnCommands(t *testing.T) {
	sites := scanPaneSites(t)
	if len(sites) == 0 {
		t.Fatal("no pane handler dispatches on paneCmd: the scan is broken, not migrated")
	}
	// Every declared pane must be used, and every used pane declared.
	for p := range paneCommands {
		if _, ok := sites[p]; !ok {
			t.Errorf("pane %s is declared but no handler dispatches on it", p)
		}
	}
	for p, site := range sites {
		declared := map[keyAct]bool{}
		for _, c := range paneCommands[p] {
			declared[c.act] = true
		}
		for label := range site.labels {
			if label == string(paneUnbound) {
				continue
			}
			if !declared[keyAct(label)] {
				t.Errorf("pane %s: handler answers %q, which the declaration does not name", p, label)
			}
			delete(declared, keyAct(label))
		}
		for act := range declared {
			t.Errorf("pane %s: %s is declared but no arm runs it", p, act)
		}
	}
}

// scanPaneSites parses every non-test file of the package and reports, per pane,
// what its handlers actually dispatch on.
func scanPaneSites(t *testing.T) map[paneID]*paneSite {
	t.Helper()
	names := packageSourceFiles(t)
	fset := token.NewFileSet()
	sites := map[paneID]*paneSite{}
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			scanFunc(t, name, fd, sites)
		}
	}
	return sites
}

// scanFunc reads one handler. A function that dispatches on command names has been
// migrated, so it must not also match the chord by hand anywhere inside itself: an
// `if s == "y"` next to a migrated switch is how a rebind silently stops working,
// and no run-time test sees it.
func scanFunc(t *testing.T, file string, fd *ast.FuncDecl, sites map[paneID]*paneSite) {
	t.Helper()
	var panes []paneID
	var bare []*ast.SwitchStmt
	var cmp []string
	labels := map[paneID]map[string]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SwitchStmt:
			if p, ok := paneCmdSwitch(v.Tag); ok {
				panes = append(panes, p)
				if labels[p] == nil {
					labels[p] = map[string]bool{}
				}
				var hasDefault, hasSentinel bool
				for _, stmt := range v.Body.List {
					cc, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					if cc.List == nil {
						hasDefault = true
						continue
					}
					for _, e := range cc.List {
						if id, ok := e.(*ast.Ident); ok && id.Name == "paneUnbound" {
							hasSentinel = true
							continue
						}
						lit, ok := e.(*ast.BasicLit)
						if !ok {
							continue
						}
						label, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Errorf("%s(%s): unreadable case label %s", file, fd.Name.Name, lit.Value)
							continue
						}
						labels[p][label] = true
					}
				}
				// A pane that swallows everything else into a text field must answer a
				// switched-off command first, or the keystroke the user turned off is
				// the keystroke the field receives.
				if hasDefault && !hasSentinel {
					t.Errorf("%s(%s): pane %s dispatches on commands and has a default arm but no paneUnbound arm",
						file, fd.Name.Name, p)
				}
			} else if isNameSwitch(v.Tag, "s") {
				bare = append(bare, v)
			}
		case *ast.BinaryExpr:
			if v.Op == token.EQL || v.Op == token.NEQ {
				if chord, ok := chordCompare(v); ok {
					cmp = append(cmp, chord)
				}
			}
		}
		return true
	})
	if len(panes) == 0 {
		return
	}
	for _, p := range panes {
		site := sites[p]
		if site == nil {
			site = &paneSite{labels: map[string]bool{}}
			sites[p] = site
		}
		for label := range labels[p] {
			site.labels[label] = true
		}
	}
	for _, sw := range bare {
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, e := range cc.List {
				if lit, ok := e.(*ast.BasicLit); ok {
					t.Errorf("%s(%s): a migrated handler still switches on the chord %s",
						file, fd.Name.Name, lit.Value)
				}
			}
		}
	}
	for _, chord := range cmp {
		t.Errorf("%s(%s): a migrated handler still compares s to %q", file, fd.Name.Name, chord)
	}
}

// paneCmdSwitch recognises the migrated tag: m.paneCmd("<pane>", s).
func paneCmdSwitch(tag ast.Expr) (paneID, bool) {
	call, ok := tag.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "paneCmd" {
		return "", false
	}
	if !isNameSwitch(call.Args[1], "s") {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return paneID(name), true
}

// isNameSwitch reports a switch whose tag is one bare identifier.
func isNameSwitch(tag ast.Expr, name string) bool {
	id, ok := tag.(*ast.Ident)
	return ok && id.Name == name
}

// chordCompare recognises `s == "<chord>"` in either order: the hand-written form
// the handlers used before the tables existed.
func chordCompare(v *ast.BinaryExpr) (string, bool) {
	var id *ast.Ident
	var lit *ast.BasicLit
	switch x := v.X.(type) {
	case *ast.Ident:
		id = x
		lit, _ = v.Y.(*ast.BasicLit)
	case *ast.BasicLit:
		lit = x
		id, _ = v.Y.(*ast.Ident)
	}
	if id == nil || id.Name != "s" || lit == nil || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || s == "" {
		// The empty string is never a chord: a handler that asks whether the text
		// it was handed is empty is not matching a key by hand.
		return "", false
	}
	return s, true
}

// packageSourceFiles lists the non-test files of this package, in the order a
// reader would expect a report in.
func packageSourceFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}
