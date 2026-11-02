package syntax

import (
	"hash/maphash"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
)

// Incremental — a per-line token cache for the editor's background passes.
//
// Tokenization is a deterministic function of the text fed to the lexer: when
// the first N lines of a document are byte-for-byte what they were when the
// cache was filled, the lexer reaches line N in the same state and a fresh
// pass reproduces the cached tokens of those lines. The cache exploits that
// in three steps:
//
//  1. Prefix reuse. Lines whose text hash matches the previous pass are
//     spliced from the cache without tokenizing — up to the first changed
//     line (plus that line itself kept: an edit on line i does not disturb
//     the tokens of line i-1).
//
//  2. Re-sync window. After the first changed line the walk tokenizes fresh,
//     but every fresh line is compared against the cached tokens of the same
//     index (only where the text hash also matches, so the cached line really
//     is this line's previous self). When incWindow consecutive fresh lines
//     equal their cache entries, the lexer is demonstrably back in the state
//     the cache was built with — a state shift (a new block comment, an
//     unterminated string) would have made at least one line of the window
//     differ — and the remaining matching tail is spliced from the cache.
//
//  3. Anything the walk covers fresh is simply fresh, and correctness never
//     depends on the heuristic: the window only decides whether the cached
//     tail may be spliced, and a wrong decision to keep walking fresh is
//     slow, not wrong.
//
// One Incremental belongs to one document (one tab). The editor schedules at
// most one pass per tab, so Update calls never race.

// incWindow is the re-sync window: consecutive fresh lines that must equal
// their cached entries before the cached tail is trusted again.
const incWindow = 3

// incSeed is stable within a process, which is all a cache between two passes
// of the same process needs.
var incSeed = maphash.MakeSeed()

type incLine struct {
	toks HighlightedLine
}

// Incremental holds one document's per-line token cache.
type Incremental struct {
	mu    sync.Mutex
	lines []incLine
	// textHashes are the line hashes of the text the cache was last updated
	// with: the diff between two passes costs one hash per line instead of
	// full string compares.
	textHashes []uint64
}

// NewIncremental returns an empty per-line cache for one document.
func NewIncremental() *Incremental {
	return &Incremental{}
}

func hashLine(s string) uint64 {
	return maphash.String(incSeed, s)
}

// Update re-tokenizes text against the cache and returns the full highlight:
// one HighlightedLine per line of text (a trailing newline yields a final
// empty line, the shape HighlightBuffer produces). It reports how many lines
// were reused from the cache.
func (ic *Incremental) Update(filename, text string, hl *Highlighter) (out []HighlightedLine, reused int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	newLines := strings.Split(text, "\n")
	n := len(newLines)
	newHashes := make([]uint64, n)
	for i, ln := range newLines {
		newHashes[i] = hashLine(ln)
	}

	out = make([]HighlightedLine, n)
	oldCount := len(ic.textHashes)

	// Step 1: splice the unchanged prefix. Hash equality of the text is the
	// soundness condition — the lexer's state at line i is a function of
	// lines [0, i), and those are byte-for-byte the cached ones.
	prefix := 0
	limit := n
	if oldCount < limit {
		limit = oldCount
	}
	for prefix < limit && newHashes[prefix] == ic.textHashes[prefix] {
		out[prefix] = ic.lines[prefix].toks
		reused++
		prefix++
	}

	// Nothing changed: the cache is the answer, no lexer pass at all.
	if prefix == n {
		ic.store(out, newHashes)
		return out, reused
	}

	// Steps 2+3: tokenize the tail fresh — but only as far as the re-sync
	// window demands. One iterator over the full text drives the lexer from
	// the start (its state is a function of the prefix), the prefix lines are
	// consumed without copying tokens, the tail lines are collected — and the
	// moment incWindow consecutive fresh lines equal their cached entries,
	// iteration STOPS: the remaining tail is spliced from the cache without
	// tokenizing it. That is the whole win: a one-line edit near the top of a
	// large file costs prefix + window, not the rest of the file.
	iterator, err := hl.lexerFor(filename, text).Tokenise(nil, text)
	if err != nil {
		// A broken lexer leaves the tail unhighlighted; the prefix stays.
		ic.store(out, newHashes)
		return out, reused
	}
	matchRun := 0
	resync := -1 // first index the cached tail may splice from
	cur := 0
	var curLine HighlightedLine
	stopped := false
	flush := func() {
		if stopped {
			return
		}
		if cur >= prefix && cur < n {
			out[cur] = curLine
			// Window bookkeeping: a fresh line equal to its cached entry
			// extends the match run; the cached entry only counts where the
			// text hash matches too, so the cached line is really this
			// line's previous self.
			if cur < oldCount && newHashes[cur] == ic.textHashes[cur] &&
				incTokensEqual(curLine, ic.lines[cur].toks) {
				matchRun++
				if matchRun >= incWindow && resync < 0 {
					resync = cur + 1
					stopped = true
				}
			} else {
				matchRun = 0
			}
		}
		cur++
		curLine = HighlightedLine{}
	}
	for !stopped {
		token := iterator()
		if token == chroma.EOF {
			break
		}
		tok := token.Type
		segments := strings.Split(token.Value, "\n")
		for i, segment := range segments {
			if i > 0 {
				flush()
				if stopped {
					break
				}
			}
			for range []rune(segment) {
				curLine = append(curLine, tok)
			}
		}
	}
	if !stopped {
		flush() // the last line has no trailing separator
	}

	// The splice: from resync on, every line whose text hash matches the
	// cache takes the cached tokens; a hash mismatch ends the splice (the
	// line moved, its previous tokens are not its own any more). Lines past
	// the old cache are left unhighlighted — the next full pass paints them.
	if resync >= 0 {
		for i := resync; i < n && i < oldCount; i++ {
			if newHashes[i] != ic.textHashes[i] {
				break
			}
			out[i] = ic.lines[i].toks
			reused++
		}
	}

	ic.store(out, newHashes)
	return out, reused
}

// store records the pass result as the cache's new state.
func (ic *Incremental) store(out []HighlightedLine, hashes []uint64) {
	n := len(out)
	ic.lines = ic.lines[:0]
	if cap(ic.lines) < n {
		ic.lines = make([]incLine, 0, n)
	}
	for i := 0; i < n; i++ {
		toks := out[i]
		if toks == nil {
			toks = HighlightedLine{}
		}
		ic.lines = append(ic.lines, incLine{toks: toks})
	}
	ic.textHashes = hashes
}

// Reset drops the cache (a language change invalidates it; a theme change
// does not — tokens are style-independent).
func (ic *Incremental) Reset() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.lines = nil
	ic.textHashes = nil
}

func incTokensEqual(a, b HighlightedLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
