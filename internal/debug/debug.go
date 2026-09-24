// Package debug provides crash-safety helpers for background goroutines.
//
// A panic anywhere in a spawned goroutine would otherwise terminate the whole
// process. Every goroutine the editor starts should run under
// CapturePanicReport so the panic is logged instead of taking the process down.
package debug

import (
	"log"
	"runtime"
)

// CapturePanicReport runs fn inside a recovered frame. If fn panics, the panic
// value and a stack trace are written to the standard logger and execution
// continues in the caller's goroutine.
func CapturePanicReport(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 1<<16)
			n := runtime.Stack(buf, false)
			log.Printf("panic captured: %v\n%s", r, buf[:n])
		}
	}()
	fn()
}
