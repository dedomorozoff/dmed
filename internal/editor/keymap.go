package editor

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"dmed/internal/config"
)

// KeyMap is the chord table one model actually reads. The Go tables in keys.go
// stay package-level and immutable — they are the defaults, not a state — and
// every model owns its resolved map, so a rebind in one window cannot reach
// another and a hot reload replaces a pointer instead of mutating shared data.
//
// Resolution lives here rather than in the tables because the same chord has to
// be read three ways: as the closure to run, as the action name the help screen
// prints, and as the target of a [keys] line in the configuration.
type KeyMap struct {
	// global and editor are the two table stages of the dispatch chain, keyed by
	// the normalized chord keyOf produces. They are copies: resolving rewrites
	// entries, and writing through to the package tables would make one model's
	// config another model's defaults.
	global map[string]bindingDef
	editor map[string]bindingDef

	// defs is the built-in tables indexed by chord, kept apart from the resolved
	// ones because protection is a property of the entry dmed ships: an override
	// must not be able to make Ctrl+Q rebindable by replacing it.
	defs map[string]bindingDef

	// acts is the inverse index of the resolved maps: the chords bound to one
	// action, sorted. It is what makes an alias one fact rather than two entries
	// that happen to agree, and it is what the generated help reads.
	acts map[keyAct][]string

	// pane holds the keys of the panels and overlays, one table per keyboard owner,
	// chord to command name. A pane's stage is the only one whose value is a name
	// rather than a closure: the code stays in the handler's switch and only the
	// chord→command mapping is a preference, which is what lets an overlay be
	// re-keyed without moving a single line of its logic.
	pane map[paneID]map[string]keyAct

	// refusals explains, in the order the lines were read, what a [keys] section
	// asked for that the map would not do. A binding that silently does not move
	// is worse than one that says why.
	refusals []string
}

// ResolveKeys joins the configuration's [keys] section to the built-in tables.
// An empty section returns the defaults, which is why no caller has to know
// whether the user wrote anything.
func ResolveKeys(cfg config.Config) *KeyMap {
	k := &KeyMap{
		global: cloneTable(globalBindings),
		editor: cloneTable(editorBindings),
		defs:   defaultDefs(),
		pane:   clonePanes(),
	}
	actions := make([]string, 0, len(cfg.Keys.Overrides))
	for action := range cfg.Keys.Overrides {
		actions = append(actions, action)
	}
	// Map iteration is random and one chord can only be claimed once: the order
	// decides which of two actions asking for it wins, so it is sorted rather
	// than left to chance.
	sort.Strings(actions)
	k.resolve(actions, cfg.Keys.Overrides)
	k.index()
	return k
}

func cloneTable(t map[string]bindingDef) map[string]bindingDef {
	out := make(map[string]bindingDef, len(t))
	for chord, d := range t {
		out[chord] = d
	}
	return out
}

// clonePanes copies the shipped pane keys: resolving rewrites them, and the
// declarations are read by every model.
func clonePanes() map[paneID]map[string]keyAct {
	out := make(map[paneID]map[string]keyAct, len(paneDefaults))
	for p, table := range paneDefaults {
		c := make(map[string]keyAct, len(table))
		for chord, act := range table {
			c[chord] = act
		}
		out[p] = c
	}
	return out
}

func defaultDefs() map[string]bindingDef {
	out := make(map[string]bindingDef, len(globalBindings)+len(editorBindings))
	for _, t := range []map[string]bindingDef{globalBindings, editorBindings} {
		for chord, d := range t {
			out[chord] = d
		}
	}
	return out
}

// located is one entry together with the stage that holds it, because a chord
// can be bound in both tables at once (F6 steps in the debug panel and splits
// outside it) and a rebind must move only the action it names.
type located struct {
	chord  string
	def    bindingDef
	global bool
}

// entries lists what currently runs the action. In the resolved tables when
// about to clear it, in the built-in ones when asking where the action lives.
func (k *KeyMap) entries(act keyAct, resolved bool) []located {
	var globalTable, editorTable map[string]bindingDef
	if resolved {
		globalTable, editorTable = k.global, k.editor
	} else {
		globalTable, editorTable = globalBindings, editorBindings
	}
	var out []located
	for _, s := range []struct {
		table map[string]bindingDef
		glob  bool
	}{{globalTable, true}, {editorTable, false}} {
		for chord, d := range s.table {
			if d.act == act {
				out = append(out, located{chord: chord, def: d, global: s.glob})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].chord < out[j].chord })
	return out
}

func (k *KeyMap) stage(global bool) map[string]bindingDef {
	if global {
		return k.global
	}
	return k.editor
}

// bound reports the entry a chord runs, in whichever stage holds it. One chord
// answers one command: the stages are checked together because the dispatcher
// would otherwise decide that silently, global first, every time.
func (k *KeyMap) bound(chord string) (bindingDef, bool) {
	if d, ok := k.global[chord]; ok {
		return d, true
	}
	d, ok := k.editor[chord]
	return d, ok
}

// resolve applies the whole [keys] section in two passes: every action the file
// names is cleared first, then the chords it asked for are written. One pass
// cannot express a swap — `file.save = ctrl+k` is refused over a Ctrl+K that
// `lsp.hover = ctrl+s` has not released yet — and a user who wrote both lines
// meant both. Clearing first also keeps a move from being an addition: the
// action's own chords go before the new ones arrive, which is the only way to
// subtract an alias a line does not repeat.
func (k *KeyMap) resolve(actions []string, overrides map[string][]string) {
	known := make([]keyAct, 0, len(actions))
	for _, action := range actions {
		act := keyAct(action)
		if !k.isKnown(act) {
			k.refuse(action, "no such action (dmed --dump-keys actions lists them)")
			continue
		}
		k.clear(act, overrides[action])
		known = append(known, act)
	}
	for _, act := range known {
		k.apply(act, overrides[string(act)])
	}
}

// isKnown reports whether the name is a command dmed ships: one of the editor's
// own, or one declared by a pane.
func (k *KeyMap) isKnown(act keyAct) bool {
	if _, _, ok := paneActCommand(act); ok {
		return true
	}
	return len(k.entries(act, false)) > 0
}

// clear drops the chords the command answers to now, in whichever stage holds it.
// A pane command and a table action can never share a name — see
// TestPaneCommandsAreWellFormed — so one line addresses exactly one of them.
func (k *KeyMap) clear(act keyAct, wanted []string) {
	if _, p, ok := paneActCommand(act); ok {
		k.clearPane(p, act, wanted)
		return
	}
	for _, e := range k.entries(act, true) {
		if e.def.flags&protected == 0 {
			delete(k.stage(e.global), e.chord)
			continue
		}
		if chordListed(e.chord, wanted) {
			continue
		}
		k.refuse(string(act)+" =", e.chord+" is protected; it stays bound to "+string(e.def.act))
	}
}

// chordListed: the value is what the user typed, so a range has to be expanded
// before it can be compared with a chord from the table.
func chordListed(chord string, wanted []string) bool {
	for _, raw := range wanted {
		for _, c := range expandChord(raw) {
			if c == chord {
				return true
			}
		}
	}
	return false
}

// apply writes the chords one action asked for, in the stage that owns the name.
func (k *KeyMap) apply(act keyAct, value []string) {
	if _, p, ok := paneActCommand(act); ok {
		k.assignPane(p, act, value)
		return
	}
	k.assign(act, value)
}

// assign moves one chord of a table stage. It is refused when it cannot arrive as
// written, when it is protected and belongs to something else, or when an action
// the file did not name already answers to it.
func (k *KeyMap) assign(act keyAct, value []string) {
	if len(value) == 0 {
		return
	}
	defaults := k.entries(act, false)
	global := defaults[0].global
	for _, raw := range value {
		for _, chord := range expandChord(raw) {
			line := string(act) + " = " + raw
			if why := checkChord(chord); why != "" {
				k.refuse(line, why)
				continue
			}
			shared := defaultCoOwner(chord, act)
			if k.isProtected(chord) {
				if cur, ok := k.bound(chord); ok && cur.act == act {
					continue // already exactly where the line says
				}
				k.refuse(line, chord+" is protected and stays with "+string(k.defs[chord].act))
				continue
			}
			if cur, taken := k.bound(chord); taken && cur.act != act && !shared {
				k.refuse(line, chord+" already runs "+string(cur.act))
				continue
			}
			if !shared {
				// The other stage cannot keep it either, or a line that writes to
				// the text table would be answered by the global one first.
				delete(k.stage(!global), chord)
			}
			k.stage(global)[chord] = defaults[0].def
		}
	}
}

// defaultCoOwner reports whether this chord ships in *both* stages under two
// different actions and the named action is one of them. F6 and F7 are the only
// such chords: they step in the debug stage and split in the text one, and the
// dispatcher's fallthrough between the two is machinery rather than a preference
// a [keys] line can address. A line naming one of them restates its own half, so
// it neither steals the chord nor removes the other half — which is what makes
// copying the shipped keys back into the file change nothing.
func defaultCoOwner(chord string, act keyAct) bool {
	g, gok := globalBindings[chord]
	e, eok := editorBindings[chord]
	return gok && eok && g.act != e.act && (g.act == act || e.act == act)
}

// clearPane drops the chords a pane command answers to now. A protected one
// survives, and is only complained about when the line does not ask for it again:
// restating Esc on the panel that closes on Esc is the user agreeing with the
// default, not trying to move it.
func (k *KeyMap) clearPane(p paneID, act keyAct, wanted []string) {
	cmd, _, ok := paneActCommand(act)
	if !ok {
		return
	}
	for _, chord := range k.paneChords(p, act) {
		if cmd.flags&protected == 0 {
			delete(k.pane[p], chord)
			continue
		}
		if chordListed(chord, wanted) {
			continue
		}
		k.refuse(string(act)+" =", chord+" is protected; it stays bound to "+string(act))
	}
}

// paneChords lists the resolved table's chords for one pane command, sorted, so
// a refusal report reads the same every run.
func (k *KeyMap) paneChords(p paneID, act keyAct) []string {
	var out []string
	for chord, held := range k.pane[p] {
		if held == act {
			out = append(out, chord)
		}
	}
	sort.Strings(out)
	return out
}

// assignPane writes the chords a pane command was given. The rules are the
// editor's, with one addition only a pane needs: a chord the global stage already
// answers is refused, because dispatchKey asks that table first and the panel
// would never see the key. The exception is a chord the command shipped with,
// where refusing would break the one property the whole section is built on —
// that restating the defaults changes nothing.
func (k *KeyMap) assignPane(p paneID, act keyAct, value []string) {
	cmd, _, ok := paneActCommand(act)
	if !ok || len(value) == 0 {
		return
	}
	for _, raw := range value {
		for _, chord := range expandChord(raw) {
			line := string(act) + " = " + raw
			if why := checkChord(chord); why != "" {
				k.refuse(line, why)
				continue
			}
			shipped := false
			for _, c := range cmd.chords {
				shipped = shipped || c == chord
			}
			if cmd.flags&protected != 0 {
				if held, taken := k.pane[p][chord]; taken && held == act {
					continue // exactly where the line says it should be
				}
				k.refuse(line, chord+" is protected; it stays bound to "+string(act))
				continue
			}
			if d, taken := k.global[chord]; taken && !shipped {
				k.refuse(line, chord+" answers globally as "+string(d.act)+" before any panel sees it")
				continue
			}
			if held, taken := k.pane[p][chord]; taken && held != act {
				k.refuse(line, chord+" already runs "+string(held))
				continue
			}
			k.pane[p][chord] = act
		}
	}
}

func (k *KeyMap) refuse(line, why string) {
	k.refusals = append(k.refusals, line+": "+why)
}

// keysRefused is the status line for a [keys] section the map did not accept.
// The bar holds one sentence, so it gives the count and quotes the first line:
// the user is already in the file, and the count says how much of it to reread.
func (m *Model) keysRefused() string {
	n := len(m.keys.refusals)
	if n == 0 {
		return ""
	}
	return m.t("msg.keys_refused", n, m.keys.refusals[0])
}

// The modifier bits are one per name in keyModOrder, in that order.
const (
	modCtrl = 1 << iota
	modAlt
	modShift
)

// namedKeys are the keys that are not a printable character, spelled the way
// the terminal reports them.
var namedKeys = map[string]bool{
	"esc": true, "enter": true, "tab": true, "backspace": true, "delete": true,
	"space": true, "up": true, "down": true, "left": true, "right": true,
	"pgup": true, "pgdown": true, "home": true, "end": true, "insert": true,
}

// checkChord reports why a chord cannot ever fire, or "" when it can. The
// names are the ones keyOf produces, so the check is against that vocabulary
// rather than a taste.
func checkChord(s string) string {
	base := strings.ToLower(strings.TrimSpace(s))
	if base == "" {
		return "empty"
	}
	var mods int
	last := -1
	for {
		next := -1
		for i, name := range keyModOrder {
			if strings.HasPrefix(base, name+"+") {
				next = i
				break
			}
		}
		if next < 0 {
			break
		}
		if next <= last {
			if mods&(1<<next) != 0 {
				return "a modifier is written twice"
			}
			return "modifiers are written ctrl, alt, shift, in that order"
		}
		last = next
		mods |= 1 << next
		base = base[len(keyModOrder[next])+1:]
	}
	if base == "" {
		return "no key after the modifiers"
	}
	if mods == modShift && len([]rune(base)) == 1 && isAlnum(base) {
		// keyOf keeps the typed character for a shift-only press, so the letter
		// arrives as "G" and the chord that names the shift can never match.
		return "a shifted key arrives as the key itself: write the character it produces"
	}
	if namedKeys[base] || fKey(base) || singlePrintable(base) {
		return ""
	}
	return fmt.Sprintf("%q is not a key dmed can see", base)
}

// singlePrintable accepts one character as the key of a chord: the letters,
// digits and symbols the terminal delivers verbatim.
func singlePrintable(base string) bool {
	r := []rune(base)
	return len(r) == 1 && r[0] >= ' ' && r[0] != 0x7f
}

func fKey(s string) bool {
	if len(s) < 2 || s[0] != 'f' {
		return false
	}
	n := 0
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= 1 && n <= 12
}

// isAlnum reports a run of letters and digits. The chord arrives lowercased
// from checkChord, so only the lowercase half of an ASCII letter can match.
func isAlnum(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

// expandChord turns one config value into the chords it names: the spelling the
// tables key on, plus the "alt+1..9" sugar for a run of digits, which is how a
// family of nine commands gets written as one line.
func expandChord(raw string) []string {
	s := strings.TrimSpace(raw)
	if len([]rune(s)) == 1 && !strings.ContainsRune(s, '+') {
		// A lone letter keeps the case it was written in: keyOf reports a shifted
		// press as the character it produces, so "N" and "n" are two different keys
		// and a list pane carries both. Anything with a modifier is spelled in
		// lowercase, which is what lets a config write Ctrl+S for ctrl+s.
		return []string{s}
	}
	s = strings.ToLower(s)
	dot := strings.Index(s, "..")
	if dot < 2 || len(s) != dot+3 {
		// Not the shape <prefix><digit>..<digit>: leave it as written and let
		// checkChord say what is wrong with it.
		return []string{s}
	}
	head, from, to := s[:dot-1], s[dot-1], s[dot+2]
	if !strings.HasSuffix(head, "+") || from > to || from < '0' || from > '9' || to < '0' || to > '9' {
		return []string{s}
	}
	out := make([]string, 0, int(to-from)+1)
	for d := from; d <= to; d++ {
		out = append(out, head+string(d))
	}
	return out
}

// index rebuilds the action -> chords index from the resolved tables, so help
// and the chord lookups read the same map the dispatcher runs.
func (k *KeyMap) index() {
	k.acts = make(map[keyAct][]string, len(k.defs))
	for _, table := range []map[string]bindingDef{k.global, k.editor} {
		for chord, d := range table {
			k.acts[d.act] = append(k.acts[d.act], chord)
		}
	}
	// A pane command is indexed too, so the help and the dump read one map for
	// both kinds of stage even though only one of them holds closures.
	for _, table := range k.pane {
		for chord, act := range table {
			k.acts[act] = append(k.acts[act], chord)
		}
	}
	for act, list := range k.acts {
		sort.Strings(list)
		// The same chord can reach one command through two stages, and the list is
		// what the cheat sheet prints: "f3 f3" would read as a fact about the keys
		// rather than about the chain.
		k.acts[act] = slices.Compact(list)
	}
}

// chords reports every chord bound to an action, in the sorted order built once
// here so a rebind and the help screen read the same list.
func (k *KeyMap) chords(act keyAct) []string { return k.acts[act] }

// isProtected reports whether a chord refuses a rebind. Protection is a property
// of the default entry, not of the resolved override: rebinding away the escape
// hatch strands a user in a panel they cannot leave.
func (k *KeyMap) isProtected(chord string) bool {
	d, ok := k.defs[chord]
	return ok && d.flags&protected != 0
}

// isTabJump reports whether a text-pane chord is a member of the tab-jump
// family, so a pane that owns every key can let the workspace commands through
// without naming the digits itself. A rebound tab.jump keeps the exemption: the
// guard asks what the chord does now, not what it used to do.
func (k *KeyMap) isTabJump(chord string) bool {
	d, ok := k.editor[chord]
	return ok && d.act == actTabJump
}
