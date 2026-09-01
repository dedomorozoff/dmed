package rope

import (
	"math/rand"
	"strings"
	"testing"
)

func runes(s string) []rune { return []rune(s) }
func str(r []rune) string   { return string(r) }

func TestNewAndString(t *testing.T) {
	r := New(runes("hello world"))
	if got := str(r.String()); got != "hello world" {
		t.Fatalf("got %q", got)
	}
	if r.Length() != 11 {
		t.Fatalf("length=%d want 11", r.Length())
	}
	if New(nil).Length() != 0 {
		t.Fatal("empty rope length must be 0")
	}
}

func TestAtAndSlice(t *testing.T) {
	s := "abcde"
	r := New(runes(s))
	for i := 0; i < len(s); i++ {
		if got := r.At(i); got != rune(s[i]) {
			t.Fatalf("At(%d)=%q want %q", i, got, s[i])
		}
	}
	if got := str(r.Slice(1, 3)); got != "bcd" {
		t.Fatalf("Slice(1,3)=%q", got)
	}
	if got := str(r.Slice(2, 100)); got != "cde" {
		t.Fatalf("Slice clamp=%q", got)
	}
}

func TestInsert(t *testing.T) {
	cases := []struct {
		pos       int
		ins, want string
	}{
		{0, "X", "Xabcde"},
		{5, "X", "abcdeX"},
		{2, "ZZ", "abZZcde"},
	}
	for _, c := range cases {
		r := New(runes("abcde"))
		r.Insert(c.pos, runes(c.ins))
		if got := str(r.String()); got != c.want {
			t.Fatalf("Insert(%d,%q)=%q want %q", c.pos, c.ins, got, c.want)
		}
	}
}

func TestDelete(t *testing.T) {
	cases := []struct {
		from, n int
		want    string
	}{
		{0, 2, "cde"},
		{3, 2, "abc"},
		{1, 3, "ae"},
		{5, 10, "abcde"},
		{0, 0, "abcde"},
	}
	for _, c := range cases {
		r := New(runes("abcde"))
		r.Delete(c.from, c.n)
		if got := str(r.String()); got != c.want {
			t.Fatalf("Delete(%d,%d)=%q want %q", c.from, c.n, got, c.want)
		}
	}
}

// TestRandomAgainstSlice runs a sequence of random inserts/deletes and checks
// the rope always matches a reference flat slice.
func TestRandomAgainstSlice(t *testing.T) {
	ref := []rune("the quick brown fox jumps over the lazy dog")
	r := New(ref)
	rand.Seed(1)
	for i := 0; i < 2000; i++ {
		switch rand.Intn(3) {
		case 0: // insert
			pos := rand.Intn(len(ref) + 1)
			ins := []rune(strings.Repeat("y", 1+rand.Intn(30)))
			r.Insert(pos, ins)
			ref = append(ref, ins...)
			copy(ref[pos+len(ins):], ref[pos:])
			copy(ref[pos:], ins)
		case 1: // delete
			if len(ref) == 0 {
				continue
			}
			from := rand.Intn(len(ref))
			n := rand.Intn(len(ref)-from) + 1
			r.Delete(from, n)
			ref = append(ref[:from], ref[from+n:]...)
		default: // verify
			if r.Length() != len(ref) {
				t.Fatalf("length=%d want %d", r.Length(), len(ref))
			}
			if got, want := str(r.String()), str(ref); got != want {
				t.Fatalf("rope=%q ref=%q", got, want)
			}
		}
	}
	if got, want := str(r.String()), str(ref); got != want {
		t.Fatalf("final: rope=%q ref=%q", got, want)
	}
}

// TestLargeFileRebalance stresses the tree with big content to exercise
// rebalancing paths.
func TestLargeFileRebalance(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString("line ")
		sb.WriteByte(byte('a' + i%26))
		sb.WriteByte('\n')
	}
	content := sb.String()
	r := New(runes(content))

	// Interleave edits across the whole document.
	for i := 0; i < 500; i++ {
		pos := rand.Intn(r.Length())
		r.Insert(pos, runes("XY"))
	}
	if r.Length() != len(runes(content))+1000 {
		t.Fatalf("length=%d", r.Length())
	}
	// Access At at scattered indices must not panic.
	for i := 0; i < 1000; i++ {
		r.At(rand.Intn(r.Length()))
	}
}

func TestEmptyDeleteRange(t *testing.T) {
	r := New(runes("abc"))
	r.Delete(1, 0)
	if got := str(r.String()); got != "abc" {
		t.Fatalf("got %q", got)
	}
	r.Delete(99, 5)
	if got := str(r.String()); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
