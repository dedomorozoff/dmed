package editor

// bumpGen fires a generation counter and returns the fresh value. The editor
// invalidates stale asynchronous replies by stamping every session with a
// monotonically increasing generation: supersede a session (kill the
// terminal, stop the chat turn, restart the debug adapter) by bumping its
// counter, and every event stamped with an older value is dropped by the
// guards in Update. termGen, chatGen and dapGen all go through here so the
// convention stays in one place — a new asynchronous subsystem should reuse
// it, not hand-roll another pattern.
func bumpGen[T int | uint64](gen *T) T {
	*gen++
	return *gen
}
