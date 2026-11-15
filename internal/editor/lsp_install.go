package editor

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type lspServerInfo struct {
	Lang    string
	Bin     string
	Command string
}

var knownLSPServers = []lspServerInfo{
	{Lang: "Go", Bin: "gopls", Command: "go install golang.org/x/tools/gopls@latest"},
	{Lang: "Python", Bin: "pyright-langserver", Command: "npm i -g pyright"},
	{Lang: "TypeScript / JS", Bin: "typescript-language-server", Command: "npm i -g typescript-language-server typescript"},
	{Lang: "Rust", Bin: "rust-analyzer", Command: "rustup component add rust-analyzer"},
	{Lang: "JSON / CSS / HTML", Bin: "vscode-langservers-extracted", Command: "npm i -g vscode-langservers-extracted"},
	{Lang: "YAML", Bin: "yaml-language-server", Command: "npm i -g yaml-language-server"},
	{Lang: "PHP", Bin: "intelephense", Command: "npm i -g intelephense"},
	{Lang: "Ruby", Bin: "solargraph", Command: "gem install solargraph"},
}

type lspInstallResultMsg struct {
	bin    string
	err    error
	output string
}

func (m *Model) openLspInstall() tea.Cmd {
	// If current active tab has a missing server with a known install command, prompt immediately!
	if t := m.cur(); t != nil && t.path != "" {
		cmd, _, _ := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
		if cmd != "" {
			if _, err := exec.LookPath(cmd); err != nil {
				if inst := lspInstallFor(cmd); inst != "" {
					m.lspInstallConfirm = true
					m.lspInstallBin = cmd
					m.lspInstallCmd = inst
					return nil
				}
			}
		}
	}

	// Otherwise, open the pick list of known servers
	m.lspInstallPickOpen = true
	m.lspInstallPickSel = 0
	m.lspInstallPickOff = 0
	return nil
}

func (m *Model) startLspInstall(bin, cmdLine string) tea.Cmd {
	m.msg = m.t("msg.lsp_installing", bin, cmdLine)
	return func() tea.Msg {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", cmdLine)
		} else {
			cmd = exec.Command("sh", "-c", cmdLine)
		}
		out, err := cmd.CombinedOutput()
		return lspInstallResultMsg{bin: bin, err: err, output: string(out)}
	}
}

func (m *Model) handleLspInstallResult(msg lspInstallResultMsg) {
	if msg.err != nil {
		m.msg = m.t("msg.lsp_install_failed", msg.bin, msg.err.Error())
		return
	}
	m.msg = m.t("msg.lsp_install_ok", msg.bin)
	if t := m.cur(); t != nil && t.path != "" {
		cmd, _, _ := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
		if cmd == msg.bin || filepath.Base(cmd) == msg.bin {
			m.ensureLSP()
		}
	}
}

func (m *Model) handleLspInstallConfirm(s string) tea.Cmd {
	switch m.paneCmd("lsp-install-confirm", s) {
	case "lsp-install-confirm.yes":
		bin := m.lspInstallBin
		cmdLine := m.lspInstallCmd
		m.lspInstallConfirm = false
		m.lspInstallBin = ""
		m.lspInstallCmd = ""
		return m.startLspInstall(bin, cmdLine)
	case "lsp-install-confirm.no":
		m.lspInstallConfirm = false
		m.lspInstallBin = ""
		m.lspInstallCmd = ""
		m.msg = ""
	case paneUnbound:
		return nil
	}
	return nil
}

func (m *Model) handleLspInstallPick(s string) tea.Cmd {
	switch m.paneCmd("lsp-install-pick", s) {
	case "lsp-install-pick.close":
		m.lspInstallPickOpen = false
		m.msg = ""
	case "lsp-install-pick.up":
		if n := len(knownLSPServers); n > 0 {
			m.lspInstallPickSel = (m.lspInstallPickSel - 1 + n) % n
			m.clampLspInstallPickWindow()
		}
	case "lsp-install-pick.down":
		if n := len(knownLSPServers); n > 0 {
			m.lspInstallPickSel = (m.lspInstallPickSel + 1) % n
			m.clampLspInstallPickWindow()
		}
	case "lsp-install-pick.open":
		return m.applyLspInstallPick()
	case paneUnbound:
		return nil
	}
	return nil
}

func (m *Model) clampLspInstallPickWindow() {
	const visible = 10
	n := len(knownLSPServers)
	if n <= visible {
		m.lspInstallPickOff = 0
		return
	}
	if m.lspInstallPickSel < m.lspInstallPickOff {
		m.lspInstallPickOff = m.lspInstallPickSel
	}
	if m.lspInstallPickSel >= m.lspInstallPickOff+visible {
		m.lspInstallPickOff = m.lspInstallPickSel - visible + 1
	}
}

func (m *Model) applyLspInstallPick() tea.Cmd {
	m.lspInstallPickOpen = false
	if m.lspInstallPickSel < 0 || m.lspInstallPickSel >= len(knownLSPServers) {
		return nil
	}
	srv := knownLSPServers[m.lspInstallPickSel]
	return m.startLspInstall(srv.Bin, srv.Command)
}

func (m Model) lspInstallConfirmLine() string {
	line := statusHiStyle.Render(m.t("lsp.install_confirm", m.lspInstallBin, m.lspInstallCmd))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) lspInstallPickExtraRows() int {
	if !m.lspInstallPickOpen {
		return 0
	}
	n := len(knownLSPServers)
	if n > 10 {
		n = 10
	}
	return n + 2
}

func (m Model) lspInstallPickPanel() []string {
	rows := make([]string, 0, len(knownLSPServers)+1)
	title := statusHiStyle.Render(" " + m.t("lsp.install_panel_title"))
	fill := m.width - lipgloss.Width(title)
	if fill > 0 {
		title += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, title)

	const visible = 10
	n := len(knownLSPServers)
	end := m.lspInstallPickOff + visible
	if end > n {
		end = n
	}

	for i := m.lspInstallPickOff; i < end; i++ {
		srv := knownLSPServers[i]
		prefix := "  "
		if i == m.lspInstallPickSel {
			prefix = "> "
		}
		status := "[missing]"
		if _, err := exec.LookPath(srv.Bin); err == nil {
			status = "[installed]"
		}

		line := fmt.Sprintf("%s%-18s %-28s %-12s %s", prefix, srv.Lang, srv.Bin, status, srv.Command)
		var rendered string
		if i == m.lspInstallPickSel {
			rendered = activeStyle.Render(line)
		} else {
			rendered = statusStyle.Render(line)
		}
		f := m.width - lipgloss.Width(rendered)
		if f > 0 {
			if i == m.lspInstallPickSel {
				rendered += activeStyle.Render(strings.Repeat(" ", f))
			} else {
				rendered += statusStyle.Render(strings.Repeat(" ", f))
			}
		}
		rows = append(rows, rendered)
	}
	return rows
}
