package events

import (
	"sync"
)

// EventType identifies the category of an event.
type EventType string

const (
	EventFileChanged    EventType = "file:changed"
	EventBufferModified EventType = "buffer:modified"
	EventBufferSaved    EventType = "buffer:saved"
	EventGitUpdated     EventType = "git:updated"
)

// Event represents a system event.
type Event struct {
	Type    EventType
	Path    string
	Payload any
}

// Handler is a callback for handling events.
type Handler func(Event)

// Bus provides thread-safe publish/subscribe messaging across editor components.
type Bus struct {
	mu       sync.RWMutex
	handlers map[EventType][]Handler
}

// New creates a new Event Bus.
func New() *Bus {
	return &Bus{
		handlers: make(map[EventType][]Handler),
	}
}

// Subscribe adds a handler for a given event type.
func (b *Bus) Subscribe(t EventType, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[t] = append(b.handlers[t], h)
}

// Publish broadcasts an event to all subscribers of its type.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	handlers := append([]Handler(nil), b.handlers[e.Type]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		h(e)
	}
}
