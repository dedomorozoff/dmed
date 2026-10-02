package editor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"dmed/internal/ai"
)

// segText joins the visible text of rendered segments so tests can assert on
// content without ANSI escapes.
func segText(segs []mdSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

// sameStyle compares two styles by rendering a probe: lipgloss.Style is not
// comparable directly, but identical styles render identically.
func sameStyle(a, b lipgloss.Style) bool {
	return a.Render("\x1b") == b.Render("\x1b")
}

func TestParseInlineMD(t *testing.T) {
	segs := parseInlineMD("plain **bold** and `code` and [link](http://x) end", chatAITextStyle)
	texts := make([]string, 0, len(segs))
	for _, s := range segs {
		texts = append(texts, s.text)
	}
	got := strings.Join(texts, "|")
	want := "plain |bold| and |code| and |link| end"
	if got != want {
		t.Fatalf("segments = %q, want %q", got, want)
	}
	if !sameStyle(segs[1].style, chatMDBoldStyle) {
		t.Errorf("bold span must use the bold style")
	}
	if !sameStyle(segs[3].style, chatMDCodeStyle) {
		t.Errorf("code span must use the code style")
	}
	if !sameStyle(segs[5].style, chatMDLinkStyle) {
		t.Errorf("link span must use the link style")
	}
}

func TestParseInlineMDUnmatchedMarkersStayLiteral(t *testing.T) {
	for _, in := range []string{"a ** b", "a * b", "back`tick", "snake_case_name"} {
		segs := parseInlineMD(in, chatAITextStyle)
		if got := segText(segs); got != in {
			t.Errorf("parseInlineMD(%q) text = %q, want unchanged", in, got)
		}
	}
}

func TestChatMarkdownRows(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatMsgs = []ai.Message{{Role: "assistant", Content: strings.Join([]string{
		"# Title\n",
		"- first **bold** item\n",
		"- second\n",
		"para with `code`.\n",
		"> quoted\n",
		"```go\n",
		"x := 1\n",
		"```\n",
	}, "")}}
	m.rebuildChatRows()

	kinds := make([]string, 0, len(m.chatRows))
	for _, r := range m.chatRows {
		kinds = append(kinds, r.kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"ai-head", "ai-code", "ai-quote"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q row, kinds = %v", want, kinds)
		}
	}

	var boldSeen, codeRowSeen, bulletSeen bool
	for _, r := range m.chatRows {
		if r.kind == "ai-code" && strings.Contains(r.text, "x := 1") {
			codeRowSeen = true
		}
		for _, s := range r.rich {
			if s.text == "bold" && sameStyle(s.style, chatMDBoldStyle) {
				boldSeen = true
			}
			if strings.HasPrefix(s.text, m.g.diagInfo) && sameStyle(s.style, chatMDBulletStyle) {
				bulletSeen = true
			}
		}
	}
	if !boldSeen {
		t.Errorf("**bold** was not rendered as a styled span")
	}
	if !bulletSeen {
		t.Errorf("bullets were not rendered with the bullet marker")
	}
	if !codeRowSeen {
		t.Errorf("fenced code line missing from ai-code rows")
	}
	for _, r := range m.chatRows {
		if strings.Contains(r.text, "**") || strings.Contains(r.text, "`") {
			t.Errorf("raw markup leaked into row: %q", r.text)
		}
	}
}

func TestWrapSegsBreaksAndKeepsStyles(t *testing.T) {
	segs := []mdSeg{
		{text: "alpha ", style: chatAITextStyle},
		{text: "bravo ", style: chatMDBoldStyle},
		{text: "charlie", style: chatAITextStyle},
	}
	lines := wrapSegs(segs, 11)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if got := segText(lines[0]); got != "alpha bravo" {
		t.Errorf("line 0 = %q", got)
	}
	if got := segText(lines[1]); got != "charlie" {
		t.Errorf("line 1 = %q", got)
	}
	for _, s := range lines[0] {
		if s.text == "bravo" && !sameStyle(s.style, chatMDBoldStyle) {
			t.Errorf("style lost across wrap: %+v", s)
		}
	}

	long := []mdSeg{{text: strings.Repeat("x", 15), style: chatAITextStyle}}
	lines = wrapSegs(long, 10)
	if len(lines) != 2 || segText(lines[0]) != strings.Repeat("x", 10) || segText(lines[1]) != strings.Repeat("x", 5) {
		t.Fatalf("overlong word must hard-break, got %v", lines)
	}
}

func TestChatPanelBodyLeftPad(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatMsgs = []ai.Message{{Role: "assistant", Content: "hello world\n"}}
	m.rebuildChatRows()

	w := m.chatPanelWidth()
	panel := m.chatPanel(20)
	// Rows after the header and before the hint bar are body cells: each must
	// carry the left pad and still be exactly panel width wide.
	bodyH := 20 - 2 - m.chatInputHeight()
	padded := 0
	for _, row := range panel[1 : 1+bodyH] {
		if strings.TrimSpace(row) == "" {
			continue
		}
		if got := lipgloss.Width(row); got != w {
			t.Fatalf("body row width = %d, want %d: %q", got, w, row)
		}
		if !strings.HasPrefix(row, strings.Repeat(" ", chatBodyPad)) {
			t.Fatalf("body row missing left pad: %q", row)
		}
		padded++
	}
	if padded == 0 {
		t.Fatalf("no padded body rows found in panel output")
	}
}
