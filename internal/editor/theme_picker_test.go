package editor

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
)

// What lives at this layer: what the rows of the theme panel say, what the screen
// paints from it, and what the file ends up holding. The grammar of a rule is
// internal/syntax's business and the INI round trip is internal/config's; here the
// question is whether "staged", "previewed" and "saved" stayed three different
// things — a rule that is on the screen must not be in the file, and a rule in the
// file must be on the screen without a restart.

var (
	themeEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	themeEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	themeDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	themeUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	themeLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
	themeRight = tea.KeyPressMsg{Code: tea.KeyRight}
)

// themeModel opens the panel on a project whose config names a style and no rules,
// so every expectation below is about what the panel added.
func themeModel(t *testing.T) (Model, string) {
	t.Helper()
	isolateHomeConfig(t)
	dir := t.TempDir()
	writeTemp(t, dir, ".dmed.conf", "[editor]\nsyntax_theme = monokai\n")
	m := New(dir)
	m.width, m.height = 100, 32
	m.startThemeCfg()
	return m, dir
}

// hexColour is how a painted colour gets compared: a style hands back an
// image/color, not the hex the rule was written in.
func hexColour(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// TestThemePanelTypesStagesAndWrites drives the panel the way a user does — down to
// a row, Enter, type, Enter, down, Enter — and pins the two halves of the promise:
// a keystroke previews, Ctrl+S writes.
func TestThemePanelTypesStagesAndWrites(t *testing.T) {
	m, dir := themeModel(t)

	m = press(m, themeDown) // Theme -> Token
	m = press(m, themeEnter)
	m = typeStr(m, "Keyword")
	m = press(m, themeEnter)
	m = press(m, themeDown) // Token -> Style
	m = press(m, themeEnter)
	m = typeStr(m, "#ff0000")
	m = press(m, themeEnter)
	if m.themeTok != "Keyword" || m.themeSpec != "#ff0000" {
		t.Fatalf("rows = %q/%q, want Keyword/#ff0000", m.themeTok, m.themeSpec)
	}
	m = press(m, themeDown) // Style -> Set rule
	m = press(m, themeEnter)

	if got := m.themeRules["keyword"]; got != "#ff0000" {
		t.Fatalf("staged rules = %v, want keyword = #ff0000", m.themeRules)
	}
	// The name is folded on the way in, because that is the spelling the file and
	// the config reader use; the value is kept exactly as typed.
	if _, ok := m.themeRules["Keyword"]; ok {
		t.Errorf("the displayed spelling was kept beside the folded one: %v", m.themeRules)
	}
	// Staging is not writing: the preview changed, the file did not, and neither did
	// the highlighter the text pane paints with.
	if s := readBack(t, filepath.Join(dir, ".dmed.conf")); strings.Contains(s, "[theme]") {
		t.Fatalf("a rule reached the file before Ctrl+S:\n%s", s)
	}
	if got := hexColour(m.syn.StyleFor(chroma.Keyword).GetForeground()); got == "#ff0000" {
		t.Fatal("a staged rule restyled the editor before it was saved")
	}

	m = press(m, saveChord)
	s := readBack(t, filepath.Join(dir, ".dmed.conf"))
	if !strings.Contains(s, "[theme]") || !strings.Contains(s, "keyword = #ff0000") {
		t.Fatalf("Ctrl+S did not write the rule:\n%s", s)
	}
	if got := hexColour(m.syn.StyleFor(chroma.Keyword).GetForeground()); got != "#ff0000" {
		t.Fatalf("after the write Keyword paints %s, want the saved #ff0000", got)
	}
	if !strings.Contains(m.msg, "theme saved") {
		t.Errorf("msg = %q, want the saved count", m.msg)
	}
}

// TestThemePanelRefusesAnUnreadableRule: a refusal has to be said, not swallowed,
// or the user is left choosing a colour for a token that will never use it.
func TestThemePanelRefusesAnUnreadableRule(t *testing.T) {
	m, dir := themeModel(t)

	m.themeTok, m.themeSpec = "keywordz", "#ff0000"
	m.stageThemeRule()
	if _, ok := m.themeRules["keywordz"]; ok {
		t.Fatal("an unknown token was staged")
	}
	if !strings.Contains(m.msg, "dump-tokens") {
		t.Errorf("msg = %q, want the refusal to say where the names live", m.msg)
	}

	// A wrapper no terminal paints is refused the same way, with the reason.
	m.themeTok = "background"
	m.stageThemeRule()
	if _, ok := m.themeRules["background"]; ok {
		t.Fatal("a wrapper token was staged")
	}
	if !strings.Contains(m.msg, "wrapper") {
		t.Errorf("msg = %q, want the refusal to name the wrapper", m.msg)
	}

	// A malformed style entry is refused too — the line is not kept as a rule that
	// paints nothing.
	m.themeTok, m.themeSpec = "keyword", "wobble"
	m.stageThemeRule()
	if _, ok := m.themeRules["keyword"]; ok {
		t.Fatal("an unreadable style was staged")
	}

	// Nothing typed is not an empty rule, it is a mistake.
	m.themeTok = ""
	m.stageThemeRule()
	if !strings.Contains(m.msg, "token name") {
		t.Errorf("msg = %q, want the panel to ask for a token first", m.msg)
	}

	if s := readBack(t, filepath.Join(dir, ".dmed.conf")); strings.Contains(s, "[theme]") {
		t.Fatalf("a refused rule was written anyway:\n%s", s)
	}
}

// TestThemePanelEscDiscards is the reason the rules are staged rather than written
// per keystroke: leaving the panel must be able to mean "nothing happened".
func TestThemePanelEscDiscards(t *testing.T) {
	m, dir := themeModel(t)

	m.themeTok, m.themeSpec = "keyword", "#ff0000"
	m.stageThemeRule()
	m = press(m, themeEsc)

	if m.themeCfgOpen {
		t.Fatal("Esc must close the panel")
	}
	if s := readBack(t, filepath.Join(dir, ".dmed.conf")); strings.Contains(s, "[theme]") {
		t.Fatalf("Esc left a staged rule in the file:\n%s", s)
	}
	// Reopening reads the section back, so the discarded rule really is gone rather
	// than held in a field nobody sees.
	m.startThemeCfg()
	if len(m.themeRules) != 0 {
		t.Fatalf("reopening found %v, want the file's empty section", m.themeRules)
	}
}

// TestThemePanelWritesTheBaseStyle: the ←/→ row and the rules are one save, and the
// editor's colours follow only after it.
func TestThemePanelWritesTheBaseStyle(t *testing.T) {
	m, dir := themeModel(t)

	before := m.themeName
	m = press(m, themeRight)
	if m.themeName == before {
		t.Fatalf("→ stayed on %q", m.themeName)
	}
	if m.syn.StyleName() != before {
		t.Fatalf("cycling restyled the editor before Ctrl+S: %q", m.syn.StyleName())
	}

	m = press(m, saveChord)
	s := readBack(t, filepath.Join(dir, ".dmed.conf"))
	if !strings.Contains(s, "syntax_theme = "+m.themeName) {
		t.Fatalf("Ctrl+S did not write the style:\n%s", s)
	}
	if got := m.syn.StyleName(); got != m.themeName {
		t.Fatalf("after the write the editor paints %q, want %q", got, m.themeName)
	}
	// ← is the way back, and a round trip lands on the style the file names.
	m = press(m, themeLeft)
	if m.themeName != before {
		t.Fatalf("←/→ did not round trip: %q then %q", before, m.themeName)
	}
	m = press(m, themeEsc)
	if s := readBack(t, filepath.Join(dir, ".dmed.conf")); !strings.Contains(s, "syntax_theme = "+m.cfg.Editor.SyntaxTheme) {
		t.Fatalf("the saved style and the config disagree:\n%s", s)
	}
}

// TestThemePanelPreviewNeedsNoStyleIsThe point of the panel: the colours are judged
// before anything is written, so the sample is painted from the rows on the screen.
func TestThemePanelPreviewIsPaintedFromTheRows(t *testing.T) {
	m, _ := themeModel(t)
	plain := strings.Join(m.themePreview, "\n")
	if !strings.Contains(plain, "package") {
		t.Fatal("the preview must show the sample, not an empty panel")
	}

	m.themeTok, m.themeSpec = "keyword", "#ff0000"
	m.stageThemeRule()
	staged := strings.Join(m.themePreview, "\n")
	if staged == plain {
		t.Fatal("a staged rule did not reach the preview")
	}
	// Every painted line is a styled run: a preview that rendered plain text would
	// pass the check above while telling the user nothing about the theme.
	if !strings.Contains(staged, "\x1b[") {
		t.Fatalf("the preview is not painted: %q", staged)
	}
}

// TestThemePanelTypingPreviewsOnlyWhatIsReadable: a keystroke that is not a colour
// yet must not blank the preview the user is judging. (A short hex *is* a colour —
// chroma pads it — so the unreadable case is the hash on its own.)
func TestThemePanelTypingPreviewsOnlyWhatIsReadable(t *testing.T) {
	m, _ := themeModel(t)
	m.themeTok = "keyword"
	m.themeCfgField = themeFieldStyle
	m.themeCfgEdit = true

	m.themeCfgIn = []rune("#ff0000")
	m.previewThemeTyping()
	good := strings.Join(m.themePreview, "\n")

	m.themeCfgIn = []rune("#")
	m.previewThemeTyping()
	if got := strings.Join(m.themePreview, "\n"); got != good {
		t.Fatal("an unreadable keystroke clobbered the last readable preview")
	}
	if len(m.themeRules) != 0 {
		t.Fatalf("typing staged a rule: %v", m.themeRules)
	}
}

// TestThemePanelRowsWalkCycles: the row cursor wraps in both directions and the
// text rows are entered while the actions run, which is all the ←/→ and Enter keys
// promise.
func TestThemePanelRowsWalkAndRun(t *testing.T) {
	m, _ := themeModel(t)
	if m.themeCfgField != themeFieldTheme {
		t.Fatalf("the panel opens on row %d, want the style", m.themeCfgField)
	}
	for i := 0; i < len(themeFields); i++ {
		m = press(m, themeDown)
	}
	if m.themeCfgField != themeFieldTheme {
		t.Fatalf("↓ past the last row landed on %d, want a wrap to the first", m.themeCfgField)
	}
	m = press(m, themeUp)
	if got, want := m.themeCfgField, themeFieldRevert; got != want {
		t.Fatalf("↑ past the first row landed on %d, want %d", got, want)
	}

	// Enter on an action row runs it rather than starting a text edit.
	m.themeCfgField = themeFieldRevert
	m = press(m, themeEnter)
	if m.themeCfgEdit {
		t.Fatal("Enter on an action row must run it, not open a text field")
	}
	if !strings.Contains(m.msg, "dropped") {
		t.Errorf("msg = %q, want the action to report itself", m.msg)
	}

	// Esc leaves the row being typed first, then the panel: one keystroke that did
	// both would throw away the whole session by accident.
	m.themeCfgField = themeFieldToken
	m = press(m, themeEnter)
	if !m.themeCfgEdit {
		t.Fatal("Enter on a text row must start typing")
	}
	m = press(m, themeEsc)
	if m.themeCfgEdit {
		t.Fatal("Esc must leave the row being typed")
	}
	if !m.themeCfgOpen {
		t.Fatal("the first Esc closes the row, not the panel")
	}
	m = press(m, themeEsc)
	if m.themeCfgOpen {
		t.Fatal("the second Esc closes the panel")
	}
}

// TestThemeHotReloadRepaintsWithoutRestart is the milestone's user-visible claim:
// saving the config while the editor runs changes the colours, and nothing about
// the buffers needs recomputing to do it.
func TestThemeHotReloadRepaintsWithoutRestart(t *testing.T) {
	isolateHomeConfig(t)
	dir := t.TempDir()
	path := writeTemp(t, dir, ".dmed.conf", "[editor]\nsyntax_theme = monokai\n")
	m := New(dir)
	m.width, m.height = 100, 32
	base := hexColour(m.syn.StyleFor(chroma.Keyword).GetForeground())
	comment := hexColour(m.syn.StyleFor(chroma.Comment).GetForeground())
	if base == "#ff0000" {
		t.Skip("the bundled monokai already paints Keyword red; the rule below needs a colour to change")
	}

	writeTemp(t, dir, ".dmed.conf", "[editor]\nsyntax_theme = monokai\n[theme]\nkeyword = #ff0000\n")
	next, _ := m.Update(FileChangedMsg{Path: path})
	m = next.(Model)

	if got := hexColour(m.syn.StyleFor(chroma.Keyword).GetForeground()); got != "#ff0000" {
		t.Fatalf("after the reload Keyword = %s, want the written #ff0000", got)
	}
	// One rule recolours one token: the rest of the palette is the style's answer,
	// still the same after a reload that touched nothing else.
	if got := hexColour(m.syn.StyleFor(chroma.Comment).GetForeground()); got != comment {
		t.Fatalf("Comment changed from %s to %s by a Keyword rule", comment, got)
	}
	// The highlighter holds the rules, so a line painted after the reload reads them
	// without a second pass over the buffer.
	if len(m.themeRefused) != 0 {
		t.Fatalf("a readable section was refused: %v", m.themeRefused)
	}
	if m.syn.StyleName() != "monokai" {
		t.Fatalf("the style name moved to %q", m.syn.StyleName())
	}
	// The switch-off is the other half of the same file: the line stays, the colour
	// goes back to the style's own.
	writeTemp(t, dir, ".dmed.conf", "[editor]\nsyntax_theme = monokai\n[theme]\nkeyword =\n")
	next, _ = m.Update(FileChangedMsg{Path: path})
	m = next.(Model)
	if got := hexColour(m.syn.StyleFor(chroma.Keyword).GetForeground()); got != base {
		t.Fatalf("switching the rule off left %s, want the style's own %s", got, base)
	}
}

// TestThemeRefusalsReachTheStatusLine: a [theme] line that names no token is a
// colour the user believes they set, so the reload has to say how many were dropped
// and which one first.
func TestThemeRefusalsReachTheStatusLine(t *testing.T) {
	isolateHomeConfig(t)
	dir := t.TempDir()
	path := writeTemp(t, dir, ".dmed.conf", "[theme]\nkeywordz = #ff0000\n")
	m := New(dir)
	m.width, m.height = 100, 32
	if len(m.themeRefused) != 1 {
		t.Fatalf("refusals at startup = %v, want one", m.themeRefused)
	}
	if !strings.Contains(m.msg, "[theme]") || !strings.Contains(m.msg, "keywordz") {
		t.Fatalf("startup msg = %q, want the count and the line", m.msg)
	}

	// The reload route reads the same file and says the same thing.
	next, _ := m.Update(FileChangedMsg{Path: path})
	m = next.(Model)
	if !strings.Contains(m.msg, "keywordz") {
		t.Errorf("reload msg = %q, want the refused line named", m.msg)
	}
	// While the panel is open it shows them too, since that is where a user looks
	// for the reason a colour did not take.
	m.startThemeCfg()
	if len(m.themeRefused) != 1 {
		t.Fatalf("the panel dropped the refusals: %v", m.themeRefused)
	}
	// The refused line stays in the section: Save writes what the file holds rather
	// than quietly deleting a rule the highlighter could not read.
	if got := m.themeRuleCount(); got != 1 {
		t.Errorf("a refused rule counts as %d, want it kept for the write", got)
	}
	// And the panel says so, because this is the screen a user opens to find out why
	// a colour did not take.
	if !strings.Contains(strings.Join(m.themeCfgPanel(32), "\n"), "keywordz") {
		t.Error("the panel must show the refusal, not only the bar")
	}
}

// TestThemePanelFitsTheScreen: the preview is what is left over, never what pushes
// the status bar off.
func TestThemePanelFitsTheScreen(t *testing.T) {
	m, _ := themeModel(t)
	for _, h := range []int{12, 24, 32} {
		rows := m.themeCfgPanel(h)
		if len(rows) > h {
			t.Fatalf("a %d-row screen got %d rows", h, len(rows))
		}
	}
	rows := m.themeCfgPanel(32)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"Theme", "Token", "Style", "Set rule", "Save", "Revert", "preview"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the panel is missing the %q row:\n%s", want, joined)
		}
	}
}
