package ptyterm

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPTYCommand(t *testing.T) {
	term, err := Start(testOptions())
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	defer term.Close()
	buf := make([]byte, 256)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := term.Read(buf)
		if n > 0 && strings.Contains(string(buf[:n]), "dmed-pty-ok") {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatal("PTY command did not produce expected output")
}

// TestMergeEnvReplacesRatherThanShadows is the hermeticity of the TUI harness:
// it points HOME at a temp dir, and on Windows an env block with two HOME
// entries resolves to the FIRST one — so appending the override left the
// developer's own home in force and the child silently read the real config.
func TestMergeEnvReplacesRatherThanShadows(t *testing.T) {
	t.Setenv("DMED_PT_TEST", "parent")

	out := mergeEnv([]string{"DMED_PT_TEST=child"})

	n := 0
	for _, kv := range out {
		if strings.HasPrefix(kv, "DMED_PT_TEST=") || (envFoldKeys && strings.HasPrefix(strings.ToUpper(kv), "DMED_PT_TEST=")) {
			n++
			if kv != "DMED_PT_TEST=child" {
				t.Errorf("the parent's value survived: %q", kv)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d entries for one name, want exactly 1 (a duplicate is resolved by position, and the position Windows honours is the first)", n)
	}
	// Nothing else may be dropped on the way.
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		want := name + "="
		found := false
		for _, got := range out {
			if strings.HasPrefix(got, want) || (envFoldKeys && strings.HasPrefix(strings.ToUpper(got), strings.ToUpper(want))) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mergeEnv dropped %q", name)
			break
		}
	}
}

func TestMergeEnvIgnoresMalformedEntries(t *testing.T) {
	before := len(os.Environ())
	out := mergeEnv([]string{"", "=value", "NOEQUALS", "DMED_PT_OK=1"})
	for _, kv := range out[before:] {
		if kv != "DMED_PT_OK=1" {
			t.Errorf("a malformed entry reached the child: %q", kv)
		}
	}
	if len(out) != before+1 {
		t.Errorf("len(out) = %d, want %d", len(out), before+1)
	}
}
