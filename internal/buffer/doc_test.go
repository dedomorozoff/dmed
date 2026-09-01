package buffer

import (
	"math/rand"
	"reflect"
	"testing"
)

func lines(s ...string) [][]rune {
	out := make([][]rune, len(s))
	for i, l := range s {
		out[i] = []rune(l)
	}
	return out
}

func TestDocBasics(t *testing.T) {
	d := newDoc(lines("a", "b", "c"))
	if d.count() != 3 {
		t.Fatalf("count=%d", d.count())
	}
	if got := d.lineAt(1); string(got) != "b" {
		t.Fatalf("lineAt(1)=%q", got)
	}
	if got := d.text(); got != "a\nb\nc\n" {
		t.Fatalf("text=%q", got)
	}
	// Empty doc text is a single newline (matches Buffer.Load of empty).
	if got := newDoc(nil).text(); got != "\n" {
		t.Fatalf("empty text=%q", got)
	}
}

func TestDocSetInsertDelete(t *testing.T) {
	d := newDoc(lines("a", "b", "c"))

	// setLine
	d.root = d.setLine(1, []rune("X"))
	if got := d.text(); got != "a\nX\nc\n" {
		t.Fatalf("after set: %q", got)
	}

	// insertLines in the middle
	d.root = d.insertLines(1, lines("p", "q"))
	if got := d.text(); got != "a\np\nq\nX\nc\n" {
		t.Fatalf("after insert: %q", got)
	}
	if d.count() != 5 {
		t.Fatalf("count=%d", d.count())
	}

	// deleteLines
	d.root = d.deleteLines(1, 2)
	if got := d.text(); got != "a\nX\nc\n" {
		t.Fatalf("after delete: %q", got)
	}
}

// TestDocPersistent verifies old roots stay valid after edits (structural
// sharing), which is what makes O(1) undo possible.
func TestDocPersistent(t *testing.T) {
	d := newDoc(lines("a", "b", "c"))
	old := d.root
	d.root = d.setLine(1, []rune("X"))
	if got := lText(old); got != "a\nb\nc\n" {
		t.Fatalf("old root mutated: %q", got)
	}
	if got := d.text(); got != "a\nX\nc\n" {
		t.Fatalf("new root: %q", got)
	}
}

// TestDocRandom mirrors random edits against a flat reference slice.
func TestDocRandom(t *testing.T) {
	ref := [][]rune{[]rune("one"), []rune("two"), []rune("three")}
	d := newDoc(cloneLines(ref))
	rand.Seed(7)
	for i := 0; i < 3000; i++ {
		switch rand.Intn(3) {
		case 0: // setLine
			if len(ref) == 0 {
				continue
			}
			idx := rand.Intn(len(ref))
			nl := []rune("s" + itoa(rand.Intn(50)))
			d.root = d.setLine(idx, nl)
			ref[idx] = nl
		case 1: // insertLines
			at := rand.Intn(len(ref) + 1)
			ins := lines("i"+itoa(rand.Intn(50)), "j"+itoa(rand.Intn(50)))
			d.root = d.insertLines(at, ins)
			ref = append(ref, ins...)
			copy(ref[at+len(ins):], ref[at:])
			copy(ref[at:], ins)
		default: // deleteLines
			if len(ref) == 0 {
				continue
			}
			from := rand.Intn(len(ref))
			n := rand.Intn(len(ref)-from) + 1
			d.root = d.deleteLines(from, n)
			ref = append(ref[:from], ref[from+n:]...)
		}
		if d.count() != len(ref) {
			t.Fatalf("count=%d want %d", d.count(), len(ref))
		}
		if !reflect.DeepEqual(d.lines(), ref) {
			t.Fatalf("rope lines=%v ref=%v", d.lines(), ref)
		}
	}
}

func TestDocLarge(t *testing.T) {
	var big [][]rune
	for i := 0; i < 50000; i++ {
		big = append(big, []rune("line-"+itoa(i)))
	}
	d := newDoc(big)
	if d.count() != 50000 {
		t.Fatalf("count=%d", d.count())
	}
	for i := 0; i < 2000; i++ {
		idx := rand.Intn(d.count())
		d.lineAt(idx)
	}
	// Random middle edits on a large doc must not corrupt count/text length.
	for i := 0; i < 2000; i++ {
		d.root = d.setLine(rand.Intn(d.count()), []rune("e"))
	}
	if d.count() != 50000 {
		t.Fatalf("count after edits=%d", d.count())
	}
}

func itoa(i int) string {
	return string(rune('0' + i%10))
}

func cloneLines(in [][]rune) [][]rune {
	out := make([][]rune, len(in))
	for i, l := range in {
		out[i] = append([]rune(nil), l...)
	}
	return out
}
