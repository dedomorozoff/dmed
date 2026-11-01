package sqlitedb

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestDB builds a throwaway database with the sample schema the tests
// operate on: a normal table, an INTEGER PRIMARY KEY table, a WITHOUT ROWID
// table and a view.
func newTestDB(t *testing.T) (d *DB, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INT, rate REAL, data BLOB)`,
		`INSERT INTO users VALUES (1, 'alex', 30, 2.5, NULL)`,
		`CREATE TABLE plain (label TEXT)`, // rowid exists, no declared PK
		`INSERT INTO plain (label) VALUES ('x')`,
		`CREATE TABLE keyed (code TEXT PRIMARY KEY, note TEXT) WITHOUT ROWID`,
		`INSERT INTO keyed VALUES ('a', 'note-a')`,
		`CREATE TABLE wide ("weird "" col" TEXT, "a-b" TEXT)`,
		`INSERT INTO wide VALUES ('v1', 'v2')`,
		`CREATE VIEW v_users AS SELECT name FROM users`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id, name, age, rate, data) VALUES (2, 'blob', 40, 0.0, ?)`, []byte{0xC3, 0x28, 0x00}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

func TestIsSQLite(t *testing.T) {
	_, path := newTestDB(t)
	if !IsSQLite(path) {
		t.Fatal("a created database must carry the magic header")
	}
	txt := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsSQLite(txt) {
		t.Fatal("plain text must not be detected as SQLite")
	}
}

func TestTablesListsUserObjects(t *testing.T) {
	d, _ := newTestDB(t)
	tables, err := d.Tables()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range tables {
		got[e.Name] = true
	}
	for _, want := range []string{"users", "plain", "keyed", "wide", "v_users"} {
		if !got[want] {
			t.Fatalf("table %q missing from listing: %v", want, got)
		}
	}
	for _, e := range tables {
		if strings.HasPrefix(e.Name, "sqlite_") {
			t.Fatalf("internal table leaked: %v", tables)
		}
	}
}

func TestColumnsAndRowID(t *testing.T) {
	d, _ := newTestDB(t)
	cols, err := d.Columns("users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 5 || cols[0].Name != "id" || !cols[0].PK {
		t.Fatalf("columns = %+v", cols)
	}
	if !d.HasRowID("users") {
		t.Fatal("users must have a rowid")
	}
	if d.HasRowID("keyed") {
		t.Fatal("WITHOUT ROWID table must report no rowid")
	}
	if d.HasRowID("v_users") {
		t.Fatal("a view must report no rowid")
	}
	wide, err := d.Columns("wide")
	if err != nil || len(wide) != 2 {
		t.Fatalf("wide columns = %+v err=%v", wide, err)
	}
}

func TestPageValuesAndReadonly(t *testing.T) {
	d, _ := newTestDB(t)
	rows, rids, err := d.Page("users", []string{"id", "name", "age", "rate", "data"}, "", -1, false, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rids) != 2 {
		t.Fatalf("rows=%d rids=%d", len(rows), len(rids))
	}
	u := rows[0]
	if u[0].Kind != "int" || u[0].Text != "1" {
		t.Fatalf("id cell = %+v", u[0])
	}
	if u[1].Kind != "text" || u[1].Text != "alex" {
		t.Fatalf("name cell = %+v", u[1])
	}
	if u[3].Kind != "float" {
		t.Fatalf("rate cell = %+v", u[3])
	}
	// row 1 (blob user) is the second row in natural order? ids: 1 then 2.
	if rows[1][4].Kind != "blob" || !strings.HasPrefix(rows[1][4].Text, "0x") {
		t.Fatalf("blob cell = %+v", rows[1][4])
	}

	// View: rowid-less, cells still readable.
	rows, rids, err = d.Page("v_users", []string{"name"}, "", -1, false, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rids != nil {
		t.Fatalf("view rows=%v rids=%v — must be readable but rowid-less", rows, rids)
	}
}

func TestPageOrderAndPaging(t *testing.T) {
	d, _ := newTestDB(t)
	rows, _, err := d.Page("users", []string{"name"}, "", 0, false, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0].Text != "alex" {
		t.Fatalf("first row by id asc = %v", rows)
	}
	desc, _, err := d.Page("users", []string{"id"}, "", 0, true, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if desc[0][0].Text != "2" {
		t.Fatalf("order desc id = %v", desc)
	}
	off, _, err := d.Page("users", []string{"id", "name", "age", "rate", "data"}, "", -1, false, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if off[0][0].Text != "2" {
		t.Fatalf("offset page = %v", off)
	}
}

func TestFilterMatchesAndEscapes(t *testing.T) {
	d, _ := newTestDB(t)
	n, err := d.Count("users", "alex")
	if err != nil || n != 1 {
		t.Fatalf("count = %d err=%v", n, err)
	}
	// '%' typed by the user must be literal, not a wildcard.
	n, err = d.Count("users", "%")
	if err != nil || n != 0 {
		t.Fatalf("raw %% must not match everything: %d err=%v", n, err)
	}
	// the filter must see numeric columns cast to text
	n, err = d.Count("users", "30")
	if err != nil || n != 1 {
		t.Fatalf("numeric filter count = %d err=%v", n, err)
	}
	// the same on the WITHOUT ROWID table
	n, err = d.Count("keyed", "note")
	if err != nil || n != 1 {
		t.Fatalf("keyed filter count = %d err=%v", n, err)
	}
}

func TestUpdateCellRoundtrip(t *testing.T) {
	d, _ := newTestDB(t)
	rows, rids, _ := d.Page("users", []string{"id", "name", "age", "rate", "data"}, "", -1, false, 0, 100)

	rid := rids[0]
	if err := d.UpdateCell("users", "name", rid, "Алекс"); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateCell("users", "age", rid, "31"); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateCell("users", "rate", rid, "9.5"); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateCell("users", "data", rid, ""); err != nil {
		t.Fatal(err) // empty edit = NULL
	}
	rows, rids, _ = d.Page("users", []string{"id", "name", "age", "rate", "data"}, "", -1, false, 0, 100)
	if rows[0][1].Text != "Алекс" {
		t.Fatalf("name after edit = %+v", rows[0][1])
	}
	if rows[0][2].Kind != "int" || rows[0][2].Text != "31" {
		t.Fatalf("age after edit = %+v (affinity must re-type digits)", rows[0][2])
	}
	if rows[0][3].Kind != "float" || rows[0][3].Text != "9.5" {
		t.Fatalf("rate after edit = %+v", rows[0][3])
	}
	if !rows[0][4].IsNull {
		t.Fatalf("empty edit must store NULL: %+v", rows[0][4])
	}

	// Stale rowid is an error, not a silent no-op.
	if err := d.UpdateCell("users", "name", "999999", "x"); err == nil {
		t.Fatal("stale rowid must fail")
	}
}

func TestInsertAndDeleteRow(t *testing.T) {
	d, _ := newTestDB(t)
	rid64, err := d.InsertRow("plain")
	if err != nil {
		t.Fatal(err)
	}
	rid := valueText(int64(rid64))
	rows, rids, _ := d.Page("plain", []string{"label"}, "", -1, false, 0, 100)
	found := false
	for i, r := range rids {
		if r == rid {
			// plain has no defaults at all: the inserted rowid exists, its
			// single column stays NULL.
			if !rows[i][0].IsNull {
				t.Fatalf("default insert must leave label NULL: %+v", rows[i])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("inserted row not listed")
	}
	if err := d.DeleteRow("plain", rid); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteRow("plain", rid); err == nil {
		t.Fatal("double delete must fail")
	}
	if n, _ := d.Count("plain", ""); n != 1 {
		t.Fatalf("count after delete = %d", n)
	}
}

func TestInsertWithoutNullDefaultFails(t *testing.T) {
	_, _ = newTestDB(t)
	path := filepath.Join(t.TempDir(), "strict.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE req (id INTEGER PRIMARY KEY AUTOINCREMENT, must TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if _, err := d2.InsertRow("req"); err == nil {
		t.Fatal("NOT NULL column without default must reject DEFAULT VALUES insert")
	}
}

func TestSchemaAndQuoteIdent(t *testing.T) {
	d, _ := newTestDB(t)
	s, err := d.Schema("users")
	if err != nil || !strings.Contains(s, "CREATE TABLE users") {
		t.Fatalf("schema = %q err=%v", s, err)
	}
	if got := quoteIdent(`we"i@rd`); got != `"we""i@rd"` {
		t.Fatalf("quoteIdent = %q", got)
	}
	// A quoted column in a query goes through as a real identifier.
	cols, err := d.Columns("wide")
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := d.Page("wide", []string{cols[0].Name, cols[1].Name}, "", -1, false, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0].Text != "v1" || rows[0][1].Text != "v2" {
		t.Fatalf("quoted columns round-trip failed: %v", rows)
	}
}

func TestCloseIdempotent(t *testing.T) {
	d, _ := newTestDB(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal("Close must stay idempotent")
	}
}
