package events

import (
	"testing"
)

func TestBusPublishSubscribe(t *testing.T) {
	bus := New()
	var received []Event

	bus.Subscribe(EventFileChanged, func(e Event) {
		received = append(received, e)
	})

	bus.Publish(Event{Type: EventFileChanged, Path: "main.go"})
	bus.Publish(Event{Type: EventBufferSaved, Path: "test.go"})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Path != "main.go" {
		t.Fatalf("expected path main.go, got %s", received[0].Path)
	}
}
