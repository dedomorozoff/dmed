package buffer

import "strings"

const maxUndo = 1000

type snapshot struct {
	lines [][]rune
	line  int
	col   int
}

type Buffer struct {
	lines         [][]rune
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
}

func New() *Buffer {
	return &Buffer{lines: [][]rune{{}}, saved: "\n"}
}

func Load(s string) *Buffer {
	b := New()
	if s == "" {
		return b
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	b.lines = make([][]rune, len(lines))
	for i, l := range lines {
		b.lines[i] = []rune(l)
	}
	b.saved = b.Text()
	return b
}

func clone(lines [][]rune) [][]rune {
	out := make([][]rune, len(lines))
	for i, l := range lines {
		out[i] = append([]rune(nil), l...)
	}
	return out
}

func (b *Buffer) pushUndo() {
	b.undoStack = append(b.undoStack, snapshot{clone(b.lines), b.line, b.col})
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

func (b *Buffer) Text() string {
	parts := make([]string, len(b.lines))
	for i, l := range b.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n") + "\n"
}

func (b *Buffer) MarkSaved()  { b.saved = b.Text() }
func (b *Buffer) Dirty() bool { return b.Text() != b.saved }

func (b *Buffer) LineCount() int      { return len(b.lines) }
func (b *Buffer) LineAt(i int) []rune { return b.lines[i] }
func (b *Buffer) CurLine() int        { return b.line }
func (b *Buffer) Col() int            { return b.col }

func (b *Buffer) HasSelection() bool {
	return b.hasSelection && !(b.selLine == b.line && b.selCol == b.col)
}

func (b *Buffer) ClearSelection() {
	b.hasSelection = false
}

func (b *Buffer) StartSelection() {
	if !b.hasSelection {
		b.hasSelection = true
		b.selLine = b.line
		b.selCol = b.col
	}
}

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
		return string(b.lines[sl][sc:ec])
	}
	var parts []string
	parts = append(parts, string(b.lines[sl][sc:]))
	for i := sl + 1; i < el; i++ {
		parts = append(parts, string(b.lines[i]))
	}
	parts = append(parts, string(b.lines[el][:ec]))
	return strings.Join(parts, "\n")
}

func (b *Buffer) deleteSelectionWithoutUndo() (int, int) {
	if !b.HasSelection() {
		return b.line, b.col
	}
	sl, sc, el, ec := b.SelectionRange()
	startLineRunes := b.lines[sl][:sc]
	endLineRunes := b.lines[el][ec:]
	combined := append(append([]rune(nil), startLineRunes...), endLineRunes...)

	newLines := make([][]rune, 0, len(b.lines)-(el-sl))
	newLines = append(newLines, b.lines[:sl]...)
	newLines = append(newLines, combined)
	if el+1 < len(b.lines) {
		newLines = append(newLines, b.lines[el+1:]...)
	}

	b.lines = newLines
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

func (b *Buffer) InsertText(text string) {
	if text == "" && !b.HasSelection() {
		return
	}
	b.beginChange()
	b.pushUndo()
	if b.HasSelection() {
		b.deleteSelectionWithoutUndo()
	}

	lines := strings.Split(text, "\n")
	curLine := b.lines[b.line]
	before := curLine[:b.col]
	after := curLine[b.col:]

	if len(lines) == 1 {
		runes := []rune(lines[0])
		newL := append(append([]rune(nil), before...), runes...)
		newL = append(newL, after...)
		b.lines[b.line] = newL
		b.col += len(runes)
		b.goalCol = b.col
		return
	}

	firstLine := append(append([]rune(nil), before...), []rune(lines[0])...)
	lastLine := append([]rune(lines[len(lines)-1]), after...)

	insertedLines := make([][]rune, len(lines))
	insertedLines[0] = firstLine
	for i := 1; i < len(lines)-1; i++ {
		insertedLines[i] = []rune(lines[i])
	}
	insertedLines[len(lines)-1] = lastLine

	newLines := make([][]rune, 0, len(b.lines)+len(lines)-1)
	newLines = append(newLines, b.lines[:b.line]...)
	newLines = append(newLines, insertedLines...)
	if b.line+1 < len(b.lines) {
		newLines = append(newLines, b.lines[b.line+1:]...)
	}

	b.lines = newLines
	b.line = b.line + len(lines) - 1
	b.col = len([]rune(lines[len(lines)-1]))
	b.goalCol = b.col
}

func (b *Buffer) SetCursor(line, col int) {
	b.breakGroup()
	b.hasSelection = false
	if line < 0 {
		line = 0
	} else if line >= len(b.lines) {
		line = len(b.lines) - 1
	}
	b.line = line
	if col < 0 {
		col = 0
	} else if col > len(b.lines[b.line]) {
		col = len(b.lines[b.line])
	}
	b.col = col
	b.goalCol = b.col
}

func (b *Buffer) ReplaceRange(line, col, length int, replacement []rune) bool {
	if line < 0 || line >= len(b.lines) {
		return false
	}
	l := b.lines[line]
	if col < 0 || col > len(l) || col+length > len(l) {
		return false
	}
	b.beginChange()
	b.pushUndo()
	newLine := make([]rune, 0, len(l)-length+len(replacement))
	newLine = append(newLine, l[:col]...)
	newLine = append(newLine, replacement...)
	newLine = append(newLine, l[col+length:]...)
	b.lines[line] = newLine
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
	for _, l := range b.lines {
		str := string(l)
		count += strings.Count(str, find)
	}
	if count == 0 {
		return 0
	}
	b.beginChange()
	b.pushUndo()
	for i, l := range b.lines {
		str := string(l)
		if strings.Contains(str, find) {
			newStr := strings.ReplaceAll(str, find, replace)
			b.lines[i] = []rune(newStr)
		}
	}
	if b.line >= len(b.lines) {
		b.line = len(b.lines) - 1
	}
	if b.col > len(b.lines[b.line]) {
		b.col = len(b.lines[b.line])
	}
	b.goalCol = b.col
	b.hasSelection = false
	return count
}

func (b *Buffer) Insert(r rune) {
	if b.HasSelection() {
		b.DeleteSelection()
	}
	grouped := b.lastWasInsert && b.line == b.lastLine && b.col == b.lastCol
	b.breakGroup()
	if !grouped {
		b.pushUndo()
	}
	post := b.col + 1
	l := append(b.lines[b.line], 0)
	copy(l[b.col+1:], l[b.col:])
	l[b.col] = r
	b.lines[b.line] = l
	b.col = post
	b.goalCol = b.col
	b.lastWasInsert = true
	b.lastLine = b.line
	b.lastCol = b.col
	b.hasSelection = false
}

func (b *Buffer) InsertNewline() {
	if b.HasSelection() {
		b.DeleteSelection()
	}
	b.beginChange()
	b.pushUndo()
	l := b.lines[b.line]
	rest := append([]rune(nil), l[b.col:]...)
	b.lines[b.line] = l[:b.col:b.col]
	b.lines = append(b.lines, nil)
	copy(b.lines[b.line+2:], b.lines[b.line+1:])
	b.lines[b.line+1] = rest
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
		l := b.lines[b.line]
		copy(l[b.col-1:], l[b.col:])
		l[len(l)-1] = 0
		b.lines[b.line] = l[:len(l)-1]
		b.col--
	} else if b.line > 0 {
		b.pushUndo()
		prev := b.lines[b.line-1]
		cur := b.lines[b.line]
		b.lines[b.line-1] = append(append([]rune(nil), prev...), cur...)
		b.lines = append(b.lines[:b.line], b.lines[b.line+1:]...)
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
	l := b.lines[b.line]
	if b.col < len(l) {
		b.pushUndo()
		copy(l[b.col:], l[b.col+1:])
		l[len(l)-1] = 0
		b.lines[b.line] = l[:len(l)-1]
	} else if b.line < len(b.lines)-1 {
		b.pushUndo()
		next := b.lines[b.line+1]
		b.lines[b.line] = append(append([]rune(nil), l...), next...)
		b.lines = append(b.lines[:b.line+1], b.lines[b.line+2:]...)
	}
}

func (b *Buffer) DeleteLine() {
	b.beginChange()
	b.pushUndo()
	if len(b.lines) == 1 {
		b.lines[0] = []rune{}
		b.line = 0
		b.col = 0
		b.goalCol = 0
		return
	}
	b.lines = append(b.lines[:b.line], b.lines[b.line+1:]...)
	if b.line >= len(b.lines) {
		b.line = len(b.lines) - 1
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
	// Copy lines[sl..el] and insert after el
	dup := make([][]rune, el-sl+1)
	for i := sl; i <= el; i++ {
		dup[i-sl] = append([]rune(nil), b.lines[i]...)
	}
	newLines := make([][]rune, 0, len(b.lines)+len(dup))
	newLines = append(newLines, b.lines[:el+1]...)
	newLines = append(newLines, dup...)
	if el+1 < len(b.lines) {
		newLines = append(newLines, b.lines[el+1:]...)
	}
	b.lines = newLines
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
		b.col = len(b.lines[b.line])
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveRight() {
	b.hasSelection = false
	b.breakGroup()
	if b.col < len(b.lines[b.line]) {
		b.col++
	} else if b.line < len(b.lines)-1 {
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
	if b.line >= len(b.lines)-1 {
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
		b.col = len(b.lines[b.line])
	}
	b.goalCol = b.col
}

func (b *Buffer) MoveRightWithSelect() {
	b.StartSelection()
	b.breakGroup()
	if b.col < len(b.lines[b.line]) {
		b.col++
	} else if b.line < len(b.lines)-1 {
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
	if b.line >= len(b.lines)-1 {
		return
	}
	b.line++
	b.clampCol()
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
	// swap lines[sl-1] with lines[sl..el]
	above := b.lines[sl-1]
	group := b.lines[sl : el+1]
	newLines := make([][]rune, 0, len(b.lines))
	newLines = append(newLines, b.lines[:sl-1]...)
	newLines = append(newLines, group...)
	newLines = append(newLines, above)
	if el+1 < len(b.lines) {
		newLines = append(newLines, b.lines[el+1:]...)
	}
	b.lines = newLines
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
	if el >= len(b.lines)-1 {
		return
	}
	// swap lines[el+1] with lines[sl..el]
	below := b.lines[el+1]
	group := b.lines[sl : el+1]
	newLines := make([][]rune, 0, len(b.lines))
	newLines = append(newLines, b.lines[:sl]...)
	newLines = append(newLines, below)
	newLines = append(newLines, group...)
	if el+2 < len(b.lines) {
		newLines = append(newLines, b.lines[el+2:]...)
	}
	b.lines = newLines
	b.line = sl + 1
	b.goalCol = b.col
	b.hasSelection = false
}

func (b *Buffer) clampCol() {
	n := len(b.lines[b.line])
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
	b.col = len(b.lines[b.line])
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
	b.col = len(b.lines[b.line])
	b.goalCol = b.col
}

func (b *Buffer) Undo() bool {
	b.breakGroup()
	if len(b.undoStack) == 0 {
		return false
	}
	s := b.undoStack[len(b.undoStack)-1]
	b.undoStack = b.undoStack[:len(b.undoStack)-1]
	b.redoStack = append(b.redoStack, snapshot{clone(b.lines), b.line, b.col})
	b.lines, b.line, b.col = s.lines, s.line, s.col
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
	b.lines, b.line, b.col = s.lines, s.line, s.col
	b.goalCol = b.col
	return true
}
