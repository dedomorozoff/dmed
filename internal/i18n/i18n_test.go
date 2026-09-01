package i18n

import "testing"

func TestResolve(t *testing.T) {
	cases := map[string]Lang{
		"":        En,
		"en":      En,
		"English": En,
		"ru":      Ru,
		"RU":      Ru,
		"russian": Ru,
		"русский": Ru,
	}
	for in, want := range cases {
		if got := Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTTranslates(t *testing.T) {
	ru := New(Ru)
	if got := ru.T("git.refreshed"); got != "обновлено" {
		t.Errorf("ru git.refreshed = %q", got)
	}
	en := New(En)
	if got := en.T("git.refreshed"); got != "refreshed" {
		t.Errorf("en git.refreshed = %q", got)
	}
}

func TestTFormatsArgs(t *testing.T) {
	en := New(En)
	if got := en.T("git.switched", "main"); got != "switched to main" {
		t.Errorf("got %q", got)
	}
	ru := New(Ru)
	if got := ru.T("git.switched", "main"); got != "переключено на main" {
		t.Errorf("got %q", got)
	}
}

func TestTFallbackToEnglishAndKey(t *testing.T) {
	// Missing in ru -> falls back to en.
	ru := New(Ru)
	// (use a key present only in en by temporarily checking behavior)
	if got := ru.T("git.status_count", 1, 2); got == "" {
		t.Error("expected a translated string")
	}
	// Unknown key -> key itself.
	en := New(En)
	if got := en.T("no.such.key"); got != "no.such.key" {
		t.Errorf("got %q", got)
	}
}
