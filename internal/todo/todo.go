// Package todo holds the checklist the AI keeps while it works: the short list
// of steps it intends to take, which it shows the user and ticks off as it goes.
//
// The package knows nothing about the TUI — it is a thread-safe value holder,
// like internal/agent's queue, because it is written from the chat tool
// goroutine and read from the render loop. It deliberately does not persist
// anything: the list belongs to the conversation, not to the project.
package todo

import (
	"fmt"
	"strings"
	"sync"
)

// Limits keep a runaway model from filling the context window or the panel.
const (
	// MaxItems is the number of steps kept; further items are dropped.
	MaxItems = 50
	// MaxItemLen caps one step in runes.
	MaxItemLen = 200
)

// Item is one step. Done is the tick state the model flips via Merge.
type Item struct {
	Text string
	Done bool
}

// List is a thread-safe ordered checklist. The zero value is not usable; call
// New. Every accessor hands out copies, so a caller can never mutate the list's
// own storage (the rule internal/agent's Queue follows).
type List struct {
	mu    sync.Mutex
	items []Item
}

// New returns an empty list.
func New() *List { return &List{} }

// sanitize normalizes incoming items: trimmed, non-empty, length-capped, and
// truncated to MaxItems.
func sanitize(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > MaxItemLen {
			text = string(r[:MaxItemLen])
		}
		out = append(out, Item{Text: text, Done: it.Done})
		if len(out) == MaxItems {
			break
		}
	}
	return out
}

// Replace swaps the whole list, which is what a fresh plan does. Returns the
// number of steps kept and how many were dropped by the cap.
func (l *List) Replace(items []Item) (kept, dropped int) {
	clean := sanitize(items)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = clean
	return len(clean), len(items) - len(clean)
}

// Merge adds new steps and ticks off known ones: an item whose text matches an
// existing step (case-insensitively) updates its Done state instead of being
// appended, so a model that reports "tests added" twice does not produce two
// identical steps. Returns the resulting list.
func (l *List) Merge(items []Item) []Item {
	clean := sanitize(items)
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range clean {
		found := false
		for i := range l.items {
			if strings.EqualFold(l.items[i].Text, it.Text) {
				l.items[i].Done = it.Done
				found = true
				break
			}
		}
		if found || len(l.items) >= MaxItems {
			continue
		}
		l.items = append(l.items, it)
	}
	return l.snapshotLocked()
}

// Complete ticks one step by index (0-based, as the model sees it numbered
// from 1). It reports whether the index was valid.
func (l *List) Complete(idx int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if idx < 0 || idx >= len(l.items) {
		return false
	}
	l.items[idx].Done = !l.items[idx].Done
	return true
}

// Items returns a copy of the list.
func (l *List) Items() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

// Len returns the number of steps.
func (l *List) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// Empty reports whether there is nothing to show.
func (l *List) Empty() bool { return l.Len() == 0 }

// Clear drops the whole list (a new conversation starts without a plan).
func (l *List) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = nil
}

// Render is the compact form both the chat panel and the model see: one line
// per step, numbered from 1, with the tick state spelled out.
func (l *List) Render() string {
	return Render(l.Items())
}

// Render formats items as the tool result and the panel do.
func Render(items []Item) string {
	if len(items) == 0 {
		return "(empty)"
	}
	var b strings.Builder
	for i, it := range items {
		state := "todo"
		if it.Done {
			state = "done"
		}
		fmt.Fprintf(&b, "[%d] %s %s", i+1, state, it.Text)
		if i < len(items)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (l *List) snapshotLocked() []Item {
	if l.items == nil {
		return nil
	}
	out := make([]Item, len(l.items))
	copy(out, l.items)
	return out
}
