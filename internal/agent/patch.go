package agent

import (
	"fmt"
	"strings"
)

// parseBlocks splits an agent response into per-file edits. Convention:
//
//	=== FILE: path/to/file.go ===
//	...full new file content...
//
// Blocks may be separated by blank lines. Anything before the first
// "=== FILE:" header (e.g. a short preamble or notes) is ignored, as is
// trailing prose after the last content block. The Orig field is left empty
// and filled in by the Runner from the current on-disk content.
func parseBlocks(text string) ([]Change, error) {
	const headerPrefix = "=== FILE:"

	var changes []Change
	lines := strings.Split(text, "\n")
	var cur *Change

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, headerPrefix) {
			// close previous block
			if cur != nil {
				changes = append(changes, *cur)
			}
			name := strings.TrimSpace(trimmed[len(headerPrefix):])
			name = strings.TrimSuffix(name, "===")
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("no path in header at line %d: %q", i+1, line)
			}
			cur = &Change{Path: name}
			continue
		}
		if cur != nil {
			cur.New += line + "\n"
		}
	}
	if cur != nil {
		changes = append(changes, *cur)
	}

	if len(changes) == 0 {
		return nil, fmt.Errorf("no '=== FILE:' blocks found in agent response")
	}
	return changes, nil
}
