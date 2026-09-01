package editor

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"dmed/internal/syntax"
	"dmed/internal/vcs"
)

// cyrToLat maps a Cyrillic (ЙЦУКЕН) letter to its QWERTY physical key. The
// Russian layout is positionally identical to US QWERTY, so this lets letter
// commands (c/a/r/l/d/b/i…) work under a Russian keyboard layout on any
// terminal, even ones that don't populate Key.BaseCode (Kitty / Windows
// Console API are the only backends that do). Returns 0 for non-Cyrillic.
func cyrToLat(r rune) rune {
	switch r {
	case 'й', 'Й':
		return 'q'
	case 'ц', 'Ц':
		return 'w'
	case 'у', 'У':
		return 'e'
	case 'к', 'К':
		return 'r'
	case 'е', 'Е':
		return 't'
	case 'н', 'Н':
		return 'y'
	case 'г', 'Г':
		return 'u'
	case 'ш', 'Ш':
		return 'i'
	case 'щ', 'Щ':
		return 'o'
	case 'з', 'З':
		return 'p'
	case 'х', 'Х':
		return '['
	case 'ъ', 'Ъ':
		return ']'
	case 'ф', 'Ф':
		return 'a'
	case 'ы', 'Ы':
		return 's'
	case 'в', 'В':
		return 'd'
	case 'а', 'А':
		return 'f'
	case 'п', 'П':
		return 'g'
	case 'р', 'Р':
		return 'h'
	case 'о', 'О':
		return 'j'
	case 'л', 'Л':
		return 'k'
	case 'д', 'Д':
		return 'l'
	case 'ж', 'Ж':
		return ';'
	case 'э', 'Э':
		return '\''
	case 'я', 'Я':
		return 'z'
	case 'ч', 'Ч':
		return 'x'
	case 'с', 'С':
		return 'c'
	case 'м', 'М':
		return 'v'
	case 'и', 'И':
		return 'b'
	case 'т', 'Т':
		return 'n'
	case 'ь', 'Ь':
		return 'm'
	case 'б', 'Б':
		return ','
	case 'ю', 'Ю':
		return '.'
	}
	return 0
}

// gitKeyName returns a layout-independent name for a key press so commands
// like c/a/r/l/d/b/i work regardless of the active (e.g. Cyrillic) keyboard
// layout. It tries, in order: the physical PC-101 key (BaseCode/ShiftedCode,
// Windows Console API / Kitty), a Cyrillic→QWERTY lookup of the received
// letter, then falls back to String(). Special keys (esc, arrows, tab,
// enter) are identical in every layout and come through unchanged.
func gitKeyName(msg tea.KeyPressMsg) string {
	if msg.BaseCode >= 'a' && msg.BaseCode <= 'z' {
		return string(msg.BaseCode)
	}
	if msg.ShiftedCode >= 'a' && msg.ShiftedCode <= 'z' {
		return string(msg.ShiftedCode)
	}
	if c := cyrToLat(msg.Code); c >= 'a' && c <= 'z' {
		return string(c)
	}
	for _, r := range msg.Text {
		if c := cyrToLat(r); c >= 'a' && c <= 'z' {
			return string(c)
		}
	}
	return msg.String()
}

// gitPanelMode controls what the Git panel is showing.
type gitPanelMode int

const (
	gitModeStatus gitPanelMode = iota // file list
	gitModeCommit                     // commit message input
	gitModeLog                        // commit history list
	gitModeBranch                     // branch input / switch
)

// t returns the translated UI string for key, formatted with args.
func (m Model) t(key string, args ...any) string { return m.tr.T(key, args...) }

func (m *Model) openGitPanel() {
	m.gitOpen = true
	m.gitMode = gitModeStatus
	m.gitCommitIn = nil
	m.gitDiffFocused = false
	m.treeFocus = false
	m.refreshGitFiles()
	m.refreshGitDiffPreview()
}

func (m *Model) refreshGitFiles() {
	r := m.repoForCur()
	if r == nil {
		m.gitFiles = nil
		return
	}
	files, err := r.StatusFiles()
	if err != nil {
		m.gitFiles = nil
		m.msg = m.t("git.status_error", err.Error())
		return
	}
	m.gitFiles = files
	if m.gitSel >= len(m.gitFiles) {
		m.gitSel = maxInt(0, len(m.gitFiles)-1)
	}
	m.clampGitScroll()
}

func (m *Model) repoForCur() *vcs.Repo {
	t := m.cur()
	r := m.repo
	if t.path == "" {
		return r
	}
	if r != nil && strings.HasPrefix(t.path, r.Root) {
		return r
	}
	if found, err := vcs.Open(filepath.Dir(t.path)); err == nil {
		return found
	}
	return nil
}

func (m Model) gitListHeight() int {
	n := len(m.gitFiles)
	if room := m.viewHeight(); n > room {
		n = room
	}
	return n
}

func (m *Model) clampGitScroll() {
	w := m.gitListHeight()
	if w <= 0 {
		m.gitOffset = 0
		return
	}
	if m.gitSel < m.gitOffset {
		m.gitOffset = m.gitSel
	}
	if m.gitSel >= m.gitOffset+w {
		m.gitOffset = m.gitSel - w + 1
	}
	if off := len(m.gitFiles) - w; m.gitOffset > off {
		m.gitOffset = off
	}
	if m.gitOffset < 0 {
		m.gitOffset = 0
	}
}

// refreshGitDiffPreview computes the side-by-side diff for the currently
// selected file in the git panel so it can be shown inline.
func (m *Model) refreshGitDiffPreview() {
	if m.gitSel >= len(m.gitFiles) {
		m.diffRows = nil
		m.diffHeadLines = nil
		m.diffRightLines = nil
		m.diffPath = ""
		return
	}
	fs := m.gitFiles[m.gitSel]
	r := m.repoForCur()
	if r == nil {
		m.diffRows = nil
		return
	}
	abs := filepath.Join(r.Root, filepath.FromSlash(fs.Path))

	headText, err := r.HeadContent(abs)
	if err != nil {
		headText = "" // untracked
	}

	right := ""
	if t := m.tabByPath(abs); t != nil {
		right = t.buf.Text()
	} else if data, rerr := os.ReadFile(abs); rerr == nil {
		right = strings.ReplaceAll(string(data), "\r\n", "\n")
	}

	m.diffPath = fs.Path
	m.diffHeadLines = splitLines(headText)
	m.diffRightLines = splitLines(right)
	m.diffRows = vcs.SideBySide(headText, right)
	m.diffOffsetY = 0
	m.diffOffsetX = 0
	m.diffHeadSyntax = syntax.Default().HighlightBuffer(fs.Path, headText)
	m.diffRightSyntax = syntax.Default().HighlightBuffer(fs.Path, right)
}

func (m *Model) showTree() {
	m.treeVisible = true
	m.treeFocus = true
	m.rebuildTree()
}

// --- Git log ---

func (m *Model) refreshGitLog() {
	r := m.repoForCur()
	if r == nil {
		m.gitLogEntries = nil
		return
	}
	entries, err := r.Log(50)
	if err != nil {
		m.gitLogEntries = nil
		m.msg = m.t("git.log_error", err.Error())
		return
	}
	m.gitLogEntries = entries
	if m.gitLogSel >= len(m.gitLogEntries) {
		m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
	}
	m.clampGitLogScroll()
}

func (m Model) gitLogListHeight() int {
	// Each log entry takes 2 rows (hash+subject + author+time)
	n := len(m.gitLogEntries) * 2
	if room := m.viewHeight(); n > room {
		n = room
	}
	return n
}

func (m *Model) clampGitLogScroll() {
	maxRows := m.gitLogListHeight()
	if maxRows <= 0 {
		m.gitLogOffset = 0
		return
	}
	// Each entry is 2 rows; selected entry's top row = sel*2
	selRow := m.gitLogSel * 2
	if selRow < m.gitLogOffset {
		m.gitLogOffset = selRow
	}
	if selRow+1 >= m.gitLogOffset+maxRows {
		m.gitLogOffset = selRow + 2 - maxRows
	}
	totalRows := len(m.gitLogEntries) * 2
	maxOff := totalRows - maxRows
	if maxOff < 0 {
		maxOff = 0
	}
	if m.gitLogOffset > maxOff {
		m.gitLogOffset = maxOff
	}
	if m.gitLogOffset < 0 {
		m.gitLogOffset = 0
	}
}

func (m *Model) openLogView() {
	m.gitMode = gitModeLog
	m.gitLogSel = 0
	m.gitLogOffset = 0
	m.gitDiffFocused = false
	m.refreshGitLog()
	m.showLogDiff()
}

func (m *Model) showLogDiff() {
	if m.gitLogSel >= len(m.gitLogEntries) {
		m.diffRows = nil
		return
	}
	r := m.repoForCur()
	if r == nil {
		m.diffRows = nil
		return
	}
	entry := m.gitLogEntries[m.gitLogSel]
	diffs, err := r.CommitDiff(entry.FullHash)
	if err != nil {
		m.diffRows = nil
		m.msg = m.t("git.diff_error", err.Error())
		return
	}
	// Build concatenated diff with file separator lines.
	var headAll, rightAll []string
	var rowsAll []vcs.DiffRow
	for fi, d := range diffs {
		if fi > 0 {
			// Empty separator line between files
			headAll = append(headAll, "")
			rightAll = append(rightAll, "")
			rowsAll = append(rowsAll, vcs.DiffRow{Left: len(headAll) - 1, Right: len(rightAll) - 1, Type: vcs.DiffNone})
		}
		// File header line
		header := "── " + d.Path + " ──"
		headAll = append(headAll, header)
		rightAll = append(rightAll, "")
		rowsAll = append(rowsAll, vcs.DiffRow{Left: len(headAll) - 1, Right: -1, Type: vcs.DiffNone})

		offsetL := len(headAll)
		offsetR := len(rightAll)
		for _, dr := range d.DiffRows {
			nr := dr
			if dr.Left >= 0 {
				nr.Left = dr.Left + offsetL
			}
			if dr.Right >= 0 {
				nr.Right = dr.Right + offsetR
			}
			rowsAll = append(rowsAll, nr)
		}
		headAll = append(headAll, d.HeadLines...)
		rightAll = append(rightAll, d.RightLines...)
	}
	m.diffPath = entry.Hash + " " + entry.Subject
	m.diffHeadLines = headAll
	m.diffRightLines = rightAll
	m.diffRows = rowsAll
	m.diffOffsetY = 0
	m.diffOffsetX = 0
}

// --- Branch management ---

func (m *Model) openBranchView() {
	m.gitMode = gitModeBranch
	m.gitBranchNew = false
	m.gitBranchIn = nil
	m.gitBranchSel = 0
	m.gitBranchOffset = 0
	m.gitDiffFocused = false
	m.refreshGitBranches()
}

func (m *Model) refreshGitBranches() {
	r := m.repoForCur()
	if r == nil {
		m.gitBranchList = nil
		return
	}
	branches, err := r.Branches()
	if err != nil {
		m.gitBranchList = nil
		m.msg = m.t("git.branches_error", err.Error())
		return
	}
	m.gitBranchList = branches
	m.clampGitBranchScroll()
}

func (m Model) gitBranchListHeight() int {
	n := len(m.gitBranchList)
	if n == 0 {
		n = 1 // at least one blank row
	}
	if room := m.viewHeight(); n > room {
		n = room
	}
	return n
}

func (m *Model) clampGitBranchScroll() {
	w := m.gitBranchListHeight()
	if w <= 0 {
		m.gitBranchOffset = 0
		return
	}
	if m.gitBranchSel < m.gitBranchOffset {
		m.gitBranchOffset = m.gitBranchSel
	}
	if m.gitBranchSel >= m.gitBranchOffset+w {
		m.gitBranchOffset = m.gitBranchSel - w + 1
	}
	if off := len(m.gitBranchList) - w; m.gitBranchOffset > off {
		m.gitBranchOffset = off
	}
	if m.gitBranchOffset < 0 {
		m.gitBranchOffset = 0
	}
}

func (m *Model) branchPanel(h int) []string {
	rows := make([]string, 0, h)
	cur := ""
	if r := m.repoForCur(); r != nil {
		cur = r.Branch()
	}
	start := m.gitBranchOffset
	end := start + m.gitBranchListHeight()
	for i := start; i < end && len(rows) < h; i++ {
		var name string
		if i < len(m.gitBranchList) {
			name = m.gitBranchList[i]
		}
		isCur := name != "" && name == cur
		prefix := "  "
		if isCur {
			prefix = "● "
		}
		plain := " " + prefix + name
		pad := gitPanelWidth - 1 - lipgloss.Width(plain)
		if pad < 0 {
			pad = 0
		}
		line := plain + strings.Repeat(" ", pad)
		var cell string
		switch {
		case i == m.gitBranchSel:
			cell = statusHiStyle.Render(line)
		case isCur:
			styled := " " + gitAddStyle.Render(prefix) + name
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		default:
			cell = line
		}
		rows = append(rows, cell+" ")
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

func (m *Model) handleGitBranch(msg tea.KeyPressMsg) tea.Cmd {
	key := gitKeyName(msg)
	keyL := strings.ToLower(key)
	r := m.repoForCur()

	// While naming a new branch, every key belongs to the name input.
	if m.gitBranchNew {
		switch key {
		case "enter":
			if len(m.gitBranchIn) > 0 {
				if r == nil {
					m.msg = m.t("git.norepo_msg")
					break
				}
				if err := r.CreateBranch(string(m.gitBranchIn)); err != nil {
					m.msg = m.t("git.create_error", err.Error())
				} else {
					m.msg = m.t("git.created", string(m.gitBranchIn))
					m.gitBranchNew = false
					m.gitBranchIn = nil
					m.gitMode = gitModeStatus
					m.refreshGitFiles()
					m.refreshGitDiffPreview()
				}
			}
		case "esc":
			m.gitBranchNew = false
			m.gitBranchIn = nil
			m.msg = ""
		case "backspace":
			if n := len(m.gitBranchIn); n > 0 {
				m.gitBranchIn = m.gitBranchIn[:n-1]
			}
		default:
			if len(msg.Text) > 0 {
				m.gitBranchIn = append(m.gitBranchIn, []rune(msg.Text)...)
			}
		}
		return nil
	}

	switch keyL {
	case "esc":
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
	case "q":
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
	case "n":
		// Create a new branch (enter name)
		m.gitBranchNew = true
		m.gitBranchIn = nil
		m.msg = m.t("git.new_branch_name")
	case "enter":
		// Switch to selected branch
		if r == nil || len(m.gitBranchList) == 0 {
			break
		}
		name := m.gitBranchList[m.gitBranchSel]
		if name == r.Branch() {
			m.msg = m.t("git.already_on", name)
			break
		}
		if err := r.SwitchBranch(name); err != nil {
			m.msg = m.t("git.switch_error", err.Error())
		} else {
			m.msg = m.t("git.switched", name)
			m.gitMode = gitModeStatus
			m.refreshGitFiles()
			m.refreshGitDiffPreview()
		}
	case "up", "k":
		if m.gitBranchSel > 0 {
			m.gitBranchSel--
			m.clampGitBranchScroll()
		}
	case "down", "j":
		if m.gitBranchSel < len(m.gitBranchList)-1 {
			m.gitBranchSel++
			m.clampGitBranchScroll()
		}
	case "r":
		m.refreshGitBranches()
		m.msg = m.t("git.refreshed")
	}
	return nil
}

// --- Dispatch ---

func (m *Model) handleGit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+b", "f9":
		m.gitOpen = false
		m.msg = ""
		m.showTree()
		return nil
	}
	switch m.gitMode {
	case gitModeStatus:
		return m.handleGitStatus(msg)
	case gitModeCommit:
		return m.handleGitCommit(msg)
	case gitModeLog:
		return m.handleGitLog(msg)
	case gitModeBranch:
		return m.handleGitBranch(msg)
	}
	return nil
}

// --- Status mode ---

func (m *Model) handleGitStatus(msg tea.KeyPressMsg) tea.Cmd {
	key := gitKeyName(msg)
	// Letter commands are case-insensitive so the "D:diff"-style hints work
	// whether the user types 'd' or 'D'. The diff-focus block below still uses
	// the raw key to keep 'g' (top) and 'G' (bottom) distinct.
	keyL := strings.ToLower(key)

	// Global keys
	switch keyL {
	case "esc", "ctrl+g":
		m.gitOpen = false
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	case "q":
		if m.gitDiffFocused {
			m.gitDiffFocused = false
			return nil
		}
		m.gitOpen = false
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	}

	// Diff area has focus — scroll
	if m.gitDiffFocused {
		h := m.viewHeight()
		switch key {
		case "left", "tab":
			m.gitDiffFocused = false
			return nil
		case "up", "k":
			m.diffOffsetY--
		case "down", "j":
			m.diffOffsetY++
		case "pgup":
			m.diffOffsetY -= h
		case "pgdn":
			m.diffOffsetY += h
		case "home", "g":
			m.diffOffsetY = 0
		case "end", "G":
			m.diffOffsetY = len(m.diffRows)
		case "h":
			m.diffOffsetX -= 8
		case "right", "l":
			m.diffOffsetX += 8
		}
		m.clampDiffScroll(h)
		return nil
	}

	// File list has focus
	switch keyL {
	case "right", "tab":
		if len(m.diffRows) > 0 {
			m.gitDiffFocused = true
		}
		return nil
	case "up", "k":
		if m.gitSel > 0 {
			m.gitSel--
			m.clampGitScroll()
			m.refreshGitDiffPreview()
		}
	case "down", "j":
		if m.gitSel < len(m.gitFiles)-1 {
			m.gitSel++
			m.clampGitScroll()
			m.refreshGitDiffPreview()
		}
	case "space":
		if m.gitSel >= len(m.gitFiles) {
			break
		}
		fs := m.gitFiles[m.gitSel]
		r := m.repoForCur()
		if r == nil {
			break
		}
		absPath := filepath.Join(r.Root, filepath.FromSlash(fs.Path))
		if fs.IsStaged() {
			if err := r.Unstage(absPath); err != nil {
				m.msg = m.t("git.unstage_error", err.Error())
			} else {
				m.msg = m.t("git.unstaged", fs.Path)
			}
		} else {
			if err := r.Stage(absPath); err != nil {
				m.msg = m.t("git.stage_error", err.Error())
			} else {
				m.msg = m.t("git.staged", fs.Path)
			}
		}
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
	case "enter":
		if m.gitSel >= len(m.gitFiles) {
			break
		}
		r := m.repoForCur()
		if r != nil {
			absPath := filepath.Join(r.Root, filepath.FromSlash(m.gitFiles[m.gitSel].Path))
			m.focusOrOpen(absPath)
		}
	case "d":
		m.openDiffView()
	case "l":
		m.openLogView()
	case "b":
		m.openBranchView()
	case "c":
		m.gitMode = gitModeCommit
		m.gitCommitIn = nil
		m.msg = ""
	case "r":
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
		m.msg = m.t("git.refreshed")
	case "a":
		r := m.repoForCur()
		if r == nil {
			break
		}
		for _, fs := range m.gitFiles {
			if !fs.IsStaged() {
				absPath := filepath.Join(r.Root, filepath.FromSlash(fs.Path))
				_ = r.Stage(absPath)
			}
		}
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
		m.msg = m.t("git.staged_all", len(m.gitFiles))
	case "i":
		if m.repoForCur() != nil {
			m.msg = m.t("git.already_repo")
			break
		}
		m.gitInit()
	}
	return nil
}

// gitInit initializes a git repository at the current file's directory.
func (m *Model) gitInit() {
	dir := m.root
	if dir == "" {
		t := m.cur()
		if t.path != "" {
			dir = filepath.Dir(t.path)
		}
	}
	if dir == "" {
		m.msg = m.t("git.no_dir")
		return
	}
	repo, err := vcs.Init(dir)
	if err != nil {
		m.msg = m.t("git.init_failed", err.Error())
		return
	}
	m.repo = repo
	m.msg = m.t("git.init_ok", repo.Root)
	m.refreshGitFiles()
	m.refreshGitDiffPreview()
}

// --- Commit mode ---

func (m *Model) handleGitCommit(msg tea.KeyPressMsg) tea.Cmd {
	r := m.repoForCur()
	switch msg.String() {
	case "esc":
		m.gitMode = gitModeStatus
		m.gitCommitIn = nil
		m.msg = ""
	case "enter":
		if len(m.gitCommitIn) == 0 {
			break
		}
		if r == nil {
			m.msg = m.t("git.norepo_msg")
			break
		}
		hash, err := r.Commit(string(m.gitCommitIn))
		if err != nil {
			m.msg = m.t("git.commit_failed", err.Error())
		} else {
			m.msg = m.t("git.committed", hash.String()[:7])
			m.gitCommitIn = nil
			m.gitMode = gitModeStatus
			m.gitOpen = false
			for i := range m.tabs {
				m.tabs[i].diffText = ""
			}
			m.refreshGitFiles()
		}
	case "backspace":
		if n := len(m.gitCommitIn); n > 0 {
			m.gitCommitIn = m.gitCommitIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.gitCommitIn = append(m.gitCommitIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// --- Log mode ---

func (m *Model) handleGitLog(msg tea.KeyPressMsg) tea.Cmd {
	key := gitKeyName(msg)
	keyL := strings.ToLower(key)

	// Global keys
	switch keyL {
	case "esc", "ctrl+g":
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	case "q":
		if m.gitDiffFocused {
			m.gitDiffFocused = false
			return nil
		}
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	}

	// Diff area has focus — scroll
	if m.gitDiffFocused {
		h := m.viewHeight()
		switch key {
		case "left", "tab":
			m.gitDiffFocused = false
			return nil
		case "up", "k":
			m.diffOffsetY--
		case "down", "j":
			m.diffOffsetY++
		case "pgup":
			m.diffOffsetY -= h
		case "pgdn":
			m.diffOffsetY += h
		case "home", "g":
			m.diffOffsetY = 0
		case "end", "G":
			m.diffOffsetY = len(m.diffRows)
		case "h":
			m.diffOffsetX -= 8
		case "right", "l":
			m.diffOffsetX += 8
		}
		m.clampDiffScroll(h)
		return nil
	}

	// Commit list has focus
	switch keyL {
	case "right", "tab":
		if len(m.diffRows) > 0 {
			m.gitDiffFocused = true
		}
		return nil
	case "up", "k":
		if m.gitLogSel > 0 {
			m.gitLogSel--
			m.clampGitLogScroll()
			m.showLogDiff()
		}
	case "down", "j":
		if m.gitLogSel < len(m.gitLogEntries)-1 {
			m.gitLogSel++
			m.clampGitLogScroll()
			m.showLogDiff()
		}
	case "pgup":
		m.gitLogSel -= m.viewHeight()
		if m.gitLogSel < 0 {
			m.gitLogSel = 0
		}
		m.clampGitLogScroll()
		m.showLogDiff()
	case "pgdn":
		m.gitLogSel += m.viewHeight()
		if m.gitLogSel >= len(m.gitLogEntries) {
			m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
		}
		m.clampGitLogScroll()
		m.showLogDiff()
	case "home":
		m.gitLogSel = 0
		m.clampGitLogScroll()
		m.showLogDiff()
	case "end":
		m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
		m.clampGitLogScroll()
		m.showLogDiff()
	case "r":
		m.refreshGitLog()
		m.showLogDiff()
		m.msg = m.t("git.refreshed")
	}
	return nil
}
