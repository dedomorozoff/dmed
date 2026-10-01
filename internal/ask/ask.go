// Package ask holds questions the AI asks the user while a tool loop is
// parked: the question text, the suggested choices and the answer.
//
// Like internal/todo it is a thread-safe value holder with no TUI dependency —
// questions are pushed from the chat tool goroutine and answered from the key
// loop, so the state has to be safe to share and the queue has to survive a
// round that asked more than one question.
package ask

import (
	"strings"
	"sync"
	"time"
)

// MaxQuestionLen and MaxChoices keep a question renderable: the overlay is one
// or two lines above the status bar, not a scrolling document.
const (
	MaxQuestionLen = 400
	MaxChoices     = 6
	MaxChoiceLen   = 120
)

// Request is one question awaiting an answer.
type Request struct {
	ID       string    // stable id, unique per request
	Question string    // what the model wants to know
	Choices  []string  // optional suggestions; an empty list means free-form input
	Asked    time.Time // when the question was raised
}

// NewRequest normalizes and bounds a question: whitespace trimmed, question and
// choices length-capped, empty choices dropped and the list capped.
func NewRequest(id, question string, choices []string, now time.Time) Request {
	q := strings.TrimSpace(question)
	if r := []rune(q); len(r) > MaxQuestionLen {
		q = string(r[:MaxQuestionLen])
	}
	var out []string
	for _, c := range choices {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if r := []rune(c); len(r) > MaxChoiceLen {
			c = string(r[:MaxChoiceLen])
		}
		out = append(out, c)
		if len(out) == MaxChoices {
			break
		}
	}
	return Request{ID: id, Question: q, Choices: out, Asked: now}
}

// Queue is the set of questions still unanswered, oldest first. It exists so a
// second question raised while the first is on screen is not lost.
type Queue struct {
	mu   sync.Mutex
	seq  int
	reqs []Request
}

// NewQueue returns an empty queue.
func NewQueue() *Queue { return &Queue{} }

// Next returns the next question, assigning it an id on the way out. The
// request is kept in the queue so Pending can still report it.
func (q *Queue) Next(question string, choices []string, now time.Time) Request {
	q.mu.Lock()
	q.seq++
	req := NewRequest(itoa(q.seq), question, choices, now)
	q.reqs = append(q.reqs, req)
	q.mu.Unlock()
	return req
}

// Answer resolves the request with the given id, recording what the user said.
// It reports whether the id was still pending.
func (q *Queue) Answer(id, answer string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, r := range q.reqs {
		if r.ID == id {
			q.reqs = append(q.reqs[:i], q.reqs[i+1:]...)
			return true
		}
	}
	return false
}

// Cancel drops the request without an answer (Esc).
func (q *Queue) Cancel(id string) bool { return q.Answer(id, "") }

// Pending returns the ids of the questions still waiting, in order.
func (q *Queue) Pending() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.reqs))
	for _, r := range q.reqs {
		out = append(out, r.ID)
	}
	return out
}

// Len returns the number of pending questions.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.reqs)
}

// itoa avoids pulling strconv in for one counter.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
