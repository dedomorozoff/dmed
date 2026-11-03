package editor

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2/styles"

	"dmed/internal/config"
	"dmed/internal/syntax"
)

// syntaxFor builds the highlighter a config describes: the base chroma style with
// the [theme] rules laid over it. It also hands back the rules it could not read,
// because a theme that quietly ignored half its lines would be reported as a bug
// in the colours rather than in the spelling.
func syntaxFor(cfg config.Config) (*syntax.Highlighter, []string) {
	ov, refused := syntax.ResolveTheme(cfg.Theme.Tokens)
	return syntax.NewWith(cfg.Editor.SyntaxTheme, ov), refused
}

// themeFields describes the theme panel rows. The base style is cycled with ←/→,
// every bundled palette one press apart with the preview changing under the keys.
// The token and the style are typed, because a token name is not a small list and
// no cycle reaches the one the user came for.
var themeFields = []struct {
	name string
	kind string // "choice" | "text" | "action"
}{
	{name: "Theme", kind: "choice"},
	{name: "Token", kind: "text"},
	{name: "Style", kind: "text"},
	{name: "Set rule", kind: "action"},
	{name: "Save", kind: "action"},
	{name: "Revert", kind: "action"},
}

const (
	themeFieldTheme  = 0
	themeFieldToken  = 1
	themeFieldStyle  = 2
	themeFieldSet    = 3
	themeFieldSave   = 4
	themeFieldRevert = 5
)

// themeSampleName is the file the preview is highlighted as: the extension is what
// picks the lexer, and Go is the language the startup warmup already paid for.
const themeSampleName = "theme_sample.go"

// themeSample is the text the preview paints. It is fixed rather than the active
// buffer on purpose: two themes are only comparable when the same tokens are under
// each of them, and someone choosing a colour for "comment" wants to see a comment,
// not whatever file happens to be open. The lines are chosen to put every family
// the eye checks for on the screen at once.
var themeSample = strings.Join([]string{
	"// a comment, italic if the theme says so",
	"package main",
	"",
	"import \"fmt\"",
	"",
	"type Model struct {",
	"\tcount int64",
	"\tlabel string",
	"}",
	"",
	"func (m Model) Render(wide bool) string {",
	"\tif m.count > 42 && wide {",
	"\t\treturn fmt.Sprintf(\"%s: %d\", m.label, m.count)",
	"\t}",
	"\treturn `raw` /* + 3.14 == done */",
	"}",
}, "\n")

func (m *Model) startThemeCfg() {
	m.themeCfgOpen = true
	m.themeCfgField = 0
	m.themeCfgEdit = false
	m.themeCfgIn = nil
	m.themeName = m.cfg.Editor.SyntaxTheme
	m.themeTok = ""
	m.themeSpec = ""
	m.themeRules = themeRulesOf(m.cfg)
	m.refreshThemePreview()
	m.msg = ""
}

// themeRulesOf copies the section out of the config: the panel stages its own
// edits, so a rule reaches the file only through Save and Esc leaves it untouched.
func themeRulesOf(cfg config.Config) map[string]string {
	rules := make(map[string]string, len(cfg.Theme.Tokens))
	for name, spec := range cfg.Theme.Tokens {
		rules[name] = spec
	}
	return rules
}

// refreshThemePreview paints the sample with what the rows say right now. It is
// synchronous because there is nothing to wait for: the highlighter is built from
// the strings on the screen, not from a server.
func (m *Model) refreshThemePreview() {
	ov, refused := syntax.ResolveTheme(m.themeRules)
	m.themeRefused = refused
	m.themePreview = paintThemeSample(syntax.NewWith(m.themeName, ov))
}

// paintThemeSample tokenizes the fixed sample and renders each line in runs of
// equal token, the same shape the editor paints real lines with: a style call per
// character would make the preview the most expensive row on the screen.
func paintThemeSample(hi *syntax.Highlighter) []string {
	src := strings.Split(themeSample, "\n")
	toks := hi.HighlightBuffer(themeSampleName, themeSample)
	out := make([]string, 0, len(src))
	for i, text := range src {
		line := []rune(text)
		if i < len(toks) && len(toks[i]) == len(line) {
			out = append(out, paintTokenRun(toks[i], line, hi))
			continue
		}
		out = append(out, text)
	}
	return out
}

func paintTokenRun(toks syntax.HighlightedLine, line []rune, hi *syntax.Highlighter) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		j := i + 1
		for j < len(line) && toks[j] == toks[i] {
			j++
		}
		b.WriteString(hi.StyleFor(toks[i]).Render(string(line[i:j])))
		i = j
	}
	return b.String()
}

func (m Model) themeFieldValue(i int) string {
	switch i {
	case themeFieldTheme:
		return m.themeName
	case themeFieldToken:
		return m.themeTok
	case themeFieldStyle:
		return m.themeSpec
	case themeFieldSet:
		return m.t("theme.set_hint")
	case themeFieldSave:
		return strconv.Itoa(m.themeRuleCount()) + " " + m.t("theme.rules")
	case themeFieldRevert:
		return m.t("theme.revert_hint")
	}
	return ""
}

// themeRuleCount is how many rules the panel is holding, spelled out because a
// rule that was staged and never saved is otherwise invisible: the preview changed,
// so the user has every reason to believe the file did too.
func (m Model) themeRuleCount() int {
	n := 0
	for _, spec := range m.themeRules {
		if spec != "" {
			n++
		}
	}
	return n
}

func (m *Model) cycleThemeChoice(d int) {
	names := styles.Names()
	if len(names) == 0 {
		return
	}
	m.themeName = cycleString(names, m.themeName, d)
	m.refreshThemePreview()
}

func (m *Model) commitThemeField() {
	v := strings.TrimSpace(string(m.themeCfgIn))
	switch m.themeCfgField {
	case themeFieldToken:
		m.themeTok = v
	case themeFieldStyle:
		m.themeSpec = v
	}
	m.themeCfgEdit = false
	m.themeCfgIn = nil
}

// stageThemeRule validates the token name and the style entry before anything is
// kept, and reports the refusal instead. The check is the very resolution the
// config goes through later, so a rule that stages is a rule that will paint.
func (m *Model) stageThemeRule() {
	name := strings.ToLower(strings.TrimSpace(m.themeTok))
	spec := strings.TrimSpace(m.themeSpec)
	if name == "" {
		m.msg = m.t("theme.need_token")
		return
	}
	if _, refused := syntax.ResolveTheme(map[string]string{name: spec}); len(refused) > 0 {
		m.msg = refused[0]
		return
	}
	if m.themeRules == nil {
		m.themeRules = map[string]string{}
	}
	m.themeRules[name] = spec
	m.themeCfgEdit = false
	m.themeCfgIn = nil
	m.refreshThemePreview()
	if spec == "" {
		m.msg = m.t("theme.rule_off", name)
		return
	}
	m.msg = m.t("theme.rule_staged", name, spec)
}

// saveThemeCfg writes the base style and the staged rules. Saving does not close
// the panel: the point of writing from a preview is to compare the file's answer
// with the screen, and that comparison needs the screen.
func (m *Model) saveThemeCfg() {
	path := m.aiConfigPath() // shared: project config if present, else global
	if m.themeName != m.cfg.Editor.SyntaxTheme {
		if err := config.WriteSyntaxTheme(path, m.themeName); err != nil {
			m.msg = m.t("theme.write_failed", err.Error())
			return
		}
	}
	if _, err := config.WriteTheme(path, m.themeRules); err != nil {
		m.msg = m.t("theme.write_failed", err.Error())
		return
	}
	m.cfg = config.Load(m.root)
	syn, refused := syntaxFor(m.cfg)
	m.syn = syn
	m.themeRefused = refused
	// Nothing is dropped from the per-tab caches: they hold token *kinds*, and a
	// theme changes only the style a kind resolves to, which the view asks for as
	// it paints. Re-tokenizing every tab to change a colour would be the slow way
	// to do the one thing this panel exists for.
	m.themeRules = themeRulesOf(m.cfg)
	m.msg = m.t("theme.saved", m.themeRuleCount())
}

// revertThemeCfg throws away the staged edits and takes the section back from the
// file. Nothing is written, which is why it is a row rather than another keystroke.
func (m *Model) revertThemeCfg() {
	m.themeCfgEdit = false
	m.themeCfgIn = nil
	m.themeName = m.cfg.Editor.SyntaxTheme
	m.themeTok = ""
	m.themeSpec = ""
	m.themeRules = themeRulesOf(m.cfg)
	m.refreshThemePreview()
	m.msg = m.t("theme.reverted")
}

// handleThemeCfg routes keys while the theme panel is open.
func (m *Model) handleThemeCfg(s string, msg tea.KeyPressMsg) tea.Cmd {
	if m.themeCfgEdit {
		switch m.paneCmd("theme-cfg-field", s) {
		case "theme-cfg-field.close":
			m.themeCfgEdit = false
			m.themeCfgIn = nil
		case "theme-cfg-field.run":
			m.commitThemeField()
		case "theme-cfg-field.erase":
			if n := len(m.themeCfgIn); n > 0 {
				m.themeCfgIn = m.themeCfgIn[:n-1]
			}
		case paneUnbound:
			return nil
		default:
			if len(msg.Text) > 0 {
				m.themeCfgIn = append(m.themeCfgIn, []rune(msg.Text)...)
				if m.themeCfgField == themeFieldStyle {
					// The row being typed is the one the preview reads, so a colour is
					// judged as it is written rather than after a second Enter.
					m.previewThemeTyping()
				}
			}
		}
		return nil
	}

	switch m.paneCmd("theme-cfg", s) {
	case "theme-cfg.close":
		m.themeCfgOpen = false
		m.themeCfgEdit = false
		m.themeCfgIn = nil
	case "theme-cfg.next":
		m.themeCfgField = (m.themeCfgField + 1) % len(themeFields)
	case "theme-cfg.prev":
		m.themeCfgField = (m.themeCfgField - 1 + len(themeFields)) % len(themeFields)
	case "theme-cfg.choice-prev":
		m.cycleThemeChoice(-1)
	case "theme-cfg.choice-next":
		m.cycleThemeChoice(1)
	case "theme-cfg.open":
		switch themeFields[m.themeCfgField].kind {
		case "choice":
			m.cycleThemeChoice(1)
		case "action":
			m.runThemeAction(m.themeCfgField)
		default:
			m.themeCfgEdit = true
			m.themeCfgIn = []rune(m.themeFieldValue(m.themeCfgField))
		}
	case "theme-cfg.save":
		m.saveThemeCfg()
	}
	return nil
}

// previewThemeTyping paints with the row still being typed, without staging it:
// the staged map holds only what passed validation, and a trailing "#" is not a
// colour yet. A half-typed rule therefore previews as soon as it is readable and
// leaves the last readable preview alone while it is not.
func (m *Model) previewThemeTyping() {
	name := strings.ToLower(strings.TrimSpace(m.themeTok))
	if name == "" || m.themeName == "" {
		return
	}
	rules := make(map[string]string, len(m.themeRules)+1)
	for k, spec := range m.themeRules {
		rules[k] = spec
	}
	rules[name] = strings.TrimSpace(string(m.themeCfgIn))
	ov, refused := syntax.ResolveTheme(rules)
	if len(refused) > 0 {
		return
	}
	m.themePreview = paintThemeSample(syntax.NewWith(m.themeName, ov))
}

func (m *Model) runThemeAction(i int) {
	switch i {
	case themeFieldSet:
		m.stageThemeRule()
	case themeFieldSave:
		m.saveThemeCfg()
	case themeFieldRevert:
		m.revertThemeCfg()
	}
}

func (m Model) themeCfgPanel(h int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, statusHiStyle.Render(m.t("theme.settings"))+" "+hintStyle.Render(m.t("theme.settings_hint")))
	for i, f := range themeFields {
		marker := " "
		if i == m.themeCfgField {
			marker = ">"
		}
		hint := ""
		switch f.kind {
		case "choice":
			hint = "  " + m.t("theme.cycle")
		case "action":
			hint = "  " + m.t("theme.enter_run")
		}
		if m.themeCfgEdit && i == m.themeCfgField {
			// The row being typed shows the text under the cursor rather than the
			// committed value, so the difference between the two is never a mystery.
			rows = append(rows, " "+statusHiStyle.Render(marker)+" "+padTo(f.name, 12)+" "+
				statusStyle.Render(string(m.themeCfgIn))+cursorStyle.Render(" "))
			continue
		}
		rows = append(rows, " "+statusHiStyle.Render(marker)+" "+padTo(f.name, 12)+" "+
			statusStyle.Render(m.themeFieldValue(i))+hintStyle.Render(hint))
	}
	// The lines the file holds that read as nothing are shown here as well as in the
	// bar: this is the screen a user opens to find out why a colour did not take.
	if len(m.themeRefused) > 0 {
		rows = append(rows, " "+diagErrStyle.Render(m.themeRefusedMsg()))
	}
	rows = append(rows, "", hintStyle.Render(" "+m.t("theme.preview")))
	// The preview takes the rows that are left: a panel that pushes the status bar
	// off the screen is not a panel, it is a crash with colours.
	left := h - len(rows) - 1
	for i, line := range m.themePreview {
		if i >= left {
			break
		}
		rows = append(rows, "   "+line)
	}
	return rows
}
