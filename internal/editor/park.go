package editor

import (
	"fmt"
)

// One flow for every parked decision.
//
// The chat tool loop already queues parks (chatPark is the head, chatParks
// the waiting ones), but each kind presented itself its own way: RUN wrote
// its own line, the mode switch printed an i18n sentence, the sub-agent wait
// reused another, and none of them said how many decisions were still
// coming. This file is the one presenter: a "[k/N]" badge over the shared
// queue, a per-kind label with its answer hint, and Tab to peek down the
// stack without moving it — answering always stays on the head, so the peek
// can show a file approval but never let you sign someone else's.

// parkPeek indexes the shared stack: 0 is the active park, 1.. are the ones
// waiting in chatParks. It is view state only — it never reorders a queue.

// parkTotal counts every decision the loop waits on: the head, the queue,
// and the diff review of pending changes as one more entry at the end.
func (m *Model) parkTotal() int {
	n := len(m.chatParks)
	if m.chatPark != nil {
		n++
	}
	if len(m.chatPendingChanges) > 0 {
		n++
	}
	return n
}

// parkLabel is the per-kind one-line description with its answer hint.
func (m *Model) parkLabel(p Park) string {
	switch p.Kind {
	case ParkRunConfirm:
		return "RUN: " + p.Text + "  " + m.t("chat.park_yes_no")
	case ParkMCPConfirm:
		return "MCP " + p.Text + "  " + m.t("chat.park_yes_no")
	case ParkSwitchMode:
		return m.t("chat.switch_status")
	case ParkSubAgent:
		return m.t("chat.subagent_status")
	case ParkAskUser:
		q := ""
		if p.Ask != nil {
			q = p.Ask.Question
		}
		return m.t("chat.ask_status") + " " + q
	}
	return ""
}

// parkStatus paints the status bar for the parked loop. With a single
// decision pending the badge is dropped — "[1/1] " over one question is
// ceremony; it appears the moment the stack is deeper.
func (m *Model) parkStatus() string {
	if m.chatPark == nil {
		return ""
	}
	total := m.parkTotal()
	view := *m.chatPark
	if m.parkPeek > 0 {
		i := m.parkPeek - 1
		if i >= len(m.chatParks) {
			m.parkPeek = 0
		} else {
			view = m.chatParks[i]
		}
	}
	label := m.parkLabel(view.park)
	if m.parkPeek > 0 {
		// A peeked entry cannot be answered: the hint says who can.
		label += "  " + m.t("chat.park_peeked")
	}
	if total <= 1 {
		return label
	}
	return fmt.Sprintf("[%d/%d] %s", m.parkPeek+1, total, label)
}

// parkCycle walks the peek index one step down the stack and wraps. The
// y/n park panes answer the Tab chord with it: peeking never decides, so
// every kind shares this one arm.
func (m *Model) parkCycle() {
	n := len(m.chatParks) + 1
	if n > 1 {
		m.parkPeek = (m.parkPeek + 1) % n
	} else {
		m.parkPeek = 0
	}
	m.msg = m.parkStatus()
}
