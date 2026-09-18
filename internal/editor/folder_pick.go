package editor

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
	"dmed/internal/i18n"
	"dmed/internal/vcs"
)

// folderPickMsg carries the folder chosen by the system folder dialog.
// An empty path means the user cancelled; err carries a hard failure.
type folderPickMsg struct {
	path string
	err  error
}

// openFolderCmd shows the native folder picker in the background and, on
// success, switches the editor's project root to the chosen folder.
func (m *Model) openFolderCmd() tea.Cmd {
	initial := m.baseDir()
	return func() tea.Msg {
		path, err := chooseFolder(initial)
		return folderPickMsg{path: path, err: err}
	}
}

// chooseFolder runs the platform's folder selection dialog, seeded with
// initial. It returns ("", nil) when the user cancels, the picked absolute
// path, or an error when no picker could be driven.
func chooseFolder(initial string) (string, error) {
	var args []string
	switch runtime.GOOS {
	case "windows":
		args = windowsFolderPicker(initial)
	case "darwin":
		args = darwinFolderPicker(initial)
	default:
		if args = linuxFolderPicker(initial); args == nil {
			return "", fmt.Errorf("no folder picker found (install zenity or kdialog)")
		}
	}
	path, err := runPicker(args)
	if err != nil {
		if isCancel(err) {
			return "", nil
		}
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil // cancelled
	}
	return strings.TrimSpace(path), nil
}

// windowsFolderPicker drives .NET's FolderBrowserDialog through PowerShell.
func windowsFolderPicker(initial string) []string {
	esc := func(s string) string {
		s = strings.ReplaceAll(s, "'", "''")
		return "'" + s + "'"
	}
	script := "Add-Type -AssemblyName System.Windows.Forms;" +
		"$d=New-Object System.Windows.Forms.FolderBrowserDialog;" +
		"$d.Description='Select project folder';" +
		"$d.ShowNewFolderButton=$true;" +
		"if(Test-Path " + esc(initial) + "){$d.SelectedPath=" + esc(initial) + "};" +
		"if($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){Write-Output $d.SelectedPath}else{exit 1}"
	return []string{"-NoProfile", "-STA", "-Command", script}
}

// darwinFolderPicker drives the macOS folder chooser through osascript.
func darwinFolderPicker(initial string) []string {
	cmd := "POSIX path of (choose folder"
	if initial != "" {
		cmd += ` with prompt "Select project folder" default location POSIX file "` + strings.ReplaceAll(initial, `"`, `\"`) + `"`
	} else {
		cmd += ` with prompt "Select project folder"`
	}
	cmd += ")"
	return []string{"-e", cmd}
}

// linuxFolderPicker prefers zenity and falls back to kdialog. A nil return
// means neither is available.
func linuxFolderPicker(initial string) []string {
	if _, err := exec.LookPath("zenity"); err == nil {
		return []string{"--file-selection", "--directory", "--title=Select project folder"}
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return []string{"--getexistingdirectory", initial}
	}
	return nil
}

// runPicker launches the picker command and returns its first output line.
func runPicker(args []string) (string, error) {
	var bin string
	switch runtime.GOOS {
	case "windows":
		bin = "powershell"
	case "darwin":
		bin = "osascript"
	default:
		if _, err := exec.LookPath("zenity"); err == nil {
			bin = "zenity"
		} else {
			bin = "kdialog"
		}
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", bin, err)
	}
	line := string(out)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line, nil
}

// isCancel guesses whether a picker error meant the user aborted the dialog
// rather than a real failure.
func isCancel(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "cancel") ||
		strings.Contains(s, "user aborted") ||
		strings.Contains(s, "no files were found") ||
		strings.Contains(s, "exit status 1")
}

// switchRoot repoints the project root at path and re-initializes the
// root-derived state: config, UI language, project tree, git repo, file
// watcher and project plugins.
func (m *Model) switchRoot(path string) {
	m.root = normalizePath(".", path)
	m.cfg = config.Load(m.root)
	m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
	m.treeVisible = true
	m.rebuildTree()
	if repo, err := vcs.Open(m.baseDir()); err == nil {
		m.repo = repo
	}
	if m.watcher != nil {
		m.watcher.Watch(m.root)
	}
	m.loadPlugins()
	m.msg = m.t("msg.project", filepath.Base(m.root))
}