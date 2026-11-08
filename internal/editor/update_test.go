package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/update"
)

// TestUpdatePaletteCommands checks that the two update commands the editor
// ships are present in the palette and carry a real action. They are the only
// way a user reaches the background check and the apply, so a regression that
// drops them would leave the feature unreachable without a failing test.
func TestUpdatePaletteCommands(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	want := map[string]bool{
		"update_check": false,
		"update_apply": false,
	}
	for _, c := range m.getPaletteCommands() {
		if _, ok := want[c.id]; ok {
			want[c.id] = true
			if c.action == nil {
				t.Fatalf("%s command has no action", c.id)
			}
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("palette is missing the %s command", id)
		}
	}

	// Open the palette and type "update": both commands must surface.
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeStr(m, "update")
	hits := m.filterPalette()
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.id] = true
	}
	if !ids["update_check"] {
		t.Fatalf("filtering 'update' must surface update_check, got: %+v", hits)
	}
	if !ids["update_apply"] {
		t.Fatalf("filtering 'update' must surface update_apply, got: %+v", hits)
	}
}

// TestApplyUpdateWithoutRelease checks that asking for an apply when no
// release has been found reports the right message and does not touch disk.
func TestApplyUpdateWithoutRelease(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.updateAvailable = nil

	m.applyUpdate()
	if m.msg != m.t("msg.update_none") {
		t.Fatalf("apply with no release = %q, want %q", m.msg, m.t("msg.update_none"))
	}
}

// TestApplyUpdateCheckDropsStaleGeneration checks that a reply stamped with an
// older generation than the one the model holds is ignored, so a second
// request started while the first was in flight cannot clobber the answer of
// the newer one.
func TestApplyUpdateCheckDropsStaleGeneration(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.updateGen = 5

	m.applyUpdateCheck(updateCheckMsg{gen: 4, release: &update.Release{Version: "old"}})
	if m.updateAvailable != nil {
		t.Fatal("stale generation must be dropped")
	}

	m.applyUpdateCheck(updateCheckMsg{gen: 5, release: &update.Release{Version: "new"}})
	if m.updateAvailable == nil || m.updateAvailable.Version != "new" {
		t.Fatalf("current generation must apply, got %+v", m.updateAvailable)
	}
}
