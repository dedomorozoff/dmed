package editor

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseRemotePluginList(t *testing.T) {
	body, _ := json.Marshal([]map[string]string{
		{"name": "emmet.lua", "type": "file"},
		{"name": "snippets.lua", "type": "file"},
		{"name": "README.md", "type": "file"},
		{"name": "subdir", "type": "dir"},
		{"name": "notes.LUA", "type": "file"}, // case-insensitive suffix
	})
	items, err := parseRemotePluginList(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.File)
	}
	want := []string{"emmet.lua", "snippets.lua", "notes.LUA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, it := range items {
		if !it.Remote {
			t.Errorf("%s should be marked remote", it.File)
		}
	}
}

func TestParseRemotePluginListErrors(t *testing.T) {
	if _, err := parseRemotePluginList([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestStoreEmbeddedItemsSeeded(t *testing.T) {
	items := storeEmbeddedItems()
	if len(items) == 0 {
		t.Fatal("no embedded items")
	}
	for _, it := range items {
		if it.Remote {
			t.Errorf("%s should not be marked remote", it.File)
		}
	}
}
