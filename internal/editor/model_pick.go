package editor

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
)

// Quick model switching from the palette: "AI: Switch Model…" asks the
// provider for its model list in the background (the same probe the wizard's
// Test row runs), answers with a bottom list in the shape of the outline
// panel, and the pick lands in the live chat *and* the config — the model
// line is what the wizard owns, and one switch must not leave the two at
// odds. A provider that reports nothing gets the status line, not an empty
// box to click through.

// aiModelsMsg carries the provider's model list, stamped with the request
// generation so a slow reply to an abandoned pick cannot open a panel.
type aiModelsMsg struct {
	gen    uint64
	models []string
	err    string
}

const modelsFetchTimeout = 5 * time.Second

func (m *Model) openModelPick() tea.Cmd {
	gen := bumpGen(&m.modelPickGen)
	prov := m.aiProvider(m.chatModel)
	if prov == nil {
		m.msg = m.t("msg.models_none")
		return nil
	}
	return func() tea.Msg {
		var msg aiModelsMsg
		debug.CapturePanicReport(func() {
			ctx, cancel := context.WithTimeout(context.Background(), modelsFetchTimeout)
			defer cancel()
			models, err := prov.Models(ctx)
			msg.gen = gen
			msg.models = models
			if err != nil {
				msg.err = err.Error()
			}
		})
		return msg
	}
}

func (m *Model) handleModelsMsg(msg aiModelsMsg) {
	if msg.gen != m.modelPickGen {
		return
	}
	if msg.err != "" {
		m.msg = m.t("msg.models_err", msg.err)
		return
	}
	if len(msg.models) == 0 {
		m.msg = m.t("chat.no_models")
		return
	}
	m.modelPick = msg.models
	m.modelPickSel = 0
	m.modelPickOff = 0
	m.modelPickOpen = true
}

// applyModelPick switches the session model and persists it as [ai] model,
// through the wizard's own save so the config, the cached provider and the
// reload land together.
func (m *Model) applyModelPick() {
	i := m.modelPickSel
	if i < 0 || i >= len(m.modelPick) {
		m.modelPickOpen = false
		return
	}
	name := m.modelPick[i]
	m.modelPickOpen = false
	if name == m.chatModel {
		return // the model already on the header needs no save
	}
	m.chatModel = name
	m.cfg.AI.Model = name
	m.saveAISettings()
	m.ai = m.aiProvider(name)
	m.msg = m.t("msg.model_switched", name)
}

func (m *Model) handleModelPick(s string, _ tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("model-pick", s) {
	case "model-pick.close":
		m.modelPickOpen = false
		m.msg = ""
	case "model-pick.up":
		if n := len(m.modelPick); n > 0 {
			m.modelPickSel = (m.modelPickSel - 1 + n) % n
			m.clampModelPickWindow()
		}
	case "model-pick.down":
		if n := len(m.modelPick); n > 0 {
			m.modelPickSel = (m.modelPickSel + 1) % n
			m.clampModelPickWindow()
		}
	case "model-pick.open":
		m.applyModelPick()
	}
	return nil
}

func (m *Model) clampModelPickWindow() {
	const visible = 12
	n := len(m.modelPick)
	if n <= visible {
		m.modelPickOff = 0
		return
	}
	if m.modelPickSel < m.modelPickOff {
		m.modelPickOff = m.modelPickSel
	}
	if m.modelPickSel >= m.modelPickOff+visible {
		m.modelPickOff = m.modelPickSel - visible + 1
	}
	if m.modelPickOff > n-visible {
		m.modelPickOff = n - visible
	}
}

func (m Model) modelPickPanel() []string {
	rows := make([]string, 0, len(m.modelPick)+1)
	title := statusHiStyle.Render(" " + m.t("modelpick.title"))
	if m.chatModel != "" {
		title += statusStyle.Render(m.t("modelpick.current", m.chatModel))
	}
	title += hintStyle.Render("  " + m.t("modelpick.hint"))
	rows = append(rows, title)
	const visible = 12
	n := len(m.modelPick)
	end := m.modelPickOff + visible
	for i := m.modelPickOff; i < end && i < n; i++ {
		label := " "
		if i == m.modelPickSel {
			label += "▸ "
		} else {
			label += "  "
		}
		label += m.modelPick[i]
		if m.modelPick[i] == m.chatModel {
			label += "  " + m.t("modelpick.active")
		}
		if i == m.modelPickSel {
			rows = append(rows, statusHiStyle.Render(padTo(label, m.width)))
		} else {
			rows = append(rows, statusStyle.Render(padTo(label, m.width)))
		}
	}
	return rows
}

// i18n catalog keys this panel and the switch read: modelpick.title,
// modelpick.current, modelpick.hint, modelpick.active, msg.model_switched,
// msg.models_err, msg.models_none (chat.no_models is reused).
