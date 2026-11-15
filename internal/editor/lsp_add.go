package editor

import (
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
)

// startLspAddPrompt initiates the two-step prompt to add or configure an LSP server.
// Step 0 asks for the language or file extension (pre-filling with the active tab's language).
// Step 1 asks for the server command line (pre-filling with existing or default command).
func (m *Model) startLspAddPrompt() {
	m.lspAddOpen = true
	m.lspAddStep = 0
	m.lspAddLang = ""
	defaultLang := ""
	if t := m.cur(); t != nil && t.path != "" {
		ext := strings.ToLower(filepath.Ext(t.path))
		_, _, lang := lspServerFor(ext)
		if lang != "" {
			defaultLang = lang
		} else if ext != "" {
			defaultLang = strings.TrimPrefix(ext, ".")
		}
	}
	m.lspAddIn = []rune(defaultLang)
}

func (m *Model) handleLspAddPrompt(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("lsp-add", s) {
	case "lsp-add.close":
		m.lspAddOpen = false
		m.lspAddStep = 0
		m.lspAddLang = ""
		m.lspAddIn = nil
		m.msg = ""
	case "lsp-add.run":
		if m.lspAddStep == 0 {
			lang := strings.ToLower(strings.TrimSpace(string(m.lspAddIn)))
			lang = strings.TrimPrefix(lang, ".")
			if lang == "" {
				m.msg = m.t("msg.lsp_lang_required")
				return nil
			}
			m.lspAddLang = lang
			m.lspAddStep = 1

			// Pre-fill command if already configured or known from built-ins
			suggested := ""
			if m.cfg.LSP.Languages != nil && m.cfg.LSP.Languages[lang] != "" {
				suggested = m.cfg.LSP.Languages[lang]
			} else {
				cmd, args, _ := lspServerFor("." + lang)
				if cmd != "" {
					if len(args) > 0 {
						suggested = cmd + " " + strings.Join(args, " ")
					} else {
						suggested = cmd
					}
				}
			}
			m.lspAddIn = []rune(suggested)
			m.msg = ""
			return nil
		}

		// Step 1: server command
		cmd := strings.TrimSpace(string(m.lspAddIn))
		if cmd == "" {
			m.msg = m.t("msg.lsp_cmd_required")
			return nil
		}
		lang := m.lspAddLang
		m.lspAddOpen = false
		m.lspAddStep = 0
		m.lspAddLang = ""
		m.lspAddIn = nil
		m.saveLspLanguage(lang, cmd)
	case "lsp-add.erase":
		if n := len(m.lspAddIn); n > 0 {
			m.lspAddIn = m.lspAddIn[:n-1]
		}
	case paneUnbound:
		return nil
	default:
		if len(msg.Text) > 0 {
			m.lspAddIn = append(m.lspAddIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) saveLspLanguage(lang, cmd string) {
	path := m.aiConfigPath()
	if err := config.WriteLSPLanguage(path, lang, cmd); err != nil {
		m.msg = m.t("msg.lsp_save_failed", err.Error())
		return
	}
	if m.cfg.LSP.Languages == nil {
		m.cfg.LSP.Languages = map[string]string{}
	}
	m.cfg.LSP.Languages[lang] = cmd
	if m.cfg.LSP.Disabled != nil {
		delete(m.cfg.LSP.Disabled, lang)
	}
	m.msg = m.t("msg.lsp_added", lang, cmd)

	// If current active tab matches this language/ext, restart LSP client with the new configuration
	if t := m.cur(); t != nil && t.path != "" {
		curExt := strings.ToLower(filepath.Ext(t.path))
		curCmd, _, curLang := m.lspResolve(curExt)
		if curLang == lang || strings.TrimPrefix(curExt, ".") == lang {
			if m.lspClient != nil {
				_ = m.lspClient.Close()
				m.lspClient = nil
			}
			if curCmd != "" {
				m.ensureLSP()
			}
		}
	}

	// If the binary is missing on PATH and has a known installer, offer to install it now!
	fields := strings.Fields(cmd)
	if len(fields) > 0 {
		bin := fields[0]
		if _, err := exec.LookPath(bin); err != nil {
			if inst := lspInstallFor(bin); inst != "" {
				m.lspInstallConfirm = true
				m.lspInstallBin = bin
				m.lspInstallCmd = inst
			}
		}
	}
}
