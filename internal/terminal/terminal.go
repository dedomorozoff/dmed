// Package terminal detects the glyph/encoding capabilities of the terminal
// the editor is running in. Old terminals — legacy Windows conhost in a
// non-UTF-8 code page, dumb/unknown unix terminals — cannot render the
// Unicode box-drawing and symbol glyphs the UI uses natively, so the caller
// falls back to ASCII rendering. Modern terminals are never degraded.
package terminal

import "strings"

// Compat describes the rendering capability the editor should target.
type Compat struct {
	// Ascii forces ASCII fallback glyphs (no box drawing, no §/▸/· symbols).
	Ascii bool
	// Reason records why, for debugging and config diagnostics.
	Reason string
}

// limitedTERM lists TERM values of terminals that predate reliable Unicode
// box-drawing output (BSD/VT consoles, ANSI emulators).
var limitedTERM = map[string]bool{
	"cons25": true, "cons25l1": true, "cons25r": true,
	"amiga": true, "mach": true, "mach-bold": true,
	"ansi": true, "ansi-80": true, "vt100": true, "vt102": true,
	"vt200": true, "vt220": true,
}

// EnvMap converts os.Environ() into a lookup map.
func EnvMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// DetectCompat decides whether the UI must fall back to ASCII rendering.
//
// override is the "[ui] ascii" setting: "auto" (the default) uses heuristics,
// "on" forces ASCII even on modern terminals, "off" forces Unicode even when
// the terminal looks limited. env is the environment (name -> value), goos is
// runtime.GOOS.
func DetectCompat(override string, env map[string]string, goos string) Compat {
	switch override {
	case "", "auto":
	case "on", "1", "true", "yes":
		return Compat{Ascii: true, Reason: "config forces ASCII"}
	case "off", "0", "false", "no":
		return Compat{Ascii: false, Reason: "config forces Unicode"}
	}

	term := strings.ToLower(env["TERM"])
	if term == "dumb" {
		return Compat{Ascii: true, Reason: "TERM=dumb"}
	}

	if goos == "windows" {
		// Legacy conhost (cmd.exe from Explorer, a bare PowerShell) sets none
		// of the markers every modern Windows host sets. Windows Terminal,
		// VS Code, mintty, ConEmu/Cmder and friends all expose at least one.
		if term == "" && env["WT_SESSION"] == "" && env["WT_PROFILE_ID"] == "" &&
			env["TERM_PROGRAM"] == "" && env["CONEMUPID"] == "" && env["CMDER_ROOT"] == "" {
			return Compat{Ascii: true, Reason: "legacy Windows console (no UTF-8/Unicode guarantee)"}
		}
		if term == "" {
			// Modern Windows terminal with TERM simply unset.
			return Compat{Ascii: false, Reason: "modern Windows terminal"}
		}
	}

	if term == "" || limitedTERM[term] {
		return Compat{Ascii: true, Reason: "unknown or limited TERM=" + term}
	}
	return Compat{Ascii: false, Reason: "modern terminal (TERM=" + term + ")"}
}
