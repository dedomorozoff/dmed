package editor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/buffer"
	"dmed/internal/events"
	"dmed/internal/vcs"
)

// Background agent tasks panel (Alt+L). Lists tasks created from the palette
// with their status/progress; lets the user navigate, cancel, and review each
// task's changes before applying them atomically through the Applier.
// Follows the project rule: agents never write to buffers directly.

// AgentRefreshMsg signals that the agent queue changed and the panel should
// repaint.
type AgentRefreshMsg struct{}

func waitForAgentRefresh(ch <-chan struct{}) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		<-ch
		return AgentRefreshMsg{}
	}
}

// ensureAgent lazily wires the M4 agent core into the model and starts the
// background worker. It also starts the repaint chain. Safe to call multiple
// times.
func (m *Model) ensureAgent() tea.Cmd {
	if m.agentQueue != nil {
		return waitForAgentRefresh(m.agentCh)
	}

	if m.agentCtx == nil {
		m.agentCtx, m.agentCancel = context.WithCancel(context.Background())
	}

	prov := ai.NewProvider(ai.Config{
		Type:   ai.ProviderType(m.cfg.AI.Provider),
		URL:    m.cfg.AI.OllamaURL,
		Model:  m.cfg.AI.Model,
		APIKey: m.cfg.AI.APIKey,
	})

	m.agentQueue = agent.NewQueue(m.bus)
	m.agentRunner = agent.NewRunner(prov, m.agentQueue)
	if sp := strings.TrimSpace(m.cfg.Agent.SystemPrompt); sp != "" {
		m.agentRunner.Prompt = func(prompt string, files []agent.TargetFile) ([]ai.Message, error) {
			var b strings.Builder
			b.WriteString(sp)
			b.WriteString("\n\nProduce the FULL new content of every file you change, each in a block:\n")
			b.WriteString("=== FILE: <relative-path> ===\n<complete new file content>\n\n")
			for _, f := range files {
				fmt.Fprintf(&b, "\n--- FILE %s ---\n%s\n", f.Path, f.Content)
			}
			return []ai.Message{
				{Role: "system", Content: b.String()},
				{Role: "user", Content: "Task: " + prompt},
			}, nil
		}
	}
	m.agentApplier = agent.NewApplier()
	m.agentCommit = &agent.Committer{Repo: m.repo, Bus: m.bus}
	m.agentCh = make(chan struct{}, 1)

	m.bus.Subscribe(events.EventAgentUpdated, func(e events.Event) {
		select {
		case m.agentCh <- struct{}{}:
		default:
		}
	})

	go m.agentWorker()

	return waitForAgentRefresh(m.agentCh)
}

// agentWorker drains the queue in a loop, running each task to completion.
func (m *Model) agentWorker() {
	for {
		if m.agentCtx != nil && m.agentCtx.Err() != nil {
			return
		}
		task := m.agentQueue.Next()
		if task == nil {
			time.Sleep(150 * time.Millisecond)
			continue
		}
		m.agentRunner.Run(m.agentCtx, task, m.agentTargets())
	}
}

// agentTargets gathers plausible source files under the project root as
// context for the agent, capped by size and count.
func (m *Model) agentTargets() []agent.TargetFile {
	base := m.baseDir()
	files := collectFiles(base, m.cfg.Editor.SkippedDirs)

	var targets []agent.TargetFile
	var budget int
	maxBudget := m.cfg.Agent.ContextMax
	if maxBudget <= 0 {
		maxBudget = 256 * 1024
	}
	for _, rel := range files {
		if len(targets) >= 40 {
			break
		}
		full := filepath.Join(base, filepath.FromSlash(rel))
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() || fi.Size() > int64(maxBudget/2) {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		budget += len(data)
		if budget > maxBudget {
			break
		}
		targets = append(targets, agent.TargetFile{Path: rel, Content: string(data)})
	}
	return targets
}

// openAgentPanel opens the agent task panel and focuses it.
func (m *Model) openAgentPanel() tea.Cmd {
	cmd := m.ensureAgent()
	m.agentOpen = true
	m.agentFocus = true
	m.msg = ""
	return cmd
}

// toggleAgentPanel shows the panel if it is closed, and if it is already open
// moves the keyboard focus between the panel and the editor without collapsing
// it (Alt+L).
func (m *Model) toggleAgentPanel() tea.Cmd {
	cmd := m.ensureAgent()
	if m.agentOpen {
		m.agentFocus = !m.agentFocus
		if m.agentFocus {
			m.msg = ""
		}
		return cmd
	}
	m.agentOpen = true
	m.agentFocus = true
	m.msg = ""
	return cmd
}

func (m *Model) startAgentTaskPrompt() tea.Cmd {
	m.ensureAgent()
	m.agentOpen = true
	m.agentFocus = true
	m.agentPrompt = true
	m.agentPromptIn = nil
	m.msg = ""
	return nil
}

// handleAgentPrompt handles text entry for a new task prompt.
func (m *Model) handleAgentPrompt(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.agentPrompt = false
		m.agentPromptIn = nil
	case "enter":
		prompt := strings.TrimSpace(string(m.agentPromptIn))
		if prompt == "" {
			m.agentPrompt = false
			return nil
		}
		m.agentQueue.Enqueue(prompt)
		m.agentPrompt = false
		m.agentPromptIn = nil
		m.msg = m.t("msg.agent_queued")
	case "backspace":
		if n := len(m.agentPromptIn); n > 0 {
			m.agentPromptIn = m.agentPromptIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.agentPromptIn = append(m.agentPromptIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// handleAgent handles keys while the agent panel is focused.
func (m *Model) handleAgent(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.agentOpen = false
		m.agentFocus = false
		m.msg = ""
	case "tab":
		// Move focus back to the editor; the panel stays open in the sidebar.
		m.agentFocus = false
		m.msg = ""
	case "j", "down":
		m.agentSel++
		m.clampAgentSel()
	case "k", "up":
		m.agentSel--
		m.clampAgentSel()
	case "g", "home":
		m.agentSel = 0
	case "G", "end":
		m.agentSel = len(m.agentQueue.Snapshot()) - 1
		m.clampAgentSel()
	case "x", "ctrl+d":
		m.cancelAgentSel()
	case "enter":
		m.reviewAgentSel()
	case "n":
		m.agentPrompt = true
		m.agentPromptIn = nil
	}
	return nil
}

// reviewAgentSel opens the diff review for a selected task that is in review.
func (m *Model) reviewAgentSel() {
	tasks := m.agentQueue.Snapshot()
	if len(tasks) == 0 || m.agentSel < 0 || m.agentSel >= len(tasks) {
		return
	}
	m.startAgentReview(tasks[m.agentSel].ID)
}

func (m *Model) clampAgentSel() {
	tasks := m.agentQueue.Snapshot()
	if len(tasks) == 0 {
		m.agentSel = 0
		return
	}
	if m.agentSel < 0 {
		m.agentSel = 0
	}
	if m.agentSel >= len(tasks) {
		m.agentSel = len(tasks) - 1
	}
}

func (m *Model) cancelAgentSel() {
	tasks := m.agentQueue.Snapshot()
	if len(tasks) == 0 || m.agentSel < 0 || m.agentSel >= len(tasks) {
		return
	}
	id := tasks[m.agentSel].ID
	m.agentRunner.Cancel(id)
	m.msg = m.t("msg.agent_cancelled", id)
}

// agentListHeight is how many tasks fit on screen at once.
func (m Model) agentListHeight() int {
	h := m.paneViewHeight(m.activePane)
	if h < 1 {
		h = 1
	}
	return h
}

// agentPanel renders the left sidebar rail with the task list.
func (m Model) agentPanel(h int) []string {
	tasks := m.agentQueue.Snapshot()

	// keep the selection on screen
	if m.agentOffset > m.agentSel {
		m.agentOffset = m.agentSel
	}
	if listH := m.agentListHeight(); m.agentOffset+listH <= m.agentSel {
		m.agentOffset = m.agentSel - listH + 1
	}
	if m.agentOffset < 0 {
		m.agentOffset = 0
	}

	rows := make([]string, 0, h)
	max := m.agentOffset + h
	for i := m.agentOffset; i < len(tasks) && i < max; i++ {
		t := tasks[i]
		sel := i == m.agentSel

		label := agentStatusLabel(t.Status)
		title := fitPath(t.Prompt, gitPanelWidth-10)
		line := " " + label + " " + title

		var progress string
		if t.Status == agent.StatusRunning {
			progress = " " + progressBar(t.Progress, 4)
		} else if t.Status == agent.StatusFailed {
			progress = " !"
		} else if t.Status == agent.StatusReview {
			progress = " ▶"
		}
		line += progress
		pad := gitPanelWidth - 1 - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		line += strings.Repeat(" ", pad) + " "
		if sel {
			rows = append(rows, statusHiStyle.Render(line))
		} else {
			rows = append(rows, line)
		}
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

func agentStatusLabel(s agent.Status) string {
	switch s {
	case agent.StatusQueued:
		return "Q"
	case agent.StatusRunning:
		return "R"
	case agent.StatusReview:
		return "▶"
	case agent.StatusApplied:
		return "A"
	case agent.StatusDone:
		return "✓"
	case agent.StatusFailed:
		return "!"
	case agent.StatusCancelled:
		return "×"
	}
	return "?"
}

func progressBar(p float32, width int) string {
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	filled := int(p * float32(width))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("·", width-filled) + "]"
}

// agentPromptLine renders the bottom input line while entering a new task.
func (m Model) agentPromptLine() string {
	line := statusHiStyle.Render(m.t("agent.task_label")) + statusStyle.Render(string(m.agentPromptIn)) + cursorStyle.Render(" ")
	hint := "  (Enter: queue, Esc: cancel)"
	line += hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// ---- Agent diff review (T6) ----

// startAgentReview opens the side-by-side review of a task that reached the
// review state. Enter on a review task triggers this.
func (m *Model) startAgentReview(id string) {
	task := m.agentQueue.Find(id)
	if task == nil || task.Status != agent.StatusReview || len(task.Changes) == 0 {
		m.msg = m.t("msg.agent_nothing")
		return
	}
	m.agentReviewMode = true
	m.agentReviewTaskID = id
	m.agentReviewChange = 0
	m.loadAgentChange(0)
	m.agentOpen = false
	m.agentFocus = false
}

// loadAgentChange loads the diff of the given change index into the review
// state.
func (m *Model) loadAgentChange(idx int) {
	task := m.agentQueue.Find(m.agentReviewTaskID)
	if task == nil || idx < 0 || idx >= len(task.Changes) {
		return
	}
	c := task.Changes[idx]
	orig := c.Orig
	prop := c.New
	if !strings.HasSuffix(orig, "\n") {
		orig += "\n"
	}
	if !strings.HasSuffix(prop, "\n") {
		prop += "\n"
	}
	m.agentReviewRows = vcs.SideBySide(orig, prop)
	m.agentReviewLeft = strings.Split(strings.TrimRight(orig, "\n"), "\n")
	m.agentReviewRight = strings.Split(strings.TrimRight(prop, "\n"), "\n")
	m.agentReviewOffY = 0
	m.agentReviewOffX = 0
}

// handleAgentReview handles keys while the agent diff review is shown.
func (m *Model) handleAgentReview(msg tea.KeyPressMsg) tea.Cmd {
	task := m.agentQueue.Find(m.agentReviewTaskID)
	n := 0
	if task != nil {
		n = len(task.Changes)
	}
	switch msg.String() {
	case "y", "enter", "a":
		m.acceptAgentReview()
	case "n", "esc", "r":
		m.rejectAgentReview()
	case "tab":
		if n > 0 {
			m.agentReviewChange = (m.agentReviewChange + 1) % n
			m.loadAgentChange(m.agentReviewChange)
			m.msg = ""
		}
	case "shift+tab":
		if n > 0 {
			m.agentReviewChange = (m.agentReviewChange - 1 + n) % n
			m.loadAgentChange(m.agentReviewChange)
			m.msg = ""
		}
	case "up", "k":
		if m.agentReviewOffY > 0 {
			m.agentReviewOffY--
		}
	case "down", "j":
		if m.agentReviewOffY < len(m.agentReviewRows)-1 {
			m.agentReviewOffY++
		}
	case "pgup":
		m.agentReviewOffY -= m.paneViewHeight(m.activePane) / 2
		if m.agentReviewOffY < 0 {
			m.agentReviewOffY = 0
		}
	case "pgdn":
		m.agentReviewOffY += m.paneViewHeight(m.activePane) / 2
		maxOff := len(m.agentReviewRows) - 1
		if m.agentReviewOffY > maxOff {
			m.agentReviewOffY = maxOff
		}
	case "home", "g":
		m.agentReviewOffY = 0
	case "end", "G":
		m.agentReviewOffY = len(m.agentReviewRows) - 1
		if m.agentReviewOffY < 0 {
			m.agentReviewOffY = 0
		}
	case "left", "h":
		m.agentReviewOffX -= 8
		if m.agentReviewOffX < 0 {
			m.agentReviewOffX = 0
		}
	case "right", "l":
		m.agentReviewOffX += 8
	}
	return nil
}

// acceptAgentReview applies the task's changes atomically, commits them, and
// refreshes open buffers.
func (m *Model) acceptAgentReview() {
	task := m.agentQueue.Find(m.agentReviewTaskID)
	if task == nil {
		m.agentReviewMode = false
		return
	}

	paths := make([]string, len(task.Changes))
	created, modified := 0, 0
	for i := range task.Changes {
		p := task.Changes[i].Path
		paths[i] = p
		// Existence must be captured before apply (the applier writes files,
		// so after apply every target exists).
		if fileExists(agentFullPath(m.baseDir(), p)) {
			modified++
		} else {
			created++
		}
	}

	if err := m.agentApplier.Apply(task.Changes); err != nil {
		m.msg = m.t("msg.agent_apply_fail", err.Error())
		m.agentReviewMode = false
		return
	}

	if err := m.agentCommit.Commit(paths, task.Prompt); err != nil {
		m.msg = m.t("msg.agent_commit_fail", err.Error())
	} else {
		// Open every created/modified file in a new tab (or focus an existing
		// tab) and reload clean buffers so the changes are immediately visible.
		for _, p := range paths {
			m.openAiFile(filepath.ToSlash(p), true)
		}
		m.msg = m.t("msg.agent_applied_files", created, modified)
	}

	m.agentQueue.SetStatus(task.ID, agent.StatusApplied)
	m.agentReviewMode = false
	m.restoreAgentPanel()
}

// restoreAgentPanel brings the task list back on screen after a diff review
// ended, so the panel does not silently collapse.
func (m *Model) restoreAgentPanel() {
	m.agentOpen = true
	m.agentFocus = true
	m.msg = ""
}

// agentFullPath resolves a change path (relative to the project root) to an
// absolute filesystem path.
func agentFullPath(base, rel string) string {
	full := rel
	if !filepath.IsAbs(full) {
		full = filepath.Join(base, filepath.FromSlash(rel))
	}
	return full
}

// rejectAgentReview discards a task's proposed changes (returns it to the list).
func (m *Model) rejectAgentReview() {
	m.agentQueue.SetStatus(m.agentReviewTaskID, agent.StatusDone)
	m.agentReviewMode = false
	m.restoreAgentPanel()
	m.msg = m.t("msg.agent_discarded")
}

// agentReviewBottom renders the status line while reviewing a task's diff.
func (m Model) agentReviewBottom() string {
	task := m.agentQueue.Find(m.agentReviewTaskID)
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.agentReviewRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	var path string
	if task != nil && m.agentReviewChange < len(task.Changes) {
		path = task.Changes[m.agentReviewChange].Path
	}
	title := " Agent diff: " + fitPath(path, 30)
	if task != nil && len(task.Changes) > 1 {
		title += fmt.Sprintf(" (%d/%d)", m.agentReviewChange+1, len(task.Changes))
	}
	line := statusHiStyle.Render(title) +
		hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render(m.t("agent.review_hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// openAiFile opens a file that AI created or modified in a new tab (or focuses
// the already-open tab, never duplicating) and reloads any clean open buffer so
// the on-disk content shows immediately. dirty buffers are left untouched.
func (m *Model) openAiFile(path string, reloadClean bool) {
	m.focusOrOpen(path)
	if reloadClean {
		m.reloadChangedBuffer(path)
	}
}

// fileExists reports whether a path exists on disk.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// reloadChangedBuffer reloads a clean open buffer whose path matches, so an
// applied agent edit is reflected immediately. Both the given path and the
// open tab paths are compared in absolute form.
func (m *Model) reloadChangedBuffer(path string) {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(m.baseDir(), filepath.FromSlash(path))
	}
	absFull, _ := filepath.Abs(full)
	for i := range m.tabs {
		t := &m.tabs[i]
		if t.path == "" || t.buf.Dirty() {
			continue
		}
		absT, _ := filepath.Abs(t.path)
		if absT != absFull {
			continue
		}
		if data, err := os.ReadFile(full); err == nil {
			t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
			t.syntaxCached = nil
			t.diffText = ""
		}
	}
}
