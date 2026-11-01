package editor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	_ "modernc.org/sqlite"
)

// newSqliteFile builds a small throwaway database on disk: a normal table,
// a view, and two rows so the browser has something to page through.
func newSqliteFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INT)`,
		`INSERT INTO users VALUES (1, 'alex', 30)`,
		`INSERT INTO users VALUES (2, 'bob', 40)`,
		`CREATE VIEW v_users AS SELECT name FROM users`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	db.Close()
	return path
}

func openBrowser(t *testing.T) (Model, string) {
	t.Helper()
	path := newSqliteFile(t)
	m := New(path)
	m.width, m.height = 80, 24
	t.Cleanup(func() { m.sqlDB.Close() }) // release the file for TempDir cleanup
	if !m.sqlOpen {
		t.Fatal("a .sqlite file must open the browser")
	}
	return m, path
}

func TestOpenPathOpensSQLiteBrowser(t *testing.T) {
	m, _ := openBrowser(t)
	if len(m.sqlTables) != 2 {
		t.Fatalf("want 2 objects (table+view), got %d", len(m.sqlTables))
	}
	if m.sqlTableName() != "users" {
		t.Fatalf("default table = %q", m.sqlTableName())
	}
	if m.sqlReadonly() {
		t.Fatal("users has a rowid, must be editable")
	}
	if len(m.sqlRows) != 2 {
		t.Fatalf("page rows = %d", len(m.sqlRows))
	}
	// The browser must not silently create a text tab for the binary file.
	if len(m.tabs) != 1 || m.tabs[0].path != "" {
		t.Fatalf("browser open must not create tabs: %+v", m.tabs)
	}
	if len(m.sqlCols) != 3 {
		t.Fatalf("columns = %v", m.sqlColNames())
	}
}

func TestNonSQLiteDotDBStaysText(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "fake.db", "just text\n")
	m := New(path)
	if m.sqlOpen {
		t.Fatal("a .db file without the magic header must not open the browser")
	}
	if len(m.tabs) != 1 || m.tabs[0].path != path {
		t.Fatal("non-sqlite .db must fall back to a text tab")
	}
}

func TestSqliteGridRendersWithinWidth(t *testing.T) {
	m, _ := openBrowser(t)
	rows := plainRows(m.View().Content)
	foundCell := false
	for _, row := range rows {
		if w := len([]rune(row)); w > m.width {
			t.Fatalf("grid row wider than the screen: %q (%d > %d)", row, w, m.width)
		}
		if strings.Contains(row, "alex") {
			foundCell = true
		}
	}
	if !foundCell {
		t.Fatal("page values must render in the grid")
	}
	if !strings.Contains(strings.Join(rows, "\n"), "users") {
		t.Fatal("the table strip must name the selected table")
	}
}

func TestSqliteNavigation(t *testing.T) {
	m, _ := openBrowser(t)
	if m.sqlRow != 0 || m.sqlCol != 0 {
		t.Fatalf("cursor starts at %d,%d", m.sqlRow, m.sqlCol)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.sqlRow != 1 {
		t.Fatalf("down: row = %d", m.sqlRow)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.sqlCol != 1 {
		t.Fatalf("right: col = %d", m.sqlCol)
	}
	// Walking past the last column clamps there.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.sqlCol != 2 {
		t.Fatalf("right clamp: col = %d", m.sqlCol)
	}
	// Up past the first row goes to the previous page and bounces off.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.sqlRow != 0 {
		t.Fatalf("up at top: row = %d", m.sqlRow)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.sqlRow != 1 || m.sqlOff != 0 {
		t.Fatalf("down at end: row = %d off = %d", m.sqlRow, m.sqlOff)
	}
	// Esc closes the browser.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.sqlOpen {
		t.Fatal("esc must close the browser")
	}
}

func TestSqliteEditCell(t *testing.T) {
	m, path := openBrowser(t)
	// Cursor onto the name column of row 1 and edit it.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sqlEdit == nil {
		t.Fatal("enter must open the cell editor")
	}
	if string(m.sqlEdit.buf) != "alex" {
		t.Fatalf("editor prefills the old value, buf = %q", string(m.sqlEdit.buf))
	}
	for range 4 { // clear "alex" fully
		m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m = typeStr(m, "llama")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sqlEdit != nil {
		t.Fatal("enter must close the editor")
	}
	// Check it really reached the file.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow(`SELECT name FROM users WHERE id = 1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "llama" {
		t.Fatalf("edit did not reach the file: name = %q", name)
	}
	// Empty edit stores NULL.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sqlEdit == nil {
		t.Fatal("enter must open the cell editor again")
	}
	for range 5 {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	var nullName sql.NullString
	if err := db.QueryRow(`SELECT name FROM users WHERE id = 1`).Scan(&nullName); err != nil {
		t.Fatal(err)
	}
	if nullName.Valid || nullName.String != "" {
		t.Fatalf("empty edit must store NULL: %q", nullName.String)
	}
}

func TestSqliteInsertDeleteRow(t *testing.T) {
	m, path := openBrowser(t)
	before := m.sqlTotal
	m = press(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if m.sqlTotal != before+1 {
		t.Fatalf("insert: total %d -> %d", before, m.sqlTotal)
	}
	// An all-defaults row of `users` lands at the end in natural order.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.sqlTotal != before {
		t.Fatalf("delete: total = %d", m.sqlTotal)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before {
		t.Fatalf("db count after insert+delete = %d", n)
	}
}

func TestSqliteFilterAndOrder(t *testing.T) {
	m, _ := openBrowser(t)
	m = press(m, tea.KeyPressMsg{Code: '/'})
	if !m.sqlFilterIn {
		t.Fatal("'/' must open the filter prompt")
	}
	m = typeStr(m, "bob")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sqlFilter != "bob" {
		t.Fatalf("filter = %q", m.sqlFilter)
	}
	if m.sqlTotal != 1 {
		t.Fatalf("filtered total = %d", m.sqlTotal)
	}
	m = press(m, tea.KeyPressMsg{Code: 'o'})
	if m.sqlOrder != 0 || m.sqlDesc {
		t.Fatalf("'o' must order by the cursor column asc: order=%d desc=%v", m.sqlOrder, m.sqlDesc)
	}
	m = press(m, tea.KeyPressMsg{Code: 'o'})
	if !m.sqlDesc {
		t.Fatal("second 'o' must flip to desc")
	}
	m = press(m, tea.KeyPressMsg{Code: 'o'})
	if m.sqlOrder != -1 {
		t.Fatal("third 'o' must clear the order")
	}
	// The grid shows the filtered row only.
	rows := plainRows(m.View().Content)
	if !strings.Contains(strings.Join(rows, "\n"), "bob") || strings.Contains(strings.Join(rows, "\n"), "alex") {
		t.Fatal("the filter must hide non-matching rows from the grid")
	}
}

func TestSqliteTableViewIsReadonly(t *testing.T) {
	m, _ := openBrowser(t)
	// Tab cycles users → v_users
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.sqlTableName() != "v_users" {
		t.Fatalf("tab must move to the view, table = %q", m.sqlTableName())
	}
	if !m.sqlReadonly() {
		t.Fatal("a view must be read-only")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sqlEdit != nil {
		t.Fatal("enter on a read-only object must not open the editor")
	}
	if !strings.Contains(m.msg, "read-only") {
		t.Fatalf("read-only must explain itself: %q", m.msg)
	}
}

func TestSqliteSchemaView(t *testing.T) {
	m, _ := openBrowser(t)
	m = press(m, tea.KeyPressMsg{Code: 's'})
	if !m.sqlSchema {
		t.Fatal("'s' must open the schema view")
	}
	rows := plainRows(m.View().Content)
	if !strings.Contains(strings.Join(rows, "\n"), "CREATE TABLE users") {
		t.Fatal("the schema view must show the CREATE statement")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.sqlSchema {
		t.Fatal("esc must return to the grid")
	}
	if !strings.Contains(strings.Join(plainRows(m.View().Content), "\n"), "alex") {
		t.Fatal("back to the grid the data must return")
	}
}

func TestSqliteBrowserNotOpenedFromTextSession(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "note.txt", "hello\n")
	m := New(path)
	// A session with normal files stays a plain editor.
	if m.sqlOpen || !strings.Contains(m.activeTab().buf.Text(), "hello") {
		t.Fatal("a text file must not be hijacked by the browser")
	}
	// The saved SQLite file can still be opened on top of it via openPath.
	sp := newSqliteFile(t)
	m.openPath(sp)
	if !m.sqlOpen {
		t.Fatal("openPath must switch to the browser for a SQLite file")
	}
	m.sqlDB.Close() // release the file for TempDir cleanup
	_ = os.Remove(path)
}
