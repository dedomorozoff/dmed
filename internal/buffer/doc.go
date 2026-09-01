package buffer

import "strings"

// This file implements a persistent, height-balanced tree of lines
// (leaf = one line). It replaces the flat [][]rune slice backing the buffer:
//
//   - random line access and edits are O(log n) instead of O(n),
//   - edits are functional (they return a new root), so undo only has to keep
//     a pointer to the previous root instead of cloning the whole document.
//
// The tree is a rope over lines: internal nodes track the line count of their
// left subtree, so splitting/concatting by line index is O(log n).

// lnode is a node in the line tree. Leaves hold a single line.
type lnode struct {
	left, right *lnode
	line        []rune
	count       int // total lines in this subtree
	height      int
}

func lIsLeaf(n *lnode) bool { return n.line != nil }

func lLeaf(line []rune) *lnode {
	// A line must never be a nil slice: lIsLeaf relies on line != nil to
	// distinguish leaves from internal nodes, and nil lines would be mistaken
	// for internal nodes.
	if line == nil {
		line = []rune{}
	}
	return &lnode{line: line, count: 1, height: 1}
}

func lHeight(n *lnode) int {
	if n == nil {
		return 0
	}
	return n.height
}

func lMk(left, right *lnode) *lnode {
	n := &lnode{left: left, right: right, count: left.count + right.count}
	lh, rh := lHeight(left), lHeight(right)
	if lh > rh {
		n.height = lh + 1
	} else {
		n.height = rh + 1
	}
	return n
}

func lLog2ceil(x int) int {
	if x <= 1 {
		return 0
	}
	n := 0
	for x > 1 {
		x = (x + 1) / 2
		n++
	}
	return n
}

// lRebalance flattens and rebuilds the tree when it has grown too tall for its
// size, restoring the O(log n) invariant.
func lRebalance(n *lnode) *lnode {
	if n == nil || lIsLeaf(n) {
		return n
	}
	if n.height <= 2*lLog2ceil(n.count)+1 {
		return n
	}
	var lines [][]rune
	lCollect(n, &lines)
	return lBuild(lines)
}

func lCollect(n *lnode, out *[][]rune) {
	if n == nil {
		return
	}
	if lIsLeaf(n) {
		*out = append(*out, n.line)
		return
	}
	lCollect(n.left, out)
	lCollect(n.right, out)
}

// lBuild builds a height-balanced tree from the given lines.
func lBuild(lines [][]rune) *lnode {
	if len(lines) == 0 {
		return nil
	}
	nodes := make([]*lnode, len(lines))
	for i, l := range lines {
		nodes[i] = lLeaf(l)
	}
	for len(nodes) > 1 {
		next := make([]*lnode, 0, (len(nodes)+1)/2)
		for i := 0; i < len(nodes); i += 2 {
			if i+1 < len(nodes) {
				next = append(next, lMk(nodes[i], nodes[i+1]))
			} else {
				next = append(next, nodes[i])
			}
		}
		nodes = next
	}
	return nodes[0]
}

// lSplit splits n so left contains exactly k lines.
func lSplit(n *lnode, k int) (*lnode, *lnode) {
	if n == nil {
		return nil, nil
	}
	if lIsLeaf(n) {
		if k <= 0 {
			return nil, n
		}
		return n, nil
	}
	lc := n.left.count
	if k < lc {
		a, b := lSplit(n.left, k)
		return a, lConcat(b, n.right)
	}
	if k > lc {
		c, d := lSplit(n.right, k-lc)
		return lConcat(n.left, c), d
	}
	return n.left, n.right
}

func lConcat(a, b *lnode) *lnode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	// Each leaf holds exactly one line, so we never merge adjacent leaves
	// here (that would collapse two lines into one).
	return lRebalance(lMk(a, b))
}

func lLineAt(n *lnode, i int) []rune {
	for n != nil && !lIsLeaf(n) {
		lc := n.left.count
		if i < lc {
			n = n.left
		} else {
			i -= lc
			n = n.right
		}
	}
	if n == nil {
		return nil
	}
	return n.line
}

func lText(n *lnode) string {
	if n == nil {
		return "\n"
	}
	var parts []string
	lCollectLines(n, &parts)
	return strings.Join(parts, "\n") + "\n"
}

func lCollectLines(n *lnode, out *[]string) {
	if n == nil {
		return
	}
	if lIsLeaf(n) {
		*out = append(*out, string(n.line))
		return
	}
	lCollectLines(n.left, out)
	lCollectLines(n.right, out)
}

// doc is a persistent line store used by Buffer.
type doc struct {
	root *lnode
}

func newDoc(lines [][]rune) *doc { return &doc{root: lBuild(lines)} }

func (d *doc) count() int {
	if d == nil || d.root == nil {
		return 0
	}
	return d.root.count
}

// lineAt returns a copy of the line at index i (copy avoids aliasing into the
// persistent tree).
func (d *doc) lineAt(i int) []rune {
	l := lLineAt(d.root, i)
	return append([]rune(nil), l...)
}

// setLine replaces the line at index i, returning the new root.
func (d *doc) setLine(i int, line []rune) *lnode {
	a, bc := lSplit(d.root, i)
	_, c := lSplit(bc, 1)
	return lConcat(lConcat(a, lLeaf(line)), c)
}

// insertLines inserts the lines at index at, returning the new root.
func (d *doc) insertLines(at int, lines [][]rune) *lnode {
	if len(lines) == 0 {
		return d.root
	}
	a, b := lSplit(d.root, at)
	return lConcat(lConcat(a, lBuild(lines)), b)
}

// deleteLines removes n lines starting at from, returning the new root.
func (d *doc) deleteLines(from, n int) *lnode {
	if n <= 0 {
		return d.root
	}
	a, bc := lSplit(d.root, from)
	_, c := lSplit(bc, n)
	return lConcat(a, c)
}

func (d *doc) text() string { return lText(d.root) }

// lines returns the full contents as a slice of lines.
func (d *doc) lines() [][]rune {
	out := make([][]rune, 0)
	if d.root != nil {
		lCollect(d.root, &out)
	}
	return out
}
