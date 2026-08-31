package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// CursorPos records a buffer cursor position (0-based line and column).
type CursorPos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// SessionState captures the editor layout, open files, and cursor positions.
type SessionState struct {
	Root       string               `json:"root,omitempty"`
	Files      []string             `json:"files"`
	ActiveTab  int                  `json:"active_tab"`
	Layout     int                  `json:"layout"`
	ActivePane int                  `json:"active_pane"`
	Cursors    map[string]CursorPos `json:"cursors,omitempty"`
}

// DefaultPath returns the default session file path for a project root or user home.
func DefaultPath(root string) string {
	if root != "" {
		return filepath.Join(root, ".dmed_session.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".dmed_session.json"
	}
	return filepath.Join(home, ".dmed_session.json")
}

// Save writes the session state to path.
func Save(path string, state SessionState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load reads and parses a session state from path.
func Load(path string) (*SessionState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
