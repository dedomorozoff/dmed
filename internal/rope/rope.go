// Package rope implements a rune-based rope: a balanced binary tree of text
// fragments that supports O(log n) insertion, deletion, and random access
// instead of the O(n) cost of a flat slice on large documents.
//
// The rope is the intended backing store for the editor buffer (the roadmap's
// "Rope-структура" item). It is independent of the buffer so it can be built
// and tested on its own before integration.
package rope

// maxLeaf bounds the number of runes stored in a single leaf.
const maxLeaf = 64

// node is either a leaf (holding runs) or an internal node (left/right).
type node struct {
	left, right *node
	runs        []rune
	len, height int
}

func isLeaf(n *node) bool { return n.runs != nil }

func leaf(r []rune) *node { return &node{runs: r, len: len(r), height: 1} }

func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func mk(left, right *node) *node {
	n := &node{left: left, right: right, len: left.len + right.len}
	lh, rh := height(left), height(right)
	if lh > rh {
		n.height = lh + 1
	} else {
		n.height = rh + 1
	}
	return n
}

// Rope is a sequence of runes.
type Rope struct {
	root *node
}

// New returns a rope containing the given runes.
func New(r []rune) *Rope { return &Rope{root: buildBalanced(chunk(r))} }

// chunk splits r into rune chunks of at most maxLeaf runes.
func chunk(r []rune) []*node {
	var out []*node
	for len(r) > 0 {
		n := len(r)
		if n > maxLeaf {
			n = maxLeaf
		}
		out = append(out, leaf(r[:n:n]))
		r = r[n:]
	}
	return out
}

// buildBalanced builds a height-balanced tree from leaves.
func buildBalanced(leaves []*node) *node {
	if len(leaves) == 0 {
		return nil
	}
	for len(leaves) > 1 {
		next := make([]*node, 0, (len(leaves)+1)/2)
		for i := 0; i < len(leaves); i += 2 {
			if i+1 < len(leaves) {
				next = append(next, mk(leaves[i], leaves[i+1]))
			} else {
				next = append(next, leaves[i])
			}
		}
		leaves = next
	}
	return leaves[0]
}

// Length returns the number of runes in the rope.
func (r *Rope) Length() int {
	if r == nil || r.root == nil {
		return 0
	}
	return r.root.len
}

// rebalance restores the height invariant by flattening the tree into leaves
// and rebuilding when it has grown too tall for its size.
func rebalance(n *node) *node {
	if n == nil || isLeaf(n) {
		return n
	}
	if n.height <= 2*log2ceil(n.len)+1 {
		return n
	}
	var leaves []*node
	collect(n, &leaves)
	return buildBalanced(leaves)
}

func collect(n *node, out *[]*node) {
	if n == nil {
		return
	}
	if isLeaf(n) {
		*out = append(*out, n)
		return
	}
	collect(n.left, out)
	collect(n.right, out)
}

func log2ceil(x int) int {
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

// split splits the tree at position pos so left contains exactly pos runes.
func split(n *node, pos int) (*node, *node) {
	if n == nil {
		return nil, nil
	}
	if isLeaf(n) {
		return leaf(n.runs[:pos:pos]), leaf(n.runs[pos:])
	}
	ll := n.left.len
	if pos < ll {
		a, b := split(n.left, pos)
		return a, rebalance(mk(b, n.right))
	}
	if pos > ll {
		c, d := split(n.right, pos-ll)
		return rebalance(mk(n.left, c)), d
	}
	return n.left, n.right
}

func concat(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if isLeaf(a) && isLeaf(b) && a.len+b.len <= maxLeaf {
		merged := make([]rune, 0, a.len+b.len)
		merged = append(merged, a.runs...)
		merged = append(merged, b.runs...)
		return leaf(merged)
	}
	return rebalance(mk(a, b))
}

// Insert inserts the runes at position pos.
func (r *Rope) Insert(pos int, s []rune) {
	if len(s) == 0 {
		return
	}
	if pos < 0 {
		pos = 0
	}
	if pos > r.Length() {
		pos = r.Length()
	}
	ins := New(s).root
	left, right := split(r.root, pos)
	r.root = concat(concat(left, ins), right)
}

// Delete removes n runes starting at position from.
func (r *Rope) Delete(from, n int) {
	if n <= 0 || r.Length() == 0 {
		return
	}
	if from < 0 {
		from = 0
	}
	if from >= r.Length() {
		return
	}
	if from+n > r.Length() {
		n = r.Length() - from
	}
	a, bc := split(r.root, from)
	_, c := split(bc, n)
	r.root = concat(a, c)
}

// At returns the rune at index i.
func (r *Rope) At(i int) rune {
	n := r.root
	for n != nil && !isLeaf(n) {
		ll := n.left.len
		if i < ll {
			n = n.left
		} else {
			i -= ll
			n = n.right
		}
	}
	if n == nil {
		return 0
	}
	return n.runs[i]
}

// Slice returns n runes starting at from.
func (r *Rope) Slice(from, n int) []rune {
	if n <= 0 || r.Length() == 0 {
		return nil
	}
	if from < 0 {
		from = 0
	}
	if from >= r.Length() {
		return nil
	}
	if from+n > r.Length() {
		n = r.Length() - from
	}
	_, bc := split(r.root, from)
	b, _ := split(bc, n)
	return flatten(b)
}

// flatten returns the runes of the subtree in order.
func flatten(n *node) []rune {
	if n == nil {
		return nil
	}
	if isLeaf(n) {
		return append([]rune(nil), n.runs...)
	}
	out := flatten(n.left)
	out = append(out, flatten(n.right)...)
	return out
}

// String returns the full contents as runes.
func (r *Rope) String() []rune { return flatten(r.root) }
