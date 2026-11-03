package editor

import "sort"

// KeyMap is the chord table one model actually reads. The Go tables in keys.go
// stay package-level and immutable — they are the defaults, not a state — and
// every model owns its resolved map, so a rebind in one window cannot reach
// another and a hot reload replaces a pointer instead of mutating shared data.
//
// Resolution lives here rather than in the tables because the same chord has to
// be read three ways: as the closure to run, as the action name the help screen
// prints, and as the alias set a [keys] section addresses.
type KeyMap struct {
	// global and editor are the two table stages of the dispatch chain, keyed by
	// the normalized chord keyOf produces.
	global map[string]bindingDef
	editor map[string]bindingDef

	// acts is the inverse index: the chords bound to one action, sorted. It is
	// what makes an alias one fact rather than two entries that happen to agree,
	// and it is what the generated help reads.
	acts map[keyAct][]string
}

// buildKeyMap resolves the map from the default tables alone. It is a function
// of immutable package data, so two models built in the same process agree
// without sharing anything mutable; a config-driven override joins it in with
// the [keys] section.
func buildKeyMap() *KeyMap {
	k := &KeyMap{
		global: globalBindings,
		editor: editorBindings,
		acts:   map[keyAct][]string{},
	}
	for _, table := range []map[string]bindingDef{k.global, k.editor} {
		for chord, d := range table {
			k.acts[d.act] = append(k.acts[d.act], chord)
		}
	}
	for list := range k.acts {
		sort.Strings(k.acts[list])
	}
	return k
}

// chords reports every chord bound to an action, in the sorted order built once
// here so a rebind and the help screen read the same list.
func (k *KeyMap) chords(act keyAct) []string { return k.acts[act] }

// isProtected reports whether a chord refuses a rebind. Protection is a property
// of the default entry, not of the resolved override: rebinding away the escape
// hatch strands a user in a panel they cannot leave.
func (k *KeyMap) isProtected(chord string) bool {
	for _, table := range []map[string]bindingDef{k.global, k.editor} {
		if d, ok := table[chord]; ok && d.flags&protected != 0 {
			return true
		}
	}
	return false
}

// isTabJump reports whether a text-pane chord is a member of the tab-jump
// family, so a pane that owns every key can let the workspace commands through
// without naming the digits itself.
func (k *KeyMap) isTabJump(chord string) bool {
	d, ok := k.editor[chord]
	return ok && d.act == actTabJump
}
