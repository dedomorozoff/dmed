package editor

// The SQLite browser is a full-screen view (like the diff view) opened
// whenever a .db/.sqlite file is opened: openPath notices the extension,
// verifies the file magic and hands the path here instead of loading binary
// garbage into a buffer.
//
// Edits are immediate — one parameterized UPDATE/INSERT/DELETE per action
// through internal/sqlitedb. The diff-review/Applier machinery stays
// untouched: it governs agent-produced text changes, not human browser
// actions on a database file.

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/sqlitedb"
)

// sqlPageSize is how many rows the browser loads per page.
const sqlPageSize = 50

// maxSqlColWidth caps a grid column so one wide cell cannot eat the screen.
const maxSqlColWidth = 40

var (
	sqlSelStyle  = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("255"))
	sqlFaintStyl = lipgloss.NewStyle().Faint(true)
	sqlHeadStyl  = lipgloss.NewStyle().Bold(true)
	sqlTblSelSty = lipgloss.NewStyle().Bold(true).Underline(true)
	sqlTblSty    = lipgloss.NewStyle().Faint(true)
)

// sqlCellEdit is the state of the in-place cell editor; nil on the model
// means no edit is in progress.
type sqlCellEdit struct {
	row, col int // page coordinates the edit was started from
	buf      []rune
}

// sqlitePathCandidate reports whether the file extension suggests a SQLite
// database. The extension only earns a peek at the file; sqlitedb.IsSQLite
// decides with the magic header.
func sqlitePathCandidate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sqlite", ".sqlite3", ".db", ".db3":
		return true
	}
	return false
}

// tryOpenSQLite opens the browser for a verified SQLite file. On any failure
// it returns an error and leaves the previous browser (if any) intact, so
// the caller can fall back to a plain text open.
func (m *Model) tryOpenSQLite(path string) error {
	if !sqlitedb.IsSQLite(path) {
		return fmt.Errorf("not a SQLite file")
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return err
	}
	tables, err := db.Tables()
	if err != nil {
		db.Close()
		return err
	}
	// One browser at a time: opening a second database releases the first.
	if m.sqlDB != nil {
		m.sqlDB.Close()
	}
	m.sqlDB = db
	m.sqlPath = path
	m.sqlOpen = true
	m.sqlLim = sqlPageSize
	m.sqlTables = tables
	m.sqlTbl = 0
	m.loadSqliteTable()
	return nil
}

// closeSQLite shuts the browser down and releases the connection.
func (m *Model) closeSQLite() {
	if m.sqlDB != nil {
		m.sqlDB.Close()
	}
	m.sqlDB = nil
	m.sqlOpen = false
	m.msg = m.t("msg.sqlite_closed", filepath.Base(m.sqlPath))
	m.sqlPath = ""
	m.sqlTables = nil
	m.sqlCols = nil
	m.sqlRows = nil
	m.sqlRids = nil
	m.sqlEdit = nil
	m.sqlFilter = ""
	m.sqlSchema = false
	m.sqlSchemaText = ""
}

// sqlTableName is the name of the table (or view) currently shown.
func (m Model) sqlTableName() string {
	if m.sqlTbl >= 0 && m.sqlTbl < len(m.sqlTables) {
		return m.sqlTables[m.sqlTbl].Name
	}
	return ""
}

// sqlReadonly reports whether the shown object cannot be edited: a view or
// a WITHOUT ROWID table (no rowid means no stable row address).
func (m Model) sqlReadonly() bool {
	return m.sqlDB == nil || !m.sqlHasRid
}

// sqlColNames flattens the metadata into the plain name list the data layer
// takes.
func (m Model) sqlColNames() []string {
	names := make([]string, len(m.sqlCols))
	for i, c := range m.sqlCols {
		names[i] = c.Name
	}
	return names
}

// loadSqliteTable switches the browser to the selected table: fresh filter,
// order and cursor, then a full page load.
func (m *Model) loadSqliteTable() {
	m.sqlOrder = -1
	m.sqlDesc = false
	m.sqlOff = 0
	m.sqlRow = 0
	m.sqlCol = 0
	m.sqlOffX = 0
	m.sqlFilter = ""
	m.sqlFilterBuf = nil
	m.sqlFilterIn = false
	m.sqlEdit = nil
	m.sqlSchema = false
	m.sqlSchemaOffY = 0
	m.sqlRows = nil
	m.sqlRids = nil
	m.sqlTotal = 0
	name := m.sqlTableName()
	if m.sqlDB == nil || name == "" {
		m.sqlCols = nil
		return
	}
	if m.sqlLim <= 0 {
		m.sqlLim = sqlPageSize
	}
	cols, err := m.sqlDB.Columns(name)
	if err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		m.sqlCols = nil
		return
	}
	m.sqlCols = cols
	m.sqlHasRid = m.sqlDB.HasRowID(name)
	if m.sqlHasRid {
		// Column cursor one-past-the-end is easiest shifted by the rowid
		// gutter presence, so keep the cursor inside the data columns.
		if m.sqlCol >= len(cols) {
			m.sqlCol = len(cols) - 1
		}
	}
	m.sqliteRefresh()
	m.loadSqliteSchema()
}

// loadSqliteSchema caches the CREATE statement for the schema view.
func (m *Model) loadSqliteSchema() {
	if m.sqlDB == nil {
		m.sqlSchemaText = ""
		return
	}
	if s, err := m.sqlDB.Schema(m.sqlTableName()); err == nil {
		m.sqlSchemaText = s
	} else {
		m.sqlSchemaText = ""
	}
}

// sqliteRefresh reloads the count and the current page of the shown table.
func (m *Model) sqliteRefresh() {
	if m.sqlDB == nil {
		return
	}
	name := m.sqlTableName()
	total, err := m.sqlDB.Count(name, m.sqlFilter)
	if err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		return
	}
	m.sqlTotal = total
	rows, rids, err := m.sqlDB.Page(name, m.sqlColNames(), m.sqlFilter, m.sqlOrder, m.sqlDesc, m.sqlOff, m.sqlLim)
	if err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		m.sqlRows, m.sqlRids = nil, nil
		return
	}
	m.sqlRows = rows
	m.sqlRids = rids
	if m.sqlOff >= m.sqlLim && m.sqlOff >= total {
		// The page slid past the end (rows deleted beneath the cursor).
		m.sqlOff = max(0, (total-1)/m.sqlLim*m.sqlLim)
		rows, rids, err = m.sqlDB.Page(name, m.sqlColNames(), m.sqlFilter, m.sqlOrder, m.sqlDesc, m.sqlOff, m.sqlLim)
		if err == nil {
			m.sqlRows, m.sqlRids = rows, rids
		}
	}
	m.clampSqliteCursor()
	m.clampSqliteOffX()
}

// sqliteGoto loads the page containing the absolute row index and places the
// cursor on it, keeping the current column.
func (m *Model) sqliteGoto(abs int) {
	if m.sqlTotal == 0 {
		m.sqlRow, m.sqlOff = 0, 0
		m.sqliteRefresh()
		return
	}
	abs = min(max(0, abs), m.sqlTotal-1)
	m.sqlOff = abs / m.sqlLim * m.sqlLim
	m.sqliteRefresh()
	m.sqlRow = abs - m.sqlOff
	if m.sqlRow >= len(m.sqlRows) {
		m.sqlRow = max(0, len(m.sqlRows)-1)
	}
	m.clampSqliteOffX()
}

// sqliteMove shifts the cell cursor; vertical moves that leave the loaded
// page fetch the neighbouring page. Horizontal moves stay within the page.
func (m *Model) sqliteMove(dr, dc int) {
	if dc != 0 && len(m.sqlCols) > 0 {
		m.sqlCol = min(max(0, m.sqlCol+dc), len(m.sqlCols)-1)
		m.clampSqliteOffX()
	}
	if dr == 0 {
		return
	}
	if m.sqlTotal == 0 {
		return
	}
	abs := m.sqlOff + m.sqlRow + dr
	if abs < 0 || abs >= m.sqlTotal {
		return
	}
	if abs >= m.sqlOff && abs < m.sqlOff+len(m.sqlRows) {
		m.sqlRow = abs - m.sqlOff
		m.clampSqliteOffX()
		return
	}
	m.sqliteGoto(abs)
}

// clampSqliteCursor keeps row/col inside the loaded page and metadata.
func (m *Model) clampSqliteCursor() {
	if m.sqlRow >= len(m.sqlRows) {
		m.sqlRow = max(0, len(m.sqlRows)-1)
	}
	if m.sqlRow < 0 {
		m.sqlRow = 0
	}
	if m.sqlCol >= len(m.sqlCols) {
		m.sqlCol = max(0, len(m.sqlCols)-1)
	}
	if m.sqlCol < 0 {
		m.sqlCol = 0
	}
}

// sqlColWidths computes the display width of every grid column: optional
// rowid gutter first, then the data columns (header text — sort mark
// included — vs page cells, capped).
func (m Model) sqlColWidths() []int {
	rid := 0
	if m.sqlHasRid {
		rid = 3
		for _, r := range m.sqlRids {
			rid = max(rid, lipgloss.Width(r))
		}
	}
	names := m.sqlColNames()
	ws := make([]int, len(names))
	for i, n := range names {
		w := lipgloss.Width(n)
		if i == m.sqlOrder {
			w += 2
		}
		ws[i] = min(w, maxSqlColWidth)
	}
	for _, row := range m.sqlRows {
		for i, v := range row {
			w := lipgloss.Width(sqlCellText(v))
			if w > maxSqlColWidth {
				w = maxSqlColWidth
			}
			if w > ws[i] {
				ws[i] = w
			}
		}
	}
	if m.sqlHasRid {
		ws = append([]int{rid}, ws...)
	}
	return ws
}

// sqlColOffset returns the rune offset where data column i starts, plus its
// width; the optional rowid gutter shifts the data columns.
// Offsets align with sqlGridRow output: "w + 3" per column for the " | "
// separator.
func (m Model) sqlColOffset(i int) (start, width, total int) {
	ws := m.sqlColWidths()
	total = 0
	for j, w := range ws {
		if j < len(ws)-1 {
			total += w + 3
		} else {
			total += w
		}
		if j == i {
			return total - w - 3 + w, w, total
		}
	}
	return 0, 0, total
}

// clampSqliteOffX scrolls horizontally so the cursor cell is visible.
func (m *Model) clampSqliteOffX() {
	ws := m.sqlColWidths()
	if len(ws) == 0 {
		m.sqlOffX = 0
		return
	}
	// Data column i lives at gutter offset + i in the width list.
	i := m.sqlCol
	if m.sqlHasRid {
		i++
	}
	start, width, total := m.sqlColOffset(i)
	w := m.width
	m.sqlOffX = min(max(0, m.sqlOffX), total-1)
	if width >= w {
		m.sqlOffX = start
		return
	}
	if m.sqlOffX > start {
		m.sqlOffX = start
	}
	if m.sqlOffX+w < start+width {
		m.sqlOffX = max(0, start+width-w)
	}
}

// sqlCellText renders one cell for the grid; NULL becomes a faint NULL and
// embedded newlines are flattened so a row always stays one line tall.
func sqlCellText(v sqlitedb.Value) string {
	if v.IsNull {
		return "NULL"
	}
	t := v.Text
	t = strings.ReplaceAll(t, "\r\n", "\\n")
	t = strings.ReplaceAll(t, "\n", "\\n")
	return t
}

// sqlSeg is one styled run of a grid row.
type sqlSeg struct {
	text string
	kind int // 0 plain, 1 faint, 2 selected, 3 header
}

// truncateCell cuts text to w runes with an ellipsis; w ≤ 0 means empty.
func truncateCell(text string, w int) string {
	r := []rune(text)
	if w <= 0 {
		return ""
	}
	if len(r) <= w {
		return text
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// padCell right-pads text with spaces to w display width (rune count).
func padCell(text string, w int) string {
	r := []rune(text)
	if len(r) >= w {
		return string(r)
	}
	return text + strings.Repeat(" ", w-len(r))
}

// sqlGridRow renders one grid line: rowIdx -1 draws the header. Runs carry
// their style so NULL cells and the selected cell can be painted distinctly
// while the padded layout stays aligned.
func (m Model) sqlGridRow(rowIdx int) string {
	ws := m.sqlColWidths()
	var segs []sqlSeg

	if m.sqlHasRid {
		ridText := ""
		if rowIdx >= 0 && rowIdx < len(m.sqlRids) {
			ridText = m.sqlRids[rowIdx]
		} else if rowIdx < 0 {
			ridText = m.t("sqlite.rowid_col")
		}
		segs = append(segs, sqlSeg{text: truncateCell(ridText, ws[0]), kind: 1})
	}
	ridOff := 0
	if m.sqlHasRid {
		ridOff = 1
	}
	for i, c := range m.sqlCols {
		w := ws[i+ridOff]
		segs = append(segs, sqlSeg{text: " | ", kind: 1})
		kind := 0
		text := ""
		if rowIdx < 0 {
			text = c.Name
			if i == m.sqlOrder {
				if m.sqlDesc {
					text += " ↓"
				} else {
					text += " ↑"
				}
			}
			kind = 3
		} else if rowIdx < len(m.sqlRows) {
			v := m.sqlRows[rowIdx][i]
			text = sqlCellText(v)
			if v.IsNull || v.Kind == "blob" {
				kind = 1
			}
		}
		text = truncateCell(text, w)
		if rowIdx == m.sqlRow && i == m.sqlCol && rowIdx >= 0 {
			kind = 2
		}
		segs = append(segs, sqlSeg{text: padCell(text, w), kind: kind})
	}
	return m.sqlPaintSegs(segs)
}

// sqlPaintSegs flattens styled segments into one printable line clipped to
// the content window [sqlOffX, sqlOffX+width).
func (m Model) sqlPaintSegs(segs []sqlSeg) string {
	type styledRune struct {
		r rune
		k int
	}
	var rs []styledRune
	for _, seg := range segs {
		for _, r := range seg.text {
			rs = append(rs, styledRune{r: r, k: seg.kind})
		}
	}
	lo := m.sqlOffX
	if lo > len(rs) {
		lo = len(rs)
	}
	hi := m.sqlOffX + m.width
	if hi > len(rs) {
		hi = len(rs)
	}
	rs = rs[lo:hi]
	var b strings.Builder
	i := 0
	for i < len(rs) {
		j := i
		for j < len(rs) && rs[j].k == rs[i].k {
			j++
		}
		runes := make([]rune, 0, j-i)
		for _, r := range rs[i:j] {
			runes = append(runes, r.r)
		}
		switch rs[i].k {
		case 1:
			b.WriteString(sqlFaintStyl.Render(string(runes)))
		case 2:
			b.WriteString(sqlSelStyle.Render(string(runes)))
		case 3:
			b.WriteString(sqlHeadStyl.Render(string(runes)))
		default:
			b.WriteString(string(runes))
		}
		i = j
	}
	return b.String()
}

// sqliteTableStrip renders the top line: every object of the database, the
// selected one bold; views carry a suffix. The strip scrolls horizontally so
// the selected name stays visible.
func (m Model) sqliteTableStrip() string {
	type item struct {
		text string
		sel  bool
	}
	items := make([]item, 0, len(m.sqlTables))
	for i, t := range m.sqlTables {
		name := t.Name
		if t.Kind == "view" {
			name += " (view)"
		}
		items = append(items, item{text: " " + name + " ", sel: i == m.sqlTbl})
	}
	// Window so the selected item is on screen.
	width := m.width
	start := 0
	if m.sqlTbl >= 0 {
		start = m.sqlTbl
		for start > 0 {
			w := lipgloss.Width(items[start].text)
			if w >= width {
				break
			}
			width -= w
			start--
		}
	}
	var b strings.Builder
	avail := m.width
	for i := start; i < len(items) && avail > 0; i++ {
		text := items[i].text
		if lipgloss.Width(text) > avail {
			text = truncateCell(text, avail)
		}
		if items[i].sel {
			b.WriteString(sqlTblSelSty.Render(text))
		} else {
			b.WriteString(sqlTblSty.Render(text))
		}
		avail -= lipgloss.Width(text)
	}
	return b.String()
}

// sqliteSchemaRows renders the CREATE statement of the shown object.
func (m Model) sqliteSchemaRows(h int) []string {
	rows := make([]string, 0, h)
	title := m.t("sqlite.schema_title", m.sqlTableName())
	rows = append(rows, sqlHeadStyl.Render(" "+truncateCell(title, max(0, m.width-1))))
	lines := strings.Split(strings.ReplaceAll(m.sqlSchemaText, "\r", ""), "\n")
	maxStart := max(0, len(lines)-(h-1))
	off := min(max(0, m.sqlSchemaOffY), maxStart)
	for i := off; i < len(lines) && len(rows) < h; i++ {
		rows = append(rows, truncateCell(lines[i], m.width))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	rows[0] = sqlHeadStyl.Render(" " + truncateCell(m.t("sqlite.schema_title", m.sqlTableName()), max(0, m.width-1)))
	return rows
}

// sqliteViewRows renders the whole browser content area (h rows).
func (m Model) sqliteViewRows(h int) []string {
	rows := make([]string, 0, h)
	if m.sqlSchema {
		return m.sqliteSchemaRows(h)
	}
	rows = append(rows, m.sqliteTableStrip())
	if m.sqlDB == nil || len(m.sqlTables) == 0 {
		rows = append(rows, sqlFaintStyl.Render(" "+m.t("sqlite.no_tables")))
		for len(rows) < h {
			rows = append(rows, "")
		}
		return rows
	}
	rows = append(rows, m.sqlGridRow(-1))
	dataRows := min(h-2, len(m.sqlRows))
	for i := 0; i < dataRows; i++ {
		rows = append(rows, m.sqlGridRow(i))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

// sqlBottom is the summary/context line under the grid.
func (m Model) sqlBottom() string {
	name := m.sqlTableName()
	rows := m.t("sqlite.rows", m.sqlOff+1, m.sqlOff+len(m.sqlRows), m.sqlTotal)
	if m.sqlTotal == 0 {
		rows = m.t("sqlite.rows_empty")
	}
	hint := m.t("sqlite.keys")
	if m.sqlReadonly() {
		hint = m.t("sqlite.keys_ro")
	}
	var line string
	if m.sqlFilter != "" {
		line = fmt.Sprintf("%s · %s · %s · /%s · %s", filepath.Base(m.sqlPath), name, rows, m.sqlFilter, hint)
	} else {
		line = fmt.Sprintf("%s · %s · %s · %s", filepath.Base(m.sqlPath), name, rows, hint)
	}
	return truncateCell(line, m.width)
}

// sqlEditLine is the bottom line while a cell is being edited.
func (m Model) sqlEditLine() string {
	e := m.sqlEdit
	if e == nil || e.col >= len(m.sqlCols) {
		return ""
	}
	label := m.t("sqlite.edit_col", m.sqlCols[e.col].Name)
	hint := "  " + m.t("sqlite.edit_hint")
	return truncateCell(label+string(e.buf)+"▌"+hint, m.width)
}

// sqlFilterLine is the bottom line while the filter is being typed.
func (m Model) sqlFilterLine() string {
	return truncateCell("/ "+string(m.sqlFilterBuf)+"▌  "+m.t("sqlite.filter_hint"), m.width)
}

// handleSqlite owns every key while the browser is open.
func (m *Model) handleSqlite(msg tea.KeyPressMsg) tea.Cmd {
	if m.sqlEdit != nil {
		return m.handleSqliteEdit(msg)
	}
	if m.sqlFilterIn {
		return m.handleSqliteFilterInput(msg)
	}
	if m.sqlSchema {
		// The DDL view scrolls on its own; 's'/esc returns to the grid.
		switch msg.String() {
		case "esc", "s", "q":
			m.sqlSchema = false
		case "up", "k":
			m.sqlSchemaOffY--
		case "down", "j":
			m.sqlSchemaOffY++
		case "pgup":
			m.sqlSchemaOffY -= m.viewHeight()
		case "pgdown":
			m.sqlSchemaOffY += m.viewHeight()
		default:
			return nil
		}
		maxStart := len(strings.Split(m.sqlSchemaText, "\n")) - m.viewHeight()
		m.sqlSchemaOffY = min(max(0, m.sqlSchemaOffY), max(0, maxStart))
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		m.closeSQLite()
	case "up", "k":
		m.sqliteMove(-1, 0)
	case "down", "j":
		m.sqliteMove(1, 0)
	case "left", "h":
		m.sqliteMove(0, -1)
	case "right", "l":
		m.sqliteMove(0, 1)
	case "pgup":
		m.sqliteGoto(m.sqlOff - m.sqlLim)
	case "pgdown":
		m.sqliteGoto(m.sqlOff + m.sqlLim)
	case "home", "g":
		m.sqliteGoto(0)
	case "end", "G":
		m.sqliteGoto(m.sqlTotal - 1)
	case "enter":
		m.beginSqliteCellEdit()
	case "ctrl+n":
		m.sqliteInsertRow()
	case "ctrl+d":
		m.sqliteDeleteRow()
	case "o":
		m.sqliteToggleOrder()
	case "/":
		m.sqlFilterIn = true
		m.sqlFilterBuf = []rune(m.sqlFilter)
	case "tab":
		m.sqliteCycleTable(1)
	case "shift+tab":
		m.sqliteCycleTable(-1)
	case "s":
		if m.sqlSchemaText != "" {
			m.sqlSchema = true
			m.sqlSchemaOffY = 0
		}
	default:
		return nil
	}
	return nil
}

// handleSqliteEdit drives the in-place cell editor.
func (m *Model) handleSqliteEdit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.commitSqliteCellEdit()
	case "esc", "ctrl+c":
		m.sqlEdit = nil
	case "backspace":
		if r := m.sqlEdit.buf; len(r) > 0 {
			m.sqlEdit.buf = r[:len(r)-1]
		}
	default:
		if msg.Text != "" {
			m.sqlEdit.buf = append(m.sqlEdit.buf, []rune(msg.Text)...)
		}
	}
	return nil
}

// handleSqliteFilterInput drives the filter prompt.
func (m *Model) handleSqliteFilterInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.sqlFilter = string(m.sqlFilterBuf)
		m.sqlFilterIn = false
		m.sqliteGoto(0)
	case "esc":
		m.sqlFilterIn = false
	case "backspace":
		if r := m.sqlFilterBuf; len(r) > 0 {
			m.sqlFilterBuf = r[:len(r)-1]
		}
	default:
		if msg.Text != "" {
			m.sqlFilterBuf = append(m.sqlFilterBuf, []rune(msg.Text)...)
		}
	}
	return nil
}

// beginSqliteCellEdit starts editing the cursor cell.
func (m *Model) beginSqliteCellEdit() {
	if m.sqlReadonly() {
		m.msg = m.t("sqlite.readonly")
		return
	}
	if m.sqlRow < 0 || m.sqlRow >= len(m.sqlRows) || m.sqlCol < 0 || m.sqlCol >= len(m.sqlCols) {
		return
	}
	v := m.sqlRows[m.sqlRow][m.sqlCol]
	if v.Kind == "blob" {
		m.msg = m.t("sqlite.blob_ro")
		return
	}
	e := &sqlCellEdit{row: m.sqlRow, col: m.sqlCol}
	if !v.IsNull {
		e.buf = []rune(v.Text)
	}
	m.sqlEdit = e
}

// commitSqliteCellEdit writes the edited value back with a bound UPDATE.
func (m *Model) commitSqliteCellEdit() {
	e := m.sqlEdit
	m.sqlEdit = nil
	if e == nil || m.sqlDB == nil || !m.sqlHasRid {
		return
	}
	if e.row >= len(m.sqlRids) || e.col >= len(m.sqlCols) {
		return
	}
	col := m.sqlCols[e.col].Name
	if err := m.sqlDB.UpdateCell(m.sqlTableName(), col, m.sqlRids[e.row], string(e.buf)); err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		return
	}
	m.msg = m.t("sqlite.saved", col)
	m.sqliteRefresh()
}

// sqliteInsertRow adds an all-defaults row and moves the cursor to it when
// it lands inside the current view (natural order, no filter).
func (m *Model) sqliteInsertRow() {
	if m.sqlReadonly() {
		m.msg = m.t("sqlite.readonly")
		return
	}
	rid, err := m.sqlDB.InsertRow(m.sqlTableName())
	if err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		return
	}
	m.msg = m.t("sqlite.inserted")
	ridText := fmt.Sprintf("%d", rid)
	m.sqliteRefresh()
	for i, r := range m.sqlRids {
		if r == ridText {
			m.sqlRow = i
			return
		}
	}
	if m.sqlOrder == -1 && m.sqlFilter == "" && m.sqlTotal > 0 {
		m.sqliteGoto(m.sqlTotal - 1)
	}
}

// sqliteDeleteRow removes the cursor row.
func (m *Model) sqliteDeleteRow() {
	if m.sqlReadonly() {
		m.msg = m.t("sqlite.readonly")
		return
	}
	if m.sqlRow < 0 || m.sqlRow >= len(m.sqlRids) {
		return
	}
	if err := m.sqlDB.DeleteRow(m.sqlTableName(), m.sqlRids[m.sqlRow]); err != nil {
		m.msg = m.t("sqlite.err", err.Error())
		return
	}
	m.msg = m.t("sqlite.deleted")
	m.sqliteRefresh()
}

// sqliteToggleOrder cycles the cursor column's sort: none → asc → desc → none.
func (m *Model) sqliteToggleOrder() {
	if len(m.sqlCols) == 0 {
		return
	}
	if m.sqlOrder == m.sqlCol {
		if !m.sqlDesc {
			m.sqlDesc = true
		} else {
			m.sqlOrder = -1
			m.sqlDesc = false
		}
	} else {
		m.sqlOrder = m.sqlCol
		m.sqlDesc = false
	}
	m.sqliteGoto(0)
}

// sqliteCycleTable selects the next/previous table (or view).
func (m *Model) sqliteCycleTable(d int) {
	n := len(m.sqlTables)
	if n == 0 {
		return
	}
	m.sqlTbl = ((m.sqlTbl+d)%n + n) % n
	m.loadSqliteTable()
}
