package terminal

import "testing"

func TestDetectCompat(t *testing.T) {
	cases := []struct {
		name     string
		override string
		env      map[string]string
		goos     string
		want     bool
	}{
		// Config overrides win over everything.
		{"override off on limited env", "off", map[string]string{"TERM": "dumb"}, "linux", false},
		{"override on on modern env", "on", map[string]string{"TERM": "xterm-256color"}, "linux", true},
		{"auto treated as heuristic", "auto", map[string]string{"TERM": "xterm-256color"}, "linux", false},

		// Unix.
		{"linux modern term", "auto", map[string]string{"TERM": "xterm-256color"}, "linux", false},
		{"linux tmux", "auto", map[string]string{"TERM": "tmux-256color"}, "linux", false},
		{"linux no term", "auto", map[string]string{}, "linux", true},
		{"linux dumb term", "auto", map[string]string{"TERM": "dumb"}, "linux", true},
		{"linux limited term", "auto", map[string]string{"TERM": "cons25"}, "linux", true},
		{"linux vt100", "auto", map[string]string{"TERM": "vt100"}, "linux", true},
		{"linux case insensitivity", "auto", map[string]string{"TERM": "DUMB"}, "linux", true},

		// Windows.
		{"win legacy conhost", "auto", map[string]string{}, "windows", true},
		{"win windows terminal", "auto", map[string]string{"WT_SESSION": "abc123"}, "windows", false},
		{"win vscode", "auto", map[string]string{"TERM_PROGRAM": "vscode"}, "windows", false},
		{"win conemu", "auto", map[string]string{"CONEMUPID": "1234"}, "windows", false},
		{"win cmder", "auto", map[string]string{"CMDER_ROOT": "C:\\cmder"}, "windows", false},
		{"win mintty term", "auto", map[string]string{"TERM": "xterm-256color"}, "windows", false},
		{"win legacy with TERM", "auto", map[string]string{"TERM": "cons25"}, "windows", true},
	}
	for _, c := range cases {
		got := DetectCompat(c.override, c.env, c.goos)
		if got.Ascii != c.want {
			t.Errorf("%s: Ascii = %v (reason %q), want %v", c.name, got.Ascii, got.Reason, c.want)
		}
	}
}

func TestEnvMap(t *testing.T) {
	m := EnvMap([]string{"TERM=xterm", "PATH=/usr/bin", "empty=", "noequals"})
	if m["TERM"] != "xterm" || m["PATH"] != "/usr/bin" {
		t.Fatalf("EnvMap = %v", m)
	}
	if _, ok := m["empty"]; !ok {
		t.Fatalf("EnvMap must keep empty values: %v", m)
	}
	if _, ok := m["noequals"]; ok {
		t.Fatalf("EnvMap must drop entries without '=': %v", m)
	}
}
