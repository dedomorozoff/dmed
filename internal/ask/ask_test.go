package ask

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRequestNormalizes(t *testing.T) {
	req := NewRequest("1", "  which database?  ", []string{" postgres ", "", "sqlite"}, time.Time{})
	if req.Question != "which database?" {
		t.Fatalf("question = %q", req.Question)
	}
	if len(req.Choices) != 2 {
		t.Fatalf("choices = %v, want the empty one dropped", req.Choices)
	}
	if req.Choices[0] != "postgres" {
		t.Fatalf("choice not trimmed: %q", req.Choices[0])
	}
}

func TestNewRequestCaps(t *testing.T) {
	var choices []string
	for i := 0; i < MaxChoices+5; i++ {
		choices = append(choices, strings.Repeat("x", i+1))
	}
	req := NewRequest("1", strings.Repeat("q", MaxQuestionLen+10), choices, time.Time{})
	if r := []rune(req.Question); len(r) != MaxQuestionLen {
		t.Fatalf("question kept %d runes, want %d", len(r), MaxQuestionLen)
	}
	if len(req.Choices) != MaxChoices {
		t.Fatalf("choices = %d, want cap %d", len(req.Choices), MaxChoices)
	}
	for _, c := range req.Choices {
		if r := []rune(c); len(r) > MaxChoiceLen {
			t.Fatalf("choice %q exceeds %d runes", c, MaxChoiceLen)
		}
	}
}

func TestQueueOrdersAndResolves(t *testing.T) {
	q := NewQueue()
	first := q.Next("first?", nil, time.Time{})
	second := q.Next("second?", nil, time.Time{})

	if first.ID == second.ID {
		t.Fatal("ids must differ")
	}
	if got := q.Pending(); len(got) != 2 || got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("pending = %v, want oldest first", got)
	}
	if !q.Answer(first.ID, "yes") {
		t.Fatal("Answer must accept a pending id")
	}
	if q.Answer(first.ID, "again") {
		t.Fatal("an answered question must not resolve twice")
	}
	if q.Len() != 1 {
		t.Fatalf("len = %d, want 1", q.Len())
	}
	if !q.Cancel(second.ID) {
		t.Fatal("Cancel must drop a pending question")
	}
	if q.Len() != 0 {
		t.Fatalf("len = %d, want 0", q.Len())
	}
}

func TestQueueConcurrentUseIsSafe(t *testing.T) {
	q := NewQueue()
	var wg sync.WaitGroup
	wg.Add(2)
	ids := make(chan string, 100)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ids <- q.Next("q?", []string{"a", "b"}, time.Time{}).ID
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = q.Len()
			_ = q.Pending()
		}
	}()
	wg.Wait()
	close(ids)
	n := 0
	for id := range ids {
		q.Answer(id, "ok")
		n++
	}
	if q.Len() != 0 {
		t.Fatalf("len = %d after answering %d questions", q.Len(), n)
	}
}
