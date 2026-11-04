package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dmed/internal/agent"
	"dmed/internal/config"
)

func storeHarness(t *testing.T) Model {
	t.Helper()
	m := Model{root: t.TempDir()}
	m.cfg = config.Defaults()
	m.agentQueue = agent.NewQueue(nil)
	return m
}

func TestStoreViewMarksInFlightTasksInterrupted(t *testing.T) {
	now := time.Now()
	view := storeView([]agent.Task{
		{ID: "t1", Prompt: "a", Status: agent.StatusRunning, Updated: now},
		{ID: "t2", Prompt: "b", Status: agent.StatusQueued, Updated: now},
		{ID: "t3", Prompt: "c", Status: agent.StatusReview,
			Changes: []agent.Change{{Path: "x.go", Orig: "old", New: "new"}}, Updated: now},
	})
	if view[0].Status != agent.StatusCancelled || !strings.Contains(view[0].Error, "editor exited") {
		t.Fatalf("a running task must be stored as interrupted: %+v", view[0])
	}
	if view[1].Status != agent.StatusCancelled {
		t.Fatalf("a queued task cannot survive the editor: %+v", view[1])
	}
	// A review survives whole — that is the point of the file.
	if view[2].Status != agent.StatusReview || len(view[2].Changes) != 1 {
		t.Fatalf("the review was lost: %+v", view[2])
	}
}

func TestAgentStoreRoundTrip(t *testing.T) {
	m := storeHarness(t)
	m.agentQueue.Restore([]agent.Task{{
		ID: "t1104-101010-1", Prompt: "refactor x", Status: agent.StatusReview,
		Changes: []agent.Change{{Path: "a.go", Orig: "one\n", New: "two\n"}},
		Created: time.Now(), Updated: time.Now(),
	}})
	m.agentQueue.Enqueue("second task") // queued → stored as cancelled-interrupted
	m.saveAgentStore()

	data, err := os.ReadFile(agentStorePath(m.root))
	if err != nil {
		t.Fatalf("store not written: %v", err)
	}
	if !strings.Contains(string(data), "refactor x") {
		t.Fatalf("store content = %s", data)
	}

	// A fresh session over the same root restores both rows.
	m2 := storeHarness(t)
	m2.root = m.root
	m2.agentQueue = agent.NewQueue(nil)
	m2.loadAgentStore(m2.agentQueue)
	tasks := m2.agentQueue.Snapshot()
	if len(tasks) != 2 {
		t.Fatalf("restored %d tasks, want 2", len(tasks))
	}
	if tasks[0].ID != "t1104-101010-1" || tasks[0].Status != agent.StatusReview {
		t.Fatalf("restored review = %+v", tasks[0])
	}
	if len(tasks[0].Changes) != 1 || tasks[0].Changes[0].New != "two\n" {
		t.Fatalf("restored changes lost: %+v", tasks[0].Changes)
	}
	if tasks[1].Status != agent.StatusCancelled {
		t.Fatalf("the interrupted task = %+v", tasks[1])
	}
	// A new task after a restore does not reuse a restored ID.
	fresh := m2.agentQueue.Enqueue("third")
	for _, tk := range tasks {
		if tk.ID == fresh.ID {
			t.Fatalf("ID collision between restored %s and fresh", fresh.ID)
		}
	}
}

func TestSaveAgentStoreWritesOnlyOnDurableChange(t *testing.T) {
	m := storeHarness(t)
	tk := m.agentQueue.Enqueue("work")
	m.saveAgentStore()
	path := agentStorePath(m.root)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Progress moves on every stream tick; the durable shape does not, and
	// the file must not be rewritten for it.
	m.agentQueue.SetProgress(tk.ID, 0.5)
	if err := os.WriteFile(path, append(first, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	m.saveAgentStore()
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first)+" " {
		t.Fatal("a progress-only refresh must not rewrite the store")
	}
	// A status change does rewrite it.
	m.agentQueue.SetStatus(tk.ID, agent.StatusDone)
	m.saveAgentStore()
	third, _ := os.ReadFile(path)
	if strings.Contains(string(third), `"status": "queued"`) {
		t.Fatal("the status change was not persisted")
	}
}

func TestAgentStoreDisabledAndCorrupt(t *testing.T) {
	m := storeHarness(t)
	m.cfg.Agent.Persist = false
	m.agentQueue.Enqueue("x")
	m.saveAgentStore()
	if _, err := os.Stat(agentStorePath(m.root)); !os.IsNotExist(err) {
		t.Fatal("persist = false must not write the file")
	}

	m2 := storeHarness(t)
	if err := os.WriteFile(filepath.Join(m2.root, agentStoreFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2.loadAgentStore(m2.agentQueue)
	if len(m2.agentQueue.Snapshot()) != 0 {
		t.Fatal("a corrupt store must start the queue empty, not fail the editor")
	}
}

func TestRetryAgentSelStartsANewTask(t *testing.T) {
	m := storeHarness(t)
	failed := m.agentQueue.Enqueue("fix it")
	m.agentQueue.Next()
	m.agentQueue.SetError(failed.ID, "provider down")
	m.agentSel = 0
	m.retryAgentSel()
	tasks := m.agentQueue.Snapshot()
	if len(tasks) != 2 {
		t.Fatalf("retry must add a task, got %d", len(tasks))
	}
	if tasks[1].Prompt != "fix it" || tasks[1].Status != agent.StatusQueued {
		t.Fatalf("retried task = %+v", tasks[1])
	}
	// The interrupted original keeps its row and its error.
	if tasks[0].Status != agent.StatusFailed || tasks[0].Error != "provider down" {
		t.Fatalf("the original was rewritten: %+v", tasks[0])
	}

	// A delegated task is refused: its chat is long gone.
	sub := m.agentQueue.EnqueueSub("delegate", "chat:t")
	m.agentSel = len(tasks)
	m.agentQueue.SetError(sub.ID, "interrupted")
	m.retryAgentSel()
	if len(m.agentQueue.Snapshot()) != 3 {
		t.Fatal("a sub task must not be retried as a plain agent")
	}

	// A running task cannot be retried (it is not finished).
	running := m.agentQueue.Enqueue("busy")
	m.agentQueue.Next()
	m.agentSel = len(m.agentQueue.Snapshot()) - 1
	m.retryAgentSel()
	if len(m.agentQueue.Snapshot()) != 4 {
		t.Fatalf("a running task must not be re-queued, got %d", len(m.agentQueue.Snapshot()))
	}
	if _, found := m.agentQueue.Find(running.ID); !found {
		t.Fatal("the running task vanished")
	}
}
