package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherFileChange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(f, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	notified := make(chan string, 1)
	w, err := New(func(path string) {
		notified <- path
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Watch(f); err != nil {
		t.Fatal(err)
	}

	// Modify file
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(f, []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case p := <-notified:
		absF, _ := filepath.Abs(f)
		if p != absF {
			t.Fatalf("expected path %s, got %s", absF, p)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for file modification event")
	}
}
