package ptyterm

import (
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
