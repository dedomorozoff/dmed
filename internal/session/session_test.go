package session

import (
	"path/filepath"
	"testing"
)

func TestSessionSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "session.json")

	initial := SessionState{
		Root:       dir,
		Files:      []string{"a.go", "b.go"},
		ActiveTab:  1,
		Layout:     1,
		ActivePane: 0,
	}

	if err := Save(sessPath, initial); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(sessPath)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Root != initial.Root || len(loaded.Files) != 2 || loaded.ActiveTab != 1 {
		t.Fatalf("loaded session mismatch: %+v", loaded)
	}
}
