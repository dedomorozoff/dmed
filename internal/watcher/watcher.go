package watcher

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors file changes using fsnotify and delivers debounced change notifications.
type Watcher struct {
	fsw     *fsnotify.Watcher
	cb      func(path string)
	mu      sync.Mutex
	watched map[string]bool
	timers  map[string]*time.Timer
	done    chan struct{}
}

// New creates a new file Watcher with a callback for file modifications.
func New(callback func(path string)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsw:     fsw,
		cb:      callback,
		watched: make(map[string]bool),
		timers:  make(map[string]*time.Timer),
		done:    make(chan struct{}),
	}

	go w.loop()
	return w, nil
}

// Watch adds a file or directory path to the watch list.
func (w *Watcher) Watch(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watched[abs] {
		return nil
	}

	if err := w.fsw.Add(abs); err != nil {
		return err
	}
	w.watched[abs] = true
	return nil
}

// Unwatch removes a file or directory path from the watch list.
func (w *Watcher) Unwatch(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.watched[abs] {
		return nil
	}

	delete(w.watched, abs)
	if t, ok := w.timers[abs]; ok {
		t.Stop()
		delete(w.timers, abs)
	}
	return w.fsw.Remove(abs)
}

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				w.scheduleNotify(event.Name)
			}
		case <-w.fsw.Errors:
			// Ignore errors in loop
		}
	}
}

func (w *Watcher) scheduleNotify(name string) {
	abs, err := filepath.Abs(name)
	if err != nil {
		abs = name
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if t, exists := w.timers[abs]; exists {
		t.Stop()
	}

	w.timers[abs] = time.AfterFunc(80*time.Millisecond, func() {
		w.mu.Lock()
		delete(w.timers, abs)
		w.mu.Unlock()
		if w.cb != nil {
			w.cb(abs)
		}
	})
}

// Close stops the watcher and cleans up resources.
func (w *Watcher) Close() error {
	w.mu.Lock()
	select {
	case <-w.done:
		w.mu.Unlock()
		return nil
	default:
		close(w.done)
	}
	for _, t := range w.timers {
		t.Stop()
	}
	w.timers = make(map[string]*time.Timer)
	w.mu.Unlock()

	return w.fsw.Close()
}
