package debug

import "testing"

func TestCapturePanicReport(t *testing.T) {
	ran := false
	CapturePanicReport(func() {
		ran = true
		panic("boom")
	})
	if !ran {
		t.Fatal("wrapped function did not run")
	}
	// Recovery must let the caller continue normally.
	CapturePanicReport(func() {})
}

func TestCapturePanicReportPanicsRerun(t *testing.T) {
	n := 0
	for i := 0; i < 3; i++ {
		CapturePanicReport(func() {
			n++
			if n == 2 {
				panic("second run panics")
			}
		})
	}
	if n != 3 {
		t.Fatalf("n = %d, want 3", n)
	}
}
