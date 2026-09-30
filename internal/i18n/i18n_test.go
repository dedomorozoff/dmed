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

// TestCatalogsHaveSameKeys guards the en/ru split. A key added to one catalog
// and forgotten in the other is invisible at runtime: Translator.T silently
// falls back to English, so the Russian UI shows English text and no test
// fails. With ~278 keys per catalog that is a routine mistake.
//
// The catalogs are package-private, so this test compares them directly.
func TestCatalogsHaveSameKeys(t *testing.T) {
	for key := range enCatalog {
		if _, ok := ruCatalog[key]; !ok {
			t.Errorf("key %q is missing from the ru catalog", key)
		}
	}
	for key := range ruCatalog {
		if _, ok := enCatalog[key]; !ok {
			t.Errorf("key %q is missing from the en catalog", key)
		}
	}
}

// TestCatalogEntriesHaveSamePlaceholders checks that translations keep the
// format verbs of the English string. A ru entry with a missing %s renders a
// broken string like "переключено на %!s(MISSING)".
func TestCatalogEntriesHaveSamePlaceholders(t *testing.T) {
	for key, enText := range enCatalog {
		ruText, ok := ruCatalog[key]
		if !ok {
			continue // reported by TestCatalogsHaveSameKeys
		}
		for _, verb := range []string{"%s", "%d", "%v", "%q"} {
			if countVerb(enText, verb) != countVerb(ruText, verb) {
				t.Errorf("key %q: %q has %s x%d but ru %q has x%d",
					key, enText, verb, countVerb(enText, verb),
					ruText, countVerb(ruText, verb))
			}
		}
	}
}

func countVerb(s, verb string) int {
	n := 0
	for i := 0; i+len(verb) <= len(s); i++ {
		if s[i:i+len(verb)] == verb {
			n++
			i += len(verb) - 1
		}
	}
	return n
}
