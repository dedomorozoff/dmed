package tui_test

import "testing"

// The [keys] section is a file one process writes and another reads, so what is
// worth proving here is the wiring between them — an in-process test builds its
// own model and cannot show that the shipped binary reaches the same code. Alt
// chords are out (ConPTY loses ESC+letter), so both scenarios drive ctrl keys,
// and the chords are chosen from the ones this terminal carries faithfully:
// ctrl+n, say, arrives on Windows as a bare Left-Ctrl press.

// TestKeysSectionSwapsTwoChords trades Ctrl+S and Ctrl+K over: the swapped chord
// writes the file, and the help overlay — generated from the resolved map, not
// copied out of the defaults — prints the chord the config asked for.
func TestKeysSectionSwapsTwoChords(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "seed.txt", "one\n")
	writeFile(t, dir, ".dmed.conf", "[keys]\nfile.save = ctrl+k\nlsp.hover = ctrl+s\n")

	s := Start(t, Options{Dir: dir, Args: []string{"seed.txt"}})
	s.WaitFor("one", settleWait)

	// Ask the running editor what it thinks the save key is before typing: the
	// help row is the config file as seen from inside the process.
	s.Press("ctrl+e")
	s.WaitFor("Ctrl+K", settleWait)
	s.Press("esc")
	s.WaitAbsent("dmed — keys", settleWait)

	s.Type("X")
	s.Press("ctrl+k")
	s.WaitFile("seed.txt", "Xone", settleWait)

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}

// TestProtectedChordRefusesAnUnbind is the failure mode the protection exists to
// prevent: a config file that takes the exit away. The line is refused out loud,
// and Ctrl+Q still quits.
func TestProtectedChordRefusesAnUnbind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "quit.txt", "body\n")
	writeFile(t, dir, ".dmed.conf", "[keys]\napp.quit =\n")

	s := Start(t, Options{Dir: dir, Args: []string{"quit.txt"}})
	s.WaitFor("body", settleWait)
	s.WaitFor("refused", settleWait)

	s.Press("ctrl+q")
	s.WaitExit(settleWait)
}
