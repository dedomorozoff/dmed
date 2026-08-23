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
func (b *Buffer) SetCursor(line, col int) {
	b.breakGroup()
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
	return count
}

func (b *Buffer) Insert(r rune) {
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
}

func (b *Buffer) InsertNewline() {
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
}

func (b *Buffer) Backspace() {
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

func (b *Buffer) MoveLeft() {
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
	b.breakGroup()
	if b.line == 0 {
		return
	}
	b.line--
	b.clampCol()
}

func (b *Buffer) MoveDown() {
	b.breakGroup()
	if b.line >= len(b.lines)-1 {
		return
	}
	b.line++
	b.clampCol()
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
	b.breakGroup()
	b.col = 0
	b.goalCol = 0
}

func (b *Buffer) LineEnd() {
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
