package todo

import (
	"strings"
	"sync"
	"testing"
)

func TestReplaceTrimsAndCaps(t *testing.T) {
	l := New()
	kept, dropped := l.Replace([]Item{
		{Text: "  read the code  "},
		{Text: "   "}, // empty after trimming: dropped
		{Text: strings.Repeat("x", MaxItemLen+50)},
	})
	if kept != 2 || dropped != 1 {
		t.Fatalf("kept=%d dropped=%d, want 2/1", kept, dropped)
	}
	items := l.Items()
	if items[0].Text != "read the code" {
		t.Fatalf("first item = %q, want trimmed", items[0].Text)
	}
	if r := []rune(items[1].Text); len(r) != MaxItemLen {
		t.Fatalf("long item kept %d runes, want cap %d", len(r), MaxItemLen)
	}
}

func TestReplaceCapsListLength(t *testing.T) {
	l := New()
	var many []Item
	for i := 0; i < MaxItems+10; i++ {
		many = append(many, Item{Text: "step"})
	}
	// Duplicate texts are kept here: Replace does not merge.
	kept, dropped := l.Replace(many)
	if kept != MaxItems || dropped != 10 {
		t.Fatalf("kept=%d dropped=%d, want %d/10", kept, dropped, MaxItems)
	}
	if l.Len() != MaxItems {
		t.Fatalf("len = %d, want %d", l.Len(), MaxItems)
	}
}

// TestMergeTicksKnownSteps is the behaviour the tools rely on: a model that
// reports "add tests" again with done:true must not produce a second step.
func TestMergeTicksKnownSteps(t *testing.T) {
	l := New()
	l.Replace([]Item{{Text: "add tests"}, {Text: "write docs"}})

	merged := l.Merge([]Item{{Text: "Add Tests", Done: true}, {Text: "run the suite"}})

	if len(merged) != 3 {
		t.Fatalf("merged = %d items, want 3: %+v", len(merged), merged)
	}
	if !merged[0].Done {
		t.Fatal("the existing step must be ticked, not duplicated")
	}
	if merged[1].Text != "write docs" || merged[1].Done {
		t.Fatalf("untouched step changed: %+v", merged[1])
	}
	if merged[2].Text != "run the suite" {
		t.Fatalf("new step not appended: %+v", merged[2])
	}
}

func TestMergeRespectsLimit(t *testing.T) {
	l := New()
	full := make([]Item, MaxItems)
	for i := range full {
		full[i] = Item{Text: "s" + strings.Repeat("x", i)}
	}
	l.Replace(full)
	merged := l.Merge([]Item{{Text: "one more"}})
	if len(merged) != MaxItems {
		t.Fatalf("len = %d, want the cap %d", len(merged), MaxItems)
	}
}

func TestCompleteToggles(t *testing.T) {
	l := New()
	l.Replace([]Item{{Text: "a"}, {Text: "b"}})
	if !l.Complete(0) {
		t.Fatal("index 0 must be valid")
	}
	if !l.Items()[0].Done {
		t.Fatal("item must be done")
	}
	l.Complete(0)
	if l.Items()[0].Done {
		t.Fatal("a second Complete must untick it")
	}
	if l.Complete(7) || l.Complete(-1) {
		t.Fatal("out-of-range indexes must be rejected")
	}
}

func TestItemsAreCopies(t *testing.T) {
	l := New()
	l.Replace([]Item{{Text: "a"}})
	got := l.Items()
	got[0].Text = "tampered"
	if l.Items()[0].Text != "a" {
		t.Fatal("Items must hand out copies, not the list's own storage")
	}
}

func TestRenderAndEmpty(t *testing.T) {
	if got := Render(nil); got != "(empty)" {
		t.Fatalf("empty render = %q", got)
	}
	l := New()
	if !l.Empty() {
		t.Fatal("a fresh list must be empty")
	}
	l.Replace([]Item{{Text: "one"}, {Text: "two", Done: true}})
	got := l.Render()
	if !strings.Contains(got, "[1] todo one") {
		t.Fatalf("render = %q, want the first step pending", got)
	}
	if !strings.Contains(got, "[2] done two") {
		t.Fatalf("render = %q, want the second step done", got)
	}
	l.Clear()
	if !l.Empty() {
		t.Fatal("Clear must empty the list")
	}
}

// TestConcurrentUseIsSafe: the list is written from the chat tool goroutine and
// read from the render loop, so this is the case that would bite in production.
// Run with -race.
func TestConcurrentUseIsSafe(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			l.Merge([]Item{{Text: "step", Done: i%2 == 0}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = l.Render()
			_ = l.Len()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = l.Items()
		}
	}()
	wg.Wait()
	if l.Len() != 1 {
		t.Fatalf("len = %d, want the single merged step", l.Len())
	}
}
