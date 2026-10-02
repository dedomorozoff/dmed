package editor

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Chat markdown styles: the transcript renders assistant output as styled
// text instead of raw markup. Colors stay in the panel palette family
// (blue headings, purple code and bullets) so the rail keeps one look.
var (
	chatMDHeadStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	chatMDCodeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("176"))
	chatMDQuoteStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	chatMDBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("176")).Bold(true)
	chatMDBoldStyle   = lipgloss.NewStyle().Bold(true)
	chatMDItalicStyle = lipgloss.NewStyle().Italic(true)
	chatMDLinkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Underline(true)
)

// chatBodyPad is the small left margin inside the chat panel between the
// panel edge and every transcript row.
const chatBodyPad = 1

// mdSeg is one styled span of a chat row. Rows carry segments instead of a
// single pre-rendered string so the panel can pad and wrap them without
// counting ANSI escape bytes as visible width.
type mdSeg struct {
	text  string
	style lipgloss.Style
}

// addAIRows renders one assistant message as markdown-aware chat rows.
func (m Model) addAIRows(rows *[]chatRow, content string) {
	add := func(kind, text string) { *rows = append(*rows, chatRow{kind: kind, text: text}) }
	addRich := func(kind string, segs []mdSeg) { *rows = append(*rows, chatRow{kind: kind, rich: segs}) }
	add("label-ai", " ai")
	m.mdRows(content, m.chatInnerWidth(), addRich, add)
	add("hint", "")
}

// mdRows turns markdown into chat rows: fenced code blocks, headings, bullets,
// quotes and inline emphasis become styled rows instead of raw markup. Plain
// paragraphs fall through with only inline styling applied.
func (m Model) mdRows(content string, w int, addRich func(string, []mdSeg), add func(string, string)) {
	if w < 1 {
		w = 1
	}
	inCode := false
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		switch {
		case inCode:
			for _, wl := range wrapRunes(line, max(1, w-3)) {
				add("ai-code", " "+m.g.vline+" "+wl)
			}
		case trimmed == "":
			add("hint", "")
		case isMDRule(trimmed):
			add("hint", " "+strings.Repeat(m.g.hline, max(1, w-2)))
		case isMDHeading(trimmed):
			text := strings.TrimLeft(trimmed, "#")
			text = strings.TrimPrefix(text, " ")
			for _, wl := range wrapRunes(text, max(1, w-1)) {
				add("ai-head", " "+wl)
			}
		case isMDQuote(trimmed):
			text := strings.TrimPrefix(trimmed, ">")
			text = strings.TrimPrefix(text, " ")
			for _, segs := range wrapSegs(parseInlineMD(text, chatMDQuoteStyle), w) {
				addRich("ai-quote", segs)
			}
		case mdBullet(trimmed) != "":
			segs := []mdSeg{{text: " " + m.g.diagInfo + " ", style: chatMDBulletStyle}}
			segs = append(segs, parseInlineMD(mdBullet(trimmed), chatAITextStyle)...)
			for _, line := range wrapSegs(segs, w) {
				addRich("ai", line)
			}
		default:
			for _, segs := range wrapSegs(parseInlineMD(line, chatAITextStyle), w) {
				addRich("ai", segs)
			}
		}
	}
}

// mdBullet recognizes "- ", "* " and "+ " list markers and returns the rest
// of the line; empty string means not a bullet.
func mdBullet(s string) string {
	if len(s) >= 2 && (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' ' {
		return s[2:]
	}
	return ""
}

// isMDHeading matches 1-6 leading '#' followed by a space.
func isMDHeading(s string) bool {
	i := 0
	for i < len(s) && s[i] == '#' {
		i++
	}
	return i >= 1 && i <= 6 && i < len(s) && s[i] == ' '
}

// isMDRule matches a horizontal rule: three or more of -, * or _.
func isMDRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// isMDQuote matches a leading ">" (with or without a space after it).
func isMDQuote(s string) bool {
	return strings.HasPrefix(s, ">")
}

// parseInlineMD splits one line into styled spans: `code`, **bold**,
// *italic*, _italic_ and [text](url). Unmatched markers stay literal text.
func parseInlineMD(text string, base lipgloss.Style) []mdSeg {
	var segs []mdSeg
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			segs = append(segs, mdSeg{text: buf.String(), style: base})
			buf.Reset()
		}
	}
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '`':
			end := runeIndexFrom(rs, i+1, '`')
			if end > i+1 {
				flush()
				segs = append(segs, mdSeg{text: string(rs[i+1 : end]), style: chatMDCodeStyle})
				i = end
				continue
			}
		case c == '*' && i+1 < len(rs) && rs[i+1] == '*':
			end := seqIndexFrom(rs, i+2, "**")
			if end > 0 {
				flush()
				segs = append(segs, parseInlineMD(string(rs[i+2:end]), chatMDBoldStyle)...)
				i = end + 1
				continue
			}
		case c == '*':
			end := runeIndexFrom(rs, i+1, '*')
			if end > i+1 {
				flush()
				segs = append(segs, parseInlineMD(string(rs[i+1:end]), chatMDItalicStyle)...)
				i = end
				continue
			}
		case c == '_' && (i == 0 || !isWordRune(rs[i-1])):
			end := runeIndexFrom(rs, i+1, '_')
			if end > i+1 && (end+1 >= len(rs) || !isWordRune(rs[end+1])) {
				flush()
				segs = append(segs, parseInlineMD(string(rs[i+1:end]), chatMDItalicStyle)...)
				i = end
				continue
			}
		case c == '[':
			bracketEnd := runeIndexFrom(rs, i+1, ']')
			if bracketEnd > i+1 && bracketEnd+2 < len(rs) && rs[bracketEnd+1] == '(' {
				urlEnd := runeIndexFrom(rs, bracketEnd+2, ')')
				if urlEnd > bracketEnd+2 {
					flush()
					segs = append(segs, mdSeg{text: string(rs[i+1 : bracketEnd]), style: chatMDLinkStyle})
					i = urlEnd
					continue
				}
			}
		}
		buf.WriteRune(c)
	}
	flush()
	return segs
}

func runeIndexFrom(rs []rune, from int, want rune) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == want {
			return i
		}
	}
	return -1
}

func seqIndexFrom(rs []rune, from int, seq string) int {
	sr := []rune(seq)
	for i := from; i+1 < len(rs); i++ {
		if rs[i] == sr[0] && rs[i+1] == sr[1] {
			return i
		}
	}
	return -1
}

// wrapSegs wraps styled segments to width w, breaking on spaces the same way
// wrapRunes breaks plain text and keeping style runs intact across lines.
func wrapSegs(segs []mdSeg, w int) [][]mdSeg {
	if w < 1 {
		w = 1
	}
	type cell struct {
		r   rune
		seg int
	}
	var cells []cell
	for si, s := range segs {
		for _, r := range s.text {
			cells = append(cells, cell{r, si})
		}
	}
	var lines [][]mdSeg
	lineOf := func(cs []cell) []mdSeg {
		var out []mdSeg
		for i := 0; i < len(cs); {
			j := i
			for j < len(cs) && cs[j].seg == cs[i].seg {
				j++
			}
			var b strings.Builder
			for _, c := range cs[i:j] {
				b.WriteRune(c.r)
			}
			out = append(out, mdSeg{text: b.String(), style: segs[cs[i].seg].style})
			i = j
		}
		return out
	}
	var line []cell
	width := 0
	var word []cell
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		if width+len(word) > w && width > 0 {
			lines = append(lines, lineOf(line))
			line = nil
			width = 0
		}
		for len(word) > w {
			lines = append(lines, lineOf(word[:w]))
			word = word[w:]
		}
		line = append(line, word...)
		width += len(word)
		word = nil
	}
	for _, c := range cells {
		if c.r == ' ' {
			flushWord()
			switch {
			case width == 0:
				// drop spaces at the start of a wrapped line
			case width < w:
				line = append(line, c)
				width++
			default:
				lines = append(lines, lineOf(line))
				line = nil
				width = 0
			}
		} else {
			word = append(word, c)
		}
	}
	flushWord()
	if len(line) > 0 {
		lines = append(lines, lineOf(line))
	}
	return lines
}
