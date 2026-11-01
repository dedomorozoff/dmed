// Package sqlitedb is the TUI-free data layer behind the editor's SQLite
// browser. It wraps database/sql on top of modernc.org/sqlite — a pure-Go
// SQLite implementation — so opening a .db file never drags in CGO and the
// 8-platform cross build stays green.
//
// Every table/column/rowid name coming out of the database is passed through
// quoteIdent (double-quoted, ""-escaped); every value supplied by the user
// goes in as a bound parameter. Editing is immediate: the browser issues one
// UPDATE/INSERT/DELETE per action, exactly like the sqlite3 command line —
// there is no dirty-buffer state to save.
package sqlitedb

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// sqliteMagic is the first 16 bytes of every SQLite 3 database file.
const sqliteMagic = "SQLite format 3\x00"

// IsSQLite reports whether the file starts with the SQLite 3 magic header —
// the only reliable way to tell a database from an equally-named binary.
func IsSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == sqliteMagic
}

// Entry is one object listed in the browser: a table or a view.
type Entry struct {
	Name string
	Kind string // "table" or "view"
}

// Column describes one column of a table.
type Column struct {
	Name string
	Type string // declared affinity, may be empty
	PK   bool
}

// Value is one scanned cell in its renderable form.
type Value struct {
	IsNull bool
	Kind   string // "null", "int", "float", "text" or "blob"
	Text   string
}

// DB is an open SQLite file. It holds one connection (single connection
// avoids SQLITE_BUSY interplay between database/sql pool connections) and is
// only ever touched from the UI thread by the browser.
type DB struct {
	sql *sql.DB
}

// Open opens (never creates) a SQLite database file. Opening is verified
// with a Ping right away so the caller gets an honest error instead of a
// deferred failure on the first query.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{sql: db}, nil
}

// Close releases the connection. Safe to call twice.
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	err := d.sql.Close()
	d.sql = nil
	return err
}

// quoteIdent makes a safely quoted identifier: every internal quote doubled,
// the whole name wrapped in double quotes. Never build SQL with a bare name.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Tables lists user tables and views; internal sqlite_* objects stay hidden.
func (d *DB) Tables() ([]Entry, error) {
	rows, err := d.sql.Query(`SELECT name, type FROM sqlite_master
		WHERE type IN ('table','view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Name, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Columns lists the table's columns via PRAGMA table_info (views too).
func (d *DB) Columns(table string) ([]Column, error) {
	rows, err := d.sql.Query("PRAGMA table_info(" + quoteIdent(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var c Column
		var cid, notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &c.Name, &c.Type, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		c.PK = pk > 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// HasRowID reports whether rows of the table can be addressed by rowid.
// Views never can; WITHOUT ROWID tables cannot either.
func (d *DB) HasRowID(table string) bool {
	rows, err := d.sql.Query("SELECT rowid FROM " + quoteIdent(table) + " LIMIT 1")
	if err != nil {
		return false
	}
	rows.Close()
	return true
}

func escapeLike(q string) string {
	out := make([]rune, 0, len(q))
	for _, r := range q {
		if r == '\\' || r == '%' || r == '_' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// whereFor builds the WHERE clause of the filter: a bare substring query is
// matched (LIKE with escaping of the standard wildcards) against every
// column cast to text.
func (d *DB) whereFor(table, q string) (string, []any) {
	if q == "" {
		return "", nil
	}
	cols, err := d.Columns(table)
	if err != nil || len(cols) == 0 {
		return "", nil
	}
	pat := "%" + escapeLike(q) + "%"
	cond := ""
	var args []any
	for _, c := range cols {
		if cond != "" {
			cond += " OR "
		}
		cond += "CAST(" + quoteIdent(c.Name) + " AS TEXT) LIKE ? ESCAPE '\\'"
		args = append(args, pat)
	}
	return " WHERE (" + cond + ")", args
}

// Count counts the rows matching the filter query.
func (d *DB) Count(table, q string) (int, error) {
	where, args := d.whereFor(table, q)
	var n int
	if err := d.sql.QueryRow("SELECT count(*) FROM "+quoteIdent(table)+where, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Page loads up to lim rows starting at off. orderCol is an index into cols
// (-1 keeps the natural order); desc sorts the order column descending. For
// a table with rowid the returned rids[i] addresses rows[i], so the browser
// can edit and delete. Views and WITHOUT ROWID tables get empty rids and are
// shown read-only.
func (d *DB) Page(table string, cols []string, q string, orderCol int, desc bool, off, lim int) (rows [][]Value, rids []string, err error) {
	where, args := d.whereFor(table, q)
	hasRid := d.HasRowID(table)

	sel := ""
	for _, c := range cols {
		if sel != "" {
			sel += ", "
		}
		sel += quoteIdent(c)
	}
	if hasRid {
		sel = "rowid, " + sel
	}
	qr := "SELECT " + sel + " FROM " + quoteIdent(table) + where
	if orderCol >= 0 && orderCol < len(cols) {
		dir := " ASC"
		if desc {
			dir = " DESC"
		}
		qr += " ORDER BY " + quoteIdent(cols[orderCol]) + dir
	}
	qr += " LIMIT " + strconv.Itoa(lim) + " OFFSET " + strconv.Itoa(off)

	rrows, err := d.sql.Query(qr, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rrows.Close()

	ncol := len(cols)
	if hasRid {
		ncol++
	}
	for rrows.Next() {
		vals := make([]any, ncol) // storage-class preserving scan
		for i := range vals {
			vals[i] = new(any)
		}
		if err := rrows.Scan(vals...); err != nil {
			return nil, nil, err
		}
		start := 0
		if hasRid {
			rids = append(rids, valueText(*vals[0].(*any)))
			start = 1
		}
		row := make([]Value, 0, len(cols))
		for _, v := range vals[start:] {
			row = append(row, toValue(*v.(*any)))
		}
		rows = append(rows, row)
	}
	return rows, rids, rrows.Err()
}

// toValue converts a scanned database value into the renderable Value. NULL
// stays null; numbers keep their kind so the browser can right-align them;
// a text column holding bytes that are not valid UTF-8 (or contain NUL) is
// rendered as a hex stub and treated as read-only blob content.
func toValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{IsNull: true, Kind: "null"}
	case int64:
		return Value{Kind: "int", Text: strconv.FormatInt(x, 10)}
	case float64:
		return Value{Kind: "float", Text: strconv.FormatFloat(x, 'g', -1, 64)}
	case string:
		return Value{Kind: "text", Text: x}
	case []byte:
		if !utf8.Valid(x) || containsNUL(x) {
			return Value{Kind: "blob", Text: BlobText(x)}
		}
		return Value{Kind: "text", Text: string(x)}
	case bool:
		if x {
			return Value{Kind: "int", Text: "1"}
		}
		return Value{Kind: "int", Text: "0"}
	case time.Time:
		return Value{Kind: "text", Text: x.Format(time.RFC3339)}
	default:
		return Value{Kind: "text", Text: fmt.Sprintf("%v", x)}
	}
}

// valueText flattens a scanned value to the string used for rowid (a rowid
// is always int64, but the helper keeps the call sites uniform).
func valueText(v any) string {
	return toValue(v).Text
}

func containsNUL(b []byte) bool {
	for _, x := range b {
		if x == 0 {
			return true
		}
	}
	return false
}

// BlobText renders binary content as a hex stub of at most 12 bytes.
func BlobText(data []byte) string {
	const max = 12
	n := len(data)
	if n > max {
		n = max
	}
	out := "0x"
	for i := 0; i < n; i++ {
		const hex = "0123456789abcdef"
		out += string([]byte{hex[data[i]>>4], hex[data[i]&0xF]})
	}
	if len(data) > max {
		out += "..."
	}
	return out
}

// UpdateCell writes text into one cell addressed by rowid. An empty string
// or a literal NULL stores NULL; anything else is stored as text and SQLite
// column affinity re-types it (digits become numbers and so on). Rows that
// cannot exist anymore (stale rowid) are reported as an error.
func (d *DB) UpdateCell(table, col, rid, text string) error {
	var val any
	if text == "" || text == "NULL" {
		val = nil
	} else {
		val = text
	}
	res, err := d.sql.Exec("UPDATE "+quoteIdent(table)+" SET "+quoteIdent(col)+" = ? WHERE rowid = ?", val, rid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return os.ErrNotExist
	}
	return nil
}

// DeleteRow removes one row by rowid.
func (d *DB) DeleteRow(table, rid string) error {
	res, err := d.sql.Exec("DELETE FROM "+quoteIdent(table)+" WHERE rowid = ?", rid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return os.ErrNotExist
	}
	return nil
}

// InsertRow adds an all-defaults row and returns its rowid. A NOT NULL
// column without a default makes this fail; the error is surfaced verbatim.
func (d *DB) InsertRow(table string) (int64, error) {
	res, err := d.sql.Exec("INSERT INTO " + quoteIdent(table) + " DEFAULT VALUES")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Schema returns the CREATE statement of a table or view.
func (d *DB) Schema(name string) (string, error) {
	var s sql.NullString
	if err := d.sql.QueryRow("SELECT sql FROM sqlite_master WHERE name = ?", name).Scan(&s); err != nil {
		return "", err
	}
	if !s.Valid {
		return "", nil
	}
	return s.String, nil
}
