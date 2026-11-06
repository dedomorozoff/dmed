package editor

import (
	"fmt"
	"strconv"
	"strings"
)

// termResponder answers the terminal-capability queries a child program sends
// through the pseudo-terminal. The embedded screen (vt10x) can parse everything
// the child paints, but a real terminal also writes back, and some programs
// stop dead until they hear an answer: fish asks for the primary device
// attributes (CSI c) before it paints its first prompt and waits ten seconds for
// a reply, so an unanswered query is the difference between an instant shell
// and one that looks hung. zsh and the prompt frameworks that go with it have
// the same habit with their own queries.
//
// It is deliberately narrow. Only the queries a program waits on are answered —
// device attributes and the terminal version, the cursor position, the keyboard
// mode, the background/foreground colour, and the capability lookups — and the
// answers describe what this panel actually is: an ANSI screen with a dark
// background, no sixel, no Kitty keyboard flags. Everything else stays
// unanswered, exactly as a terminal that does not implement it would. A short
// tail is carried between reads so a sequence split by the 32 KiB read boundary
// is still answered whole.
type termResponder struct {
	tail []byte
}

// termQueryTail is the longest incomplete sequence carried between reads. A
// capability query is a few dozen bytes; anything longer is not one, and
// holding it would only grow the buffer.
const termQueryTail = 128

// feed consumes one chunk of child output and returns the bytes a terminal
// would write back. cursor reports the 0-based caret position for a
// cursor-position report (CSI 6 n); it may be nil when no screen is available.
func (r *termResponder) feed(chunk []byte, cursor func() (row, col int)) []byte {
	data := chunk
	if len(r.tail) > 0 {
		data = append(append([]byte(nil), r.tail...), chunk...)
		r.tail = r.tail[:0]
	}
	var out []byte
	for i := 0; i < len(data); {
		if data[i] != 0x1b {
			i++
			continue
		}
		if i+1 >= len(data) {
			r.keep(data[i:])
			break
		}
		switch data[i+1] {
		case '[': // CSI: parameter bytes, intermediate bytes, one final byte
			j := i + 2
			for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
				j++
			}
			for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
				j++
			}
			if j >= len(data) {
				r.keep(data[i:])
				i = len(data)
				break
			}
			if data[j] < 0x40 || data[j] > 0x7e {
				i++ // not a CSI after all; rescan from the next byte
				continue
			}
			out = append(out, r.csi(data[i+2:j], data[j], cursor)...)
			i = j + 1
		case ']': // OSC: payload terminated by BEL or ST
			end, next := oscEnd(data, i+2)
			if end < 0 {
				r.keep(data[i:])
				i = len(data)
				break
			}
			out = append(out, r.osc(string(data[i+2:end]))...)
			i = next
		case 'P': // DCS: payload terminated by ST
			end, next := dcsEnd(data, i+2)
			if end < 0 {
				r.keep(data[i:])
				i = len(data)
				break
			}
			out = append(out, r.dcs(string(data[i+2:end]))...)
			i = next
		default:
			// Two-byte escapes (ESC 7, ESC =) and the three-byte charset
			// designators: skipping the pair leaves a harmless byte behind.
			i += 2
		}
	}
	return out
}

// keep remembers an unfinished sequence for the next read, unless it is longer
// than any query could be.
func (r *termResponder) keep(tail []byte) {
	if len(tail) > termQueryTail {
		return
	}
	r.tail = append(r.tail[:0], tail...)
}

// csi answers one control sequence. params holds everything between "ESC [" and
// the final byte; marker is the private-parameter byte ('?', '>', '='), and a
// '$' intermediate marks the DEC request-status family.
func (r *termResponder) csi(raw []byte, final byte, cursor func() (row, col int)) []byte {
	marker := byte(0)
	if len(raw) > 0 && (raw[0] == '?' || raw[0] == '>' || raw[0] == '=' || raw[0] == '<') {
		marker = raw[0]
		raw = raw[1:]
	}
	inter := ""
	if k := indexIntermediate(raw); k >= 0 {
		inter = string(raw[k:])
		raw = raw[:k]
	}
	nums := parseCSINums(raw)
	switch final {
	case 'c':
		switch marker {
		case '>':
			return []byte("\x1b[>0;0;0c") // secondary DA: VT100-compatible firmware
		case '=':
			return nil // tertiary DA: nothing waits on it
		default:
			return []byte("\x1b[?1;2c") // primary DA: VT100 with advanced video
		}
	case 'n':
		if marker != 0 || inter != "" {
			break
		}
		if len(nums) == 1 && nums[0] == 6 && cursor != nil {
			row, col := cursor()
			return []byte(fmt.Sprintf("\x1b[%d;%dR", row+1, col+1))
		}
		if len(nums) == 1 && nums[0] == 5 {
			return []byte("\x1b[0n") // device status: ready
		}
	case 'u':
		if marker == '?' {
			return []byte("\x1b[?0u") // Kitty keyboard: legacy flags, none pushed
		}
	case 'q':
		if marker == '>' && inter == "" && (len(nums) == 0 || (len(nums) == 1 && nums[0] == 0)) {
			return []byte("\x1bP>|dmed\x1b\\") // XTVERSION
		}
	case 'p':
		if marker == '?' && inter == "$" {
			// DECRQM: every requested mode is reported unrecognised, so a
			// program never assumes a mode this screen does not implement.
			var b strings.Builder
			for _, n := range nums {
				fmt.Fprintf(&b, "\x1b[?%d;0$y", n)
			}
			return []byte(b.String())
		}
	}
	return nil
}

// osc answers a colour query. The panel paints on the terminal's own
// background, so the honest answer is a dark screen with light text; a program
// that asks only wants to pick a matching palette.
func (r *termResponder) osc(payload string) []byte {
	switch payload {
	case "10;?":
		return []byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	case "11;?":
		return []byte("\x1b]11;rgb:0000/0000/0000\x1b\\")
	}
	return nil
}

// dcs answers the two capability lookups (XTGETTCAP and DECRQSS). Both are
// reported unsupported rather than left hanging; a program that asks is one
// that would otherwise wait for its timeout.
func (r *termResponder) dcs(payload string) []byte {
	switch {
	case strings.HasPrefix(payload, "+q"):
		return []byte("\x1bP0+r" + payload[2:] + "\x1b\\")
	case strings.HasPrefix(payload, "$q"):
		return []byte("\x1bP0$r" + payload[2:] + "\x1b\\")
	}
	return nil
}

// indexIntermediate finds the first intermediate byte (0x20–0x2f) in a CSI
// parameter string, or -1.
func indexIntermediate(raw []byte) int {
	for i, b := range raw {
		if b >= 0x20 && b <= 0x2f {
			return i
		}
	}
	return -1
}

// parseCSINums splits "1;2;3" into numbers. An empty parameter means zero, and
// a sub-parameter (a ':' SGR colour) returns nil: no query this responder
// answers carries one.
func parseCSINums(raw []byte) []int {
	if len(raw) == 0 {
		return nil
	}
	parts := strings.Split(string(raw), ";")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			nums = append(nums, 0)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}

// oscEnd finds the end of an OSC payload that starts at i: BEL or ST. Both end
// indexes are -1 when the payload does not end in this chunk.
func oscEnd(data []byte, i int) (end, next int) {
	for k := i; k < len(data); k++ {
		switch data[k] {
		case 0x07:
			return k, k + 1
		case 0x1b:
			if k+1 < len(data) && data[k+1] == '\\' {
				return k, k + 2
			}
			// An ESC inside an OSC that is not ST is malformed: leave the
			// sequence unfinished rather than guess where it ends.
			return -1, -1
		}
	}
	return -1, -1
}

// dcsEnd finds the end of a DCS payload that starts at i: ST only.
func dcsEnd(data []byte, i int) (end, next int) {
	for k := i; k < len(data); k++ {
		if data[k] != 0x1b {
			continue
		}
		if k+1 < len(data) && data[k+1] == '\\' {
			return k, k + 2
		}
		return -1, -1
	}
	return -1, -1
}
