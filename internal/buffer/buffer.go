package buffer

import "strings"

const maxUndo = 1000

// snapshot records the persistent document root before a change; because the
// tree is immutable, restoring undo is just swapping the root pointer (O(1)).
type snapshot struct {
	root *lnode
	line int
	col  int
}

type Buffer struct {
	d             *doc
	line          int
	col           int
	goalCol       int
	selLine       int
	selCol        int
	hasSelection  bool
	undoStack     []snapshot
	redoStack     []snapshot
	saved         string
	lastWasInsert bool
	lastLine      int
	lastCol       int

	// Multi-cursor support: secondary cursors plus the word target used by
	// AddNextOccurrence (Alt+D). See multicursor.go.
	cursors []Cursor
	word    string
}

func New() *Buffer {
	return &Buffer{d: newDoc([][]rune{{}}), saved: "\n"}
}

func Load(s string) *Buffer {
	b := New()
	if s == "" {
		return b
	}
	split := strings.Split(s, "\n")
	if len(split) > 1 && split[len(split)-1] == "" {
		split = split[:len(split)-1]
	}
	lines := make([][]rune, len(split))
	for i, l := range split {
		lines[i] = []rune(l)
	}
	b.d = newDoc(lines)
	b.saved = b.Text()
	return b
}

// linesCopy returns the document as a flat slice of lines (for the multi-
// cursor edit paths, which operate on a flat view).
func (b *Buffer) linesCopy() [][]rune { return b.d.lines() }

// setLines replaces the whole document from a flat slice of lines.
func (b *Buffer) setLines(m [][]rune) { b.d = newDoc(m) }

func (b *Buffer) lineLen(i int) int { return len(b.d.lineAt(i)) }

func (b *Buffer) pushUndo() {
	b.undoStack = append(b.undoStack, snapshot{b.d.root, b.line, b.col})
	if len(b.undoStack) > maxUndo {
		b.undoStack = b.undoStack[1:]
	}
}

func (b *Buffer) breakGroup() {
	b.lastWasInsert = false
}

func (b *Buffer) beginChange() {
	b.breakGroup()
	b.redoStack = nil
}

func (b *Buffer) Text() string { return b.d.text() }

func (b *Buffer) MarkSaved()  { b.saved = b.Text() }
func (b *Buffer) Dirty() bool { return b.Text() != b.saved }

func (b *Buffer) LineCount() int      { return b.d.count() }
func (b *Buffer) LineAt(i int) []rune { return b.d.lineAt(i) }
func (b *Buffer) CurLine() int        { return b.line }
func (b *Buffer) Col() int            { return b.col }

func (b *Buffer) HasSelection() bool {
	return b.hasSelection && !(b.selLine == b.line && b.selCol == b.col)
}

func (b *Buffer) ClearSelection() {
	b.hasSelection = false
}

// Deselect clears any active selection.
func (b *Buffer) Deselect() {
	b.hasSelection = false
}

// StartSelection ensures a selection anchor exists at the current cursor.
func (b *Buffer) StartSelection() {
	if !b.hasSelection {
		b.hasSelection = true
		b.selLine = b.line
		b.selCol = b.col
	}
}

// DragSelect moves the cursor and extends the selection anchor.
func (b *Buffer) DragSelect(line, col int) {
	if !b.hasSelection {
		b.hasSelection = true
		b.selLine = b.line
		b.selCol = b.col
	}
	b.line = line
	b.col = col
	b.clampCol()
}

// LineLen returns the number of runes in the given line.
func (b *Buffer) LineLen(i int) int { return b.lineLen(i) }

func (b *Buffer) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if !b.hasSelection {
		return b.line, b.col, b.line, b.col
	}
	if b.selLine < b.line || (b.selLine == b.line && b.selCol <= b.col) {
		return b.selLine, b.selCol, b.line, b.col
	}
	return b.line, b.col, b.selLine, b.selCol
}

func (b *Buffer) SelectedText() string {
	if !b.HasSelection() {
		return ""
	}
	sl, sc, el, ec := b.SelectionRange()
	if sl == el {
		return string(b.d.lineAt(sl)[sc:ec])
	}
	var parts []string
	parts = append(parts, string(b.d.lineAt(sl)[sc:]))
	for i := sl + 1; i < el; i++ {
		parts = append(parts, string(b.d.lineAt(i)))
	}
	parts = append(parts, string(b.d.lineAt(el)[:ec]))
	return strings.Join(parts, "\n")
}

func (b *Buffer) deleteSelectionWithoutUndo() (int, int) {
	if !b.HasSelection() {
		return b.line, b.col
	}
	sl, sc, el, ec := b.SelectionRange()
	start := b.d.lineAt(sl)[:sc]
	end := b.d.lineAt(el)[ec:]
	combined := append(append([]rune(nil), start...), end...)
	b.d.root = b.d.setLine(sl, combined)
	b.d.root = b.d.deleteLines(sl+1, el-sl)
	b.line = sl
	b.col = sc
	b.goalCol = b.col
	b.hasSelection = false
	return sl, sc
}

func (b *Buffer) DeleteSelection() bool {
	if !b.HasSelection() {
		return false
	}
	b.beginChange()
	b.pushUndo()
	b.deleteSelectionWithoutUndo()
	return true
}

func splitTextLines(text string) [][]rune {
	parts := strings.Split(text, "\n")
	out := make([][]rune, len(parts))
	for i, p := range parts {
		out[i] = []rune(p)
	}
	return out
}

func (b *Buffer) InsertText(text string) {
	if text == "" && !b.HasSelection() {
		return
	}
	b.beginChange()
	b.pushUndo()
	if b.HasSelection() {
		b.deleteSelectionWithoutUndo()
	}

	ins := splitTextLines(text)
	cur := b.d.lineAt(b.line)
	before := cur[:b.col]
	after := cur[b.col:]

	if len(ins) == 1 {
		nl := append(append([]rune(nil), before...), ins[0]...)
		nl = append(nl, after...)
		b.d.root = b.d.setLine(b.line, nl)
		b.col += len(ins[0])
		b.goalCol = b.col
		return
	}

	first := append(append([]rune(nil), before...), ins[0]...)
	last := append(append([]rune(nil), ins[len(ins)-1]...), after...)
	tail := make([][]rune, 0, len(ins)-1)
	tail = append(tail, ins[1:len(ins)-1]...)
	tail = append(tail, last)

	b.d.root = b.d.setLine(b.line, first)
	b.d.root = b.d.insertLines(b.line+1, tail)
	b.line = b.line + len(ins) - 1
	b.col = len(ins[len(ins)-1])
	b.goalCol = b.col
}

func (b *Buffer) SetCursor(line, col int) {
	b.breakGroup()
	b.hasSelection = false
	if line < 0 {
		line = 0
	} else if line >= b.d.count() {
		line = b.d.count() - 1
	}
	b.line = line
	if col < 0 {
		col = 0
	} else if col > b.lineLen(b.line) {
		col = b.lineLen(b.line)
	}
	b.col = col
	b.goalCol = b.col
}

func (b *Buffer) ReplaceRange(line, col, length int, replacement []rune) bool {
	if line < 0 || line >= b.d.count() {
		return false
	}
	l := b.d.lineAt(line)
	if col < 0 || col > len(l) || col+length > len(l) {
		return false
	}
	b.beginChange()
	b.pushUndo()
	newLine := make([]rune, 0, len(l)-length+len(replacement))
	newLine = append(newLine, l[:col]...)
	newLine = append(newLine, replacement...)
	newLine = append(newLine, l[col+length:]...)
	b.d.root = b.d.setLine(line, newLine)
	b.line = line
	b.col = col + len(replacement)
	b.goalCol = b.col
	b.hasSelection = false
	return true
}

func (b *Buffer) ReplaceAll(find, replace string) int {
	if find == "" {
		return 0
	}
	count := 0
	n := b.d.count()
	for i := 0; i < n; i++ {
		count += strings.Count(string(b.d.lineAt(i)), find)
	}
	if count == 0 {
		return 0
	}
	b.beginChange()
	b.pushUndo()
	for i := 0; i < n; i++ {
		str := string(b.d.lineAt(i))
		if strings.Contains(str, find) {
			b.d.root = b.d.setLine(i, []rune(strings.ReplaceAll(str, find, replace)))
		}
	}
	if b.line >= b.d.count() {
		b.line = b.d.count() - 1
	}
	if b.col > b.lineLen(b.line) {
		b.col = b.lineLen(b.line)
	}
	b.goalCol = b.col
	b.hasSelection = false
	return count
}

func (b *Buffer) Insert(r rune) {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	grouped := b.lastWasInsert && b.line == b.lastLine && b.col == b.lastCol
	b.breakGroup()
	if !grouped {
		b.pushUndo()
	}
	cur := b.d.lineAt(b.line)
	nl := make([]rune, 0, len(cur)+1)
	nl = append(nl, cur[:b.col]...)
	nl = append(nl, r)
	nl = append(nl, cur[b.col:]...)
	b.d.root = b.d.setLine(b.line, nl)
	b.col++
	b.goalCol = b.col
	b.lastWasInsert = true
	b.lastLine = b.line
	b.lastCol = b.col
	b.hasSelection = false
}

func (b *Buffer) InsertNewline() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	b.beginChange()
	b.pushUndo()
	l := b.d.lineAt(b.line)
	rest := append([]rune(nil), l[b.col:]...)
	b.d.root = b.d.setLine(b.line, l[:b.col:b.col])
	b.d.root = b.d.insertLines(b.line+1, [][]rune{rest})
	b.line++
	b.col = 0
	b.goalCol = 0
	b.hasSelection = false
}

func (b *Buffer) Backspace() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	b.beginChange()
	if b.col > 0 {
		b.pushUndo()
		cur := b.d.lineAt(b.line)
		nl := make([]rune, 0, len(cur)-1)
		nl = append(nl, cur[:b.col-1]...)
		nl = append(nl, cur[b.col:]...)
		b.d.root = b.d.setLine(b.line, nl)
		b.col--
	} else if b.line > 0 {
		b.pushUndo()
		prev := b.d.lineAt(b.line - 1)
		cur := b.d.lineAt(b.line)
		merged := append(append([]rune(nil), prev...), cur...)
		b.d.root = b.d.setLine(b.line-1, merged)
		b.d.root = b.d.deleteLines(b.line, 1)
		b.line--
		b.col = len(prev)
	} else {
		return
	}
	b.goalCol = b.col
}

func (b *Buffer) Delete() {
	if b.HasSelection() {
		b.DeleteSelection()
		return
	}
	b.beginChange()
	cur := b.d.lineAt(b.line)
	if b.col < len(cur) {
		b.pushUndo()
		nl := make([]rune, 0, len(cur)-1)
		nl = append(nl, cur[:b.col]...)
		nl = append(nl, cur[b.col+1:]...)
		b.d.root = b.d.setLine(b.line, nl)
	} else if b.line < b.d.count()-1 {
		b.pushUndo()
		next := b.d.lineAt(b.line + 1)
		merged := append(append([]rune(nil), cur...), next...)
		b.d.root = b.d.setLine(b.line, merged)
		b.d.root = b.d.deleteLines(b.line+1, 1)
	}
}

func (b *Buffer) DeleteLine() {
	b.beginChange()
	b.pushUndo()
	if b.d.count() == 1 {
		b.d.root = b.d.setLine(0, []rune{})
		b.line = 0
		b.col = 0
		b.goalCol = 0
		return
	}
	b.d.root = b.d.deleteLines(b.line, 1)
	if b.line >= b.d.count() {
		b.line = b.d.count() - 1
	}
	b.col = 0
	b.goalCol = 0
}

// DuplicateLine duplicates the current line (or selected lines) below.
func (b *Buffer) DuplicateLine() {
	b.beginChange()
	b.pushUndo()
	sl, _, el, _ := b.SelectionRange()
	if !b.HasSelection() {
		sl = b.line
		el = b.line
	}
	dup := make([][]rune, el-sl+1)
	for i := sl; i <= el; i++ {
		dup[i-sl] = append([]rune(nil), b.d.lineAt(i)...)
	}
	b.d.root = b.d.insertLines(el+1, dup)
	b.line = el + 1
	b.goalCol = b.col
	b.hasSelection = false
}

func (b *Buffer) MoveLeft() {
	b.hasSelection = false
	b.breakGroup()
	if b.col > 0 {
		b.col--
	} else if b.line > 0 {
		b.line--
		b.col = b.lineLen(b.line)
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveRight() {
	b.hasSelection = false
	b.breakGroup()
	if b.col < b.lineLen(b.line) {
		b.col++
	} else if b.line < b.d.count()-1 {
		b.line++
		b.col = 0
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveUp() {
	b.hasSelection = false
	b.breakGroup()
	if b.line == 0 {
		return
	}
	b.line--
	b.clampCol()
}

func (b *Buffer) MoveDown() {
	b.hasSelection = false
	b.breakGroup()
	if b.line >= b.d.count()-1 {
		return
	}
	b.line++
	b.clampCol()
}

func (b *Buffer) MoveLeftWithSelect() {
	b.StartSelection()
	b.breakGroup()
	if b.col > 0 {
		b.col--
	} else if b.line > 0 {
		b.line--
		b.col = b.lineLen(b.line)
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveRightWithSelect() {
	b.StartSelection()
	b.breakGroup()
	if b.col < b.lineLen(b.line) {
		b.col++
	} else if b.line < b.d.count()-1 {
		b.line++
		b.col = 0
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveUpWithSelect() {
	b.StartSelection()
	b.breakGroup()
	if b.line == 0 {
		return
	}
	b.line--
	b.clampCol()
}

func (b *Buffer) MoveDownWithSelect() {
	b.StartSelection()
	b.breakGroup()
	if b.line >= b.d.count()-1 {
		return
	}
	b.line++
	b.clampCol()
}

// groupLines materializes lines[from..to] inclusive.
func (b *Buffer) groupLines(from, to int) [][]rune {
	out := make([][]rune, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, append([]rune(nil), b.d.lineAt(i)...))
	}
	return out
}

// MoveLineUp moves the current line (or all selected lines) up by one position.
func (b *Buffer) MoveLineUp() {
	b.beginChange()
	b.pushUndo()
	sl, _, el, _ := b.SelectionRange()
	if !b.HasSelection() {
		sl = b.line
		el = b.line
	}
	if sl == 0 {
		return
	}
	above := b.d.lineAt(sl - 1)
	group := b.groupLines(sl, el)
	// Remove [above group] then re-insert [group above] at sl-1.
	b.d.root = b.d.deleteLines(sl-1, el-sl+2)
	reinsert := append([][]rune(nil), group...)
	reinsert = append(reinsert, append([]rune(nil), above...))
	b.d.root = b.d.insertLines(sl-1, reinsert)
	b.line = sl - 1
	b.goalCol = b.col
	b.hasSelection = false
}

// MoveLineDown moves the current line (or all selected lines) down by one position.
func (b *Buffer) MoveLineDown() {
	b.beginChange()
	b.pushUndo()
	sl, _, el, _ := b.SelectionRange()
	if !b.HasSelection() {
		sl = b.line
		el = b.line
	}
	if el >= b.d.count()-1 {
		return
	}
	below := b.d.lineAt(el + 1)
	group := b.groupLines(sl, el)
	// Remove [group below] then re-insert [below group] at sl.
	b.d.root = b.d.deleteLines(sl, el-sl+2)
	reinsert := append([][]rune{append([]rune(nil), below...)}, group...)
	b.d.root = b.d.insertLines(sl, reinsert)
	b.line = sl + 1
	b.goalCol = b.col
	b.hasSelection = false
}

func (b *Buffer) clampCol() {
	n := b.lineLen(b.line)
	if b.goalCol < n {
		b.col = b.goalCol
	} else {
		b.col = n
	}
}

func (b *Buffer) LineStart() {
	b.hasSelection = false
	b.breakGroup()
	b.col = 0
	b.goalCol = 0
}

func (b *Buffer) LineEnd() {
	b.hasSelection = false
	b.breakGroup()
	b.col = b.lineLen(b.line)
	b.goalCol = b.col
}

func (b *Buffer) LineStartWithSelect() {
	b.StartSelection()
	b.breakGroup()
	b.col = 0
	b.goalCol = 0
}

func (b *Buffer) LineEndWithSelect() {
	b.StartSelection()
	b.breakGroup()
	b.col = b.lineLen(b.line)
	b.goalCol = b.col
}

func (b *Buffer) Undo() bool {
	b.breakGroup()
	if len(b.undoStack) == 0 {
		return false
	}
	s := b.undoStack[len(b.undoStack)-1]
	b.undoStack = b.undoStack[:len(b.undoStack)-1]
	b.redoStack = append(b.redoStack, snapshot{b.d.root, b.line, b.col})
	b.d.root, b.line, b.col = s.root, s.line, s.col
	b.goalCol = b.col
	return true
}

func (b *Buffer) Redo() bool {
	b.breakGroup()
	if len(b.redoStack) == 0 {
		return false
	}
	s := b.redoStack[len(b.redoStack)-1]
	b.redoStack = b.redoStack[:len(b.redoStack)-1]
	b.pushUndo()
	b.d.root, b.line, b.col = s.root, s.line, s.col
	b.goalCol = b.col
	return true
}
