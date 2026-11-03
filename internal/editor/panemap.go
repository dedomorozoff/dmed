package editor

import (
	"sort"
)

// A pane is the panel or overlay that owns the keyboard while it is open, and the
// stage of the dispatch chain that sits between the global chords and the text
// pane. Its keys used to be literals inside the handler's own `switch s`, which
// made them the one part of the editor a `[keys]` section could not reach.
//
// They are not literals any more. A handler still holds its code — the arm bodies
// did not move, which is what keeps this migration reviewable — but it dispatches
// on a *command name* that `paneCmd` resolves from the chord, and the command's
// chords are declared here. So a rebind stays a data change: `search.next = f3`
// rewrites the declaration's mapping, not the handler.

// paneID names one keyboard owner. It is the prefix every command of that pane
// writes in a [keys] line, so the handler, the declaration and the config string
// are the same word — there is nothing to keep in step between three tables.
type paneID string

// paneCommand is one command of one pane: the name a `[keys]` line writes, the
// chords that run it as shipped, and whether the pane's own machinery needs one
// of them where it is.
type paneCommand struct {
	act    keyAct
	chords []string
	flags  bindFlags
}

// paneCommands declares the keys of every migrated pane. A pane that is not here
// yet still matches chords in its handler: the list is the migration's ledger, and
// TestPaneHandlersDispatchOnCommands fails if a declaration and a handler stop
// agreeing about a single chord.
//
// A declared name is also never a table action's name, which is what lets one
// [keys] line address exactly one stage. The find prompt's next-match key is
// `find.next`, not `search.next`: `search.next` is the text pane's find-again key,
// and giving the prompt the same name would hand Enter to the text pane the first
// time someone rebound it. TestPaneCommandsAreWellFormed keeps them apart.
var paneCommands = map[paneID][]paneCommand{
	// The Ctrl+F prompt.
	"find": {
		{act: "find.close", chords: []string{"esc"}, flags: protected},
		{act: "find.next", chords: []string{"enter", "f3", "down", "ctrl+n"}},
		{act: "find.prev", chords: []string{"shift+f3", "up", "ctrl+p"}},
		{act: "find.replace", chords: []string{"ctrl+h"}},
		{act: "find.regex", chords: []string{"tab"}},
		{act: "find.erase", chords: []string{"backspace"}, flags: protected},
	},
	// The Ctrl+H prompt: two fields, one answer key.
	"replace": {
		{act: "replace.close", chords: []string{"esc"}, flags: protected},
		{act: "replace.focus", chords: []string{"tab"}},
		{act: "replace.all", chords: []string{"ctrl+a"}},
		{act: "replace.accept", chords: []string{"enter"}},
		{act: "replace.next", chords: []string{"f3", "down", "ctrl+n"}},
		{act: "replace.prev", chords: []string{"shift+f3", "up", "ctrl+p"}},
		{act: "replace.erase", chords: []string{"backspace"}, flags: protected},
	},
	// tree
	"tree": {
		{act: "tree.up", chords: []string{"up"}},
		{act: "tree.down", chords: []string{"down"}},
		{act: "tree.page-up", chords: []string{"pgup"}},
		{act: "tree.page-down", chords: []string{"pgdown"}},
		{act: "tree.open", chords: []string{"enter"}},
		{act: "tree.expand", chords: []string{"right"}},
		{act: "tree.collapse", chords: []string{"left"}},
		{act: "tree.blur", chords: []string{"esc", "tab"}, flags: protected},
		{act: "tree.git-panel", chords: []string{"ctrl+g"}},
		{act: "tree.toggle", chords: []string{"ctrl+b", "f9"}},
		{act: "tree.new-file", chords: []string{"n"}},
		{act: "tree.new-folder", chords: []string{"N"}},
		{act: "tree.rename", chords: []string{"r"}},
		{act: "tree.duplicate", chords: []string{"d"}},
		{act: "tree.delete", chords: []string{"delete"}},
		{act: "tree.trash", chords: []string{"t"}},
	},
	// tree-confirm
	"tree-confirm": {
		{act: "tree-confirm.yes", chords: []string{"y", "enter"}, flags: protected},
		{act: "tree-confirm.no", chords: []string{"n", "esc"}, flags: protected},
	},
	// palette
	"palette": {
		{act: "palette.close", chords: []string{"esc"}, flags: protected},
		{act: "palette.run", chords: []string{"enter"}},
		{act: "palette.up", chords: []string{"up"}},
		{act: "palette.down", chords: []string{"down"}},
		{act: "palette.erase", chords: []string{"backspace"}, flags: protected},
	},
	// prompt
	"prompt": {
		{act: "prompt.close", chords: []string{"esc"}, flags: protected},
		{act: "prompt.run", chords: []string{"enter"}},
		{act: "prompt.erase", chords: []string{"backspace"}, flags: protected},
	},
	// save-prompt
	"save-prompt": {
		{act: "save-prompt.close", chords: []string{"esc"}, flags: protected},
		{act: "save-prompt.run", chords: []string{"enter"}},
		{act: "save-prompt.erase", chords: []string{"backspace"}, flags: protected},
	},
	// goto
	"goto": {
		{act: "goto.close", chords: []string{"esc"}, flags: protected},
		{act: "goto.run", chords: []string{"enter"}},
		{act: "goto.erase", chords: []string{"backspace"}, flags: protected},
		{act: "goto.clear", chords: []string{"ctrl+l"}},
	},
	// diff
	"diff": {
		{act: "diff.close", chords: []string{"esc", "q", "d"}, flags: protected},
		{act: "diff.up", chords: []string{"up", "k"}},
		{act: "diff.down", chords: []string{"down", "j"}},
		{act: "diff.page-up", chords: []string{"pgup"}},
		{act: "diff.page-down", chords: []string{"pgdown"}},
		{act: "diff.top", chords: []string{"home", "g"}},
		{act: "diff.bottom", chords: []string{"end", "G"}},
		{act: "diff.scroll-left", chords: []string{"left", "h"}},
		{act: "diff.scroll-right", chords: []string{"right", "l"}},
	},
	// git
	"git": {
		{act: "git.close", chords: []string{"ctrl+b", "f9"}},
	},
	// agent
	"agent": {
		{act: "agent.close", chords: []string{"esc"}, flags: protected},
		{act: "agent.blur", chords: []string{"tab"}},
		{act: "agent.down", chords: []string{"j", "down"}},
		{act: "agent.up", chords: []string{"k", "up"}},
		{act: "agent.top", chords: []string{"g", "home"}},
		{act: "agent.bottom", chords: []string{"G", "end"}},
		{act: "agent.cancel", chords: []string{"x", "ctrl+d"}},
		{act: "agent.review", chords: []string{"enter"}},
		{act: "agent.new-task", chords: []string{"n"}},
		{act: "agent.revert", chords: []string{"u", "U"}},
	},
	// agent-prompt
	"agent-prompt": {
		{act: "agent-prompt.close", chords: []string{"esc"}, flags: protected},
		{act: "agent-prompt.run", chords: []string{"enter"}},
		{act: "agent-prompt.erase", chords: []string{"backspace"}, flags: protected},
	},
	// agent-review
	"agent-review": {
		{act: "agent-review.accept", chords: []string{"y", "enter", "a"}, flags: protected},
		{act: "agent-review.reject", chords: []string{"n", "esc", "r"}, flags: protected},
		{act: "agent-review.next-change", chords: []string{"tab"}},
		{act: "agent-review.prev-change", chords: []string{"shift+tab"}},
		{act: "agent-review.up", chords: []string{"up", "k"}},
		{act: "agent-review.down", chords: []string{"down", "j"}},
		{act: "agent-review.page-up", chords: []string{"pgup"}},
		{act: "agent-review.page-down", chords: []string{"pgdown"}},
		{act: "agent-review.top", chords: []string{"home", "g"}},
		{act: "agent-review.bottom", chords: []string{"end", "G"}},
		{act: "agent-review.scroll-left", chords: []string{"left", "h"}},
		{act: "agent-review.scroll-right", chords: []string{"right", "l"}},
	},
	// sqlite-edit
	"sqlite-edit": {
		{act: "sqlite-edit.run", chords: []string{"enter"}},
		{act: "sqlite-edit.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "sqlite-edit.erase", chords: []string{"backspace"}, flags: protected},
	},
	// sqlite-filter
	"sqlite-filter": {
		{act: "sqlite-filter.run", chords: []string{"enter"}},
		{act: "sqlite-filter.close", chords: []string{"esc"}, flags: protected},
		{act: "sqlite-filter.erase", chords: []string{"backspace"}, flags: protected},
	},
	// conflict
	"conflict": {
		{act: "conflict.reload", chords: []string{"r", "R"}, flags: protected},
		{act: "conflict.keep", chords: []string{"i", "I", "esc"}, flags: protected},
		{act: "conflict.up", chords: []string{"up", "k"}},
		{act: "conflict.down", chords: []string{"down", "j"}},
		{act: "conflict.page-up", chords: []string{"pgup"}},
		{act: "conflict.page-down", chords: []string{"pgdown"}},
		{act: "conflict.top", chords: []string{"home", "g"}},
		{act: "conflict.bottom", chords: []string{"end", "G"}},
		{act: "conflict.scroll-left", chords: []string{"left", "h"}},
		{act: "conflict.scroll-right", chords: []string{"right", "l"}},
	},
	// quit-confirm
	"quit-confirm": {
		{act: "quit-confirm.close", chords: []string{"esc"}, flags: protected},
		{act: "quit-confirm.accept", chords: []string{"y", "Y"}, flags: protected},
		{act: "quit-confirm.decline", chords: []string{"n", "N"}, flags: protected},
	},
	// sqlite-schema
	"sqlite-schema": {
		{act: "sqlite-schema.close", chords: []string{"esc", "s", "q"}, flags: protected},
		{act: "sqlite-schema.up", chords: []string{"up", "k"}},
		{act: "sqlite-schema.down", chords: []string{"down", "j"}},
		{act: "sqlite-schema.page-up", chords: []string{"pgup"}},
		{act: "sqlite-schema.page-down", chords: []string{"pgdown"}},
	},
	// finder
	"finder": {
		{act: "finder.close", chords: []string{"esc"}, flags: protected},
		{act: "finder.run", chords: []string{"enter"}},
		{act: "finder.up", chords: []string{"up"}},
		{act: "finder.down", chords: []string{"down"}},
		{act: "finder.erase", chords: []string{"backspace"}, flags: protected},
	},
	// help
	"help": {
		{act: "help.close", chords: []string{"esc", "f1", "ctrl+e", "q"}, flags: protected},
		{act: "help.down", chords: []string{"j", "down", "pgdown"}},
		{act: "help.up", chords: []string{"k", "up", "pgup"}},
		{act: "help.top", chords: []string{"g", "home"}},
		{act: "help.bottom", chords: []string{"G", "end"}},
	},
	// lang
	"lang": {
		{act: "lang.close", chords: []string{"esc"}, flags: protected},
		{act: "lang.run", chords: []string{"enter"}},
		{act: "lang.up", chords: []string{"up", "k"}},
		{act: "lang.down", chords: []string{"down", "j"}},
	},
	// hover
	"hover": {
		{act: "hover.close", chords: []string{"esc", "ctrl+c", "ctrl+k", "enter"}, flags: protected},
	},
	// outline
	"outline": {
		{act: "outline.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "outline.up", chords: []string{"up", "k", "ctrl+p"}},
		{act: "outline.down", chords: []string{"down", "j", "ctrl+n"}},
		{act: "outline.open", chords: []string{"enter"}},
	},
	// refs
	"refs": {
		{act: "refs.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "refs.up", chords: []string{"up", "k", "ctrl+p"}},
		{act: "refs.down", chords: []string{"down", "j", "ctrl+n"}},
		{act: "refs.open", chords: []string{"enter"}},
	},
	// lsp-actions
	"lsp-actions": {
		{act: "lsp-actions.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "lsp-actions.up", chords: []string{"up", "k", "ctrl+p"}},
		{act: "lsp-actions.down", chords: []string{"down", "j", "ctrl+n"}},
		{act: "lsp-actions.page-up", chords: []string{"pgup"}},
		{act: "lsp-actions.page-down", chords: []string{"pgdown"}},
		{act: "lsp-actions.run", chords: []string{"enter"}},
	},
	// lsp-rename
	"lsp-rename": {
		{act: "lsp-rename.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "lsp-rename.run", chords: []string{"enter"}},
		{act: "lsp-rename.erase", chords: []string{"backspace"}, flags: protected},
	},
	// plugin-store
	"plugin-store": {
		{act: "plugin-store.close", chords: []string{"esc"}, flags: protected},
		{act: "plugin-store.run", chords: []string{"enter"}},
		{act: "plugin-store.up", chords: []string{"up", "k"}},
		{act: "plugin-store.down", chords: []string{"down", "j"}},
	},
	// ask
	"ask": {
		{act: "ask.close", chords: []string{"esc"}, flags: protected},
		{act: "ask.run", chords: []string{"enter"}},
		{act: "ask.up", chords: []string{"up", "k"}},
		{act: "ask.down", chords: []string{"down", "j"}},
		{act: "ask.pick", chords: []string{"tab"}},
		{act: "ask.erase", chords: []string{"backspace"}, flags: protected},
	},
	// folder
	"folder": {
		{act: "folder.close", chords: []string{"esc"}, flags: protected},
		{act: "folder.open", chords: []string{"enter", "right"}},
		{act: "folder.parent", chords: []string{"left", "backspace"}},
		{act: "folder.use", chords: []string{"o"}},
		{act: "folder.up", chords: []string{"up"}},
		{act: "folder.down", chords: []string{"down"}},
		{act: "folder.page-up", chords: []string{"pgup"}},
		{act: "folder.page-down", chords: []string{"pgdown"}},
		{act: "folder.top", chords: []string{"home"}},
		{act: "folder.bottom", chords: []string{"end"}},
		{act: "folder.home-dir", chords: []string{"~"}},
		{act: "folder.root-dir", chords: []string{"/"}},
	},
	// image
	"image": {
		{act: "image.prev-tab", chords: []string{"alt+left"}},
		{act: "image.next-tab", chords: []string{"alt+right"}},
		{act: "image.save", chords: []string{"ctrl+s"}},
		{act: "image.prev-hunk", chords: []string{"alt+["}},
		{act: "image.next-hunk", chords: []string{"alt+]"}},
		{act: "image.up", chords: []string{"up", "k"}},
		{act: "image.down", chords: []string{"down", "j"}},
		{act: "image.page-up", chords: []string{"pgup"}},
		{act: "image.page-down", chords: []string{"pgdown"}},
		{act: "image.top", chords: []string{"home", "g"}},
		{act: "image.bottom", chords: []string{"end", "G"}},
		{act: "image.paste", chords: []string{"ctrl+v"}},
	},
	// sqlite
	"sqlite": {
		{act: "sqlite.close", chords: []string{"esc", "q"}, flags: protected},
		{act: "sqlite.up", chords: []string{"up", "k"}},
		{act: "sqlite.down", chords: []string{"down", "j"}},
		{act: "sqlite.left", chords: []string{"left", "h"}},
		{act: "sqlite.right", chords: []string{"right", "l"}},
		{act: "sqlite.page-up", chords: []string{"pgup"}},
		{act: "sqlite.page-down", chords: []string{"pgdown"}},
		{act: "sqlite.top", chords: []string{"home", "g"}},
		{act: "sqlite.bottom", chords: []string{"end", "G"}},
		{act: "sqlite.edit-cell", chords: []string{"enter"}},
		{act: "sqlite.insert-row", chords: []string{"ctrl+n"}},
		{act: "sqlite.delete-row", chords: []string{"ctrl+d"}},
		{act: "sqlite.toggle-order", chords: []string{"o"}},
		{act: "sqlite.filter", chords: []string{"/"}},
		{act: "sqlite.table-next", chords: []string{"tab"}},
		{act: "sqlite.table-prev", chords: []string{"shift+tab"}},
		{act: "sqlite.schema", chords: []string{"s"}},
	},
	// park-ask
	// park-mode
	"park-mode": {
		{act: "park-mode.yes", chords: []string{"y", "enter"}, flags: protected},
		{act: "park-mode.no", chords: []string{"n", "esc", "r"}, flags: protected},
	},
	// park-run
	"park-run": {
		{act: "park-run.yes", chords: []string{"y", "enter"}, flags: protected},
		{act: "park-run.no", chords: []string{"n", "esc", "r"}, flags: protected},
	},
	// park-sub
	"park-sub": {
		{act: "park-sub.cancel", chords: []string{"esc"}, flags: protected},
	},
	// chat
	"chat": {
		{act: "chat.close", chords: []string{"esc"}, flags: protected},
		{act: "chat.stop", chords: []string{"ctrl+q", "ctrl+c"}},
		{act: "chat.run", chords: []string{"enter"}},
		{act: "chat.erase", chords: []string{"backspace"}, flags: protected},
		{act: "chat.page-up", chords: []string{"pgup"}},
		{act: "chat.page-down", chords: []string{"pgdown"}},
		{act: "chat.history-prev", chords: []string{"up"}},
		{act: "chat.history-next", chords: []string{"down"}},
		{act: "chat.thread-prev", chords: []string{"ctrl+n"}},
		{act: "chat.new-thread", chords: []string{"ctrl+u"}},
		{act: "chat.clear", chords: []string{"ctrl+l"}},
		{act: "chat.copy", chords: []string{"ctrl+y"}},
		{act: "chat.reask", chords: []string{"r", "R"}},
	},
	// chat-review
	"chat-review": {
		{act: "chat-review.accept", chords: []string{"y", "enter"}, flags: protected},
		{act: "chat-review.accept-all", chords: []string{"a"}},
		{act: "chat-review.reject", chords: []string{"n", "esc", "r"}, flags: protected},
		{act: "chat-review.next-file", chords: []string{"tab"}},
		{act: "chat-review.prev-file", chords: []string{"shift+tab"}},
		{act: "chat-review.up", chords: []string{"up", "k"}},
		{act: "chat-review.down", chords: []string{"down", "j"}},
		{act: "chat-review.page-up", chords: []string{"pgup"}},
		{act: "chat-review.page-down", chords: []string{"pgdown"}},
		{act: "chat-review.top", chords: []string{"home", "g"}},
		{act: "chat-review.bottom", chords: []string{"end", "G"}},
		{act: "chat-review.scroll-left", chords: []string{"left", "h"}},
		{act: "chat-review.scroll-right", chords: []string{"right", "l"}},
	},
	// proj-search-input
	"proj-search-input": {
		{act: "proj-search-input.close", chords: []string{"esc"}, flags: protected},
		{act: "proj-search-input.toggle-regex", chords: []string{"tab"}},
		{act: "proj-search-input.run", chords: []string{"enter"}},
		{act: "proj-search-input.erase", chords: []string{"backspace"}, flags: protected},
	},
	// proj-search
	"proj-search": {
		{act: "proj-search.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "proj-search.up", chords: []string{"up", "k", "ctrl+p"}},
		{act: "proj-search.page-up", chords: []string{"pgup"}},
		{act: "proj-search.page-down", chords: []string{"pgdown"}},
		{act: "proj-search.down", chords: []string{"down", "j", "ctrl+n"}},
		{act: "proj-search.open", chords: []string{"enter"}},
	},
	// proj-repl-input
	"proj-repl-input": {
		{act: "proj-repl-input.close", chords: []string{"esc"}, flags: protected},
		{act: "proj-repl-input.toggle-regex", chords: []string{"tab"}},
		{act: "proj-repl-input.run", chords: []string{"enter"}},
		{act: "proj-repl-input.erase", chords: []string{"backspace"}, flags: protected},
	},
	// proj-repl-list
	"proj-repl-list": {
		{act: "proj-repl-list.close", chords: []string{"esc", "ctrl+c"}, flags: protected},
		{act: "proj-repl-list.up", chords: []string{"up", "k", "ctrl+p"}},
		{act: "proj-repl-list.down", chords: []string{"down", "j", "ctrl+n"}},
		{act: "proj-repl-list.page-up", chords: []string{"pgup"}},
		{act: "proj-repl-list.page-down", chords: []string{"pgdown"}},
		{act: "proj-repl-list.open", chords: []string{"enter"}},
	},
	// proj-repl-review
	"proj-repl-review": {
		{act: "proj-repl-review.accept", chords: []string{"y", "enter"}, flags: protected},
		{act: "proj-repl-review.accept-all", chords: []string{"a"}},
		{act: "proj-repl-review.reject", chords: []string{"n", "esc", "r"}, flags: protected},
		{act: "proj-repl-review.next-file", chords: []string{"tab"}},
		{act: "proj-repl-review.prev-file", chords: []string{"shift+tab"}},
	},
	// proj-repl-scroll
	"proj-repl-scroll": {
		{act: "proj-repl-scroll.up", chords: []string{"up", "k"}},
		{act: "proj-repl-scroll.down", chords: []string{"down", "j"}},
		{act: "proj-repl-scroll.page-up", chords: []string{"pgup"}},
		{act: "proj-repl-scroll.page-down", chords: []string{"pgdown"}},
		{act: "proj-repl-scroll.top", chords: []string{"home", "g"}},
		{act: "proj-repl-scroll.bottom", chords: []string{"end", "G"}},
		{act: "proj-repl-scroll.scroll-left", chords: []string{"left", "h"}},
		{act: "proj-repl-scroll.scroll-right", chords: []string{"right", "l"}},
	},
	// inline-ask
	"inline-ask": {
		{act: "inline-ask.close", chords: []string{"esc"}, flags: protected},
		{act: "inline-ask.run", chords: []string{"enter"}},
		{act: "inline-ask.erase", chords: []string{"backspace"}, flags: protected},
	},
	// inline-review
	"inline-review": {
		{act: "inline-review.accept", chords: []string{"y", "enter"}, flags: protected},
		{act: "inline-review.reject", chords: []string{"n", "esc"}, flags: protected},
		{act: "inline-review.up", chords: []string{"up", "k"}},
		{act: "inline-review.down", chords: []string{"down", "j"}},
		{act: "inline-review.page-up", chords: []string{"pgup"}},
		{act: "inline-review.page-down", chords: []string{"pgdown"}},
		{act: "inline-review.top", chords: []string{"home", "g"}},
		{act: "inline-review.bottom", chords: []string{"end", "G"}},
		{act: "inline-review.scroll-left", chords: []string{"left", "h"}},
		{act: "inline-review.scroll-right", chords: []string{"right", "l"}},
	},
	// fix-ask
	"fix-ask": {
		{act: "fix-ask.close", chords: []string{"esc"}, flags: protected},
		{act: "fix-ask.erase", chords: []string{"backspace"}, flags: protected},
		{act: "fix-ask.clear", chords: []string{"ctrl+u"}},
		{act: "fix-ask.run", chords: []string{"enter"}},
	},
	// fix-review
	"fix-review": {
		{act: "fix-review.accept", chords: []string{"y", "enter"}, flags: protected},
		{act: "fix-review.reject", chords: []string{"n", "esc", "r"}, flags: protected},
		{act: "fix-review.up", chords: []string{"up", "k"}},
		{act: "fix-review.down", chords: []string{"down", "j"}},
		{act: "fix-review.page-up", chords: []string{"pgup"}},
		{act: "fix-review.page-down", chords: []string{"pgdown"}},
		{act: "fix-review.top", chords: []string{"home", "g"}},
		{act: "fix-review.bottom", chords: []string{"end", "G"}},
		{act: "fix-review.scroll-left", chords: []string{"left", "h"}},
		{act: "fix-review.scroll-right", chords: []string{"right", "l"}},
	},
	// dap
	"dap": {
		{act: "dap.close", chords: []string{"esc"}, flags: protected},
		{act: "dap.erase", chords: []string{"backspace", "left"}},
		{act: "dap.peek", chords: []string{"l"}},
		{act: "dap.expand", chords: []string{"enter"}},
		{act: "dap.focus-next", chords: []string{"tab"}},
		{act: "dap.focus-prev", chords: []string{"shift+tab"}},
		{act: "dap.up", chords: []string{"up", "k"}},
		{act: "dap.down", chords: []string{"down", "j"}},
		{act: "dap.page-up", chords: []string{"pgup"}},
		{act: "dap.page-down", chords: []string{"pgdown"}},
		{act: "dap.top", chords: []string{"home"}},
		{act: "dap.bottom", chords: []string{"end"}},
		{act: "dap.grow", chords: []string{"+", "="}},
		{act: "dap.shrink", chords: []string{"-", "_"}},
		{act: "dap.clear", chords: []string{"ctrl+l"}},
	},
	// dap-console
	"dap-console": {
		{act: "dap-console.close", chords: []string{"esc"}, flags: protected},
		{act: "dap-console.erase", chords: []string{"backspace"}, flags: protected},
		{act: "dap-console.run", chords: []string{"enter"}},
		{act: "dap-console.focus-next", chords: []string{"tab"}},
		{act: "dap-console.focus-prev", chords: []string{"shift+tab"}},
		{act: "dap-console.clear", chords: []string{"ctrl+l"}},
	},
	// dap-cfg
	"dap-cfg": {
		{act: "dap-cfg.close", chords: []string{"esc"}, flags: protected},
		{act: "dap-cfg.next", chords: []string{"j", "down"}},
		{act: "dap-cfg.prev", chords: []string{"k", "up"}},
		{act: "dap-cfg.choice-prev", chords: []string{"left"}},
		{act: "dap-cfg.choice-next", chords: []string{"right"}},
		{act: "dap-cfg.open", chords: []string{"enter"}},
		{act: "dap-cfg.save", chords: []string{"ctrl+s"}},
	},
	// dap-cfg-field
	"dap-cfg-field": {
		{act: "dap-cfg-field.close", chords: []string{"esc"}, flags: protected},
		{act: "dap-cfg-field.run", chords: []string{"enter"}},
		{act: "dap-cfg-field.erase", chords: []string{"backspace"}, flags: protected},
	},
	// theme-cfg — the theme panel. The rows are the same shape as the settings
	// wizards: j/k between them, Enter into the one that is editable, Ctrl+S to
	// write it. The preview is painted from what the rows say, so no row has to be
	// saved before it can be judged.
	"theme-cfg": {
		{act: "theme-cfg.close", chords: []string{"esc"}, flags: protected},
		{act: "theme-cfg.next", chords: []string{"j", "down"}},
		{act: "theme-cfg.prev", chords: []string{"k", "up"}},
		{act: "theme-cfg.choice-prev", chords: []string{"left"}},
		{act: "theme-cfg.choice-next", chords: []string{"right"}},
		{act: "theme-cfg.open", chords: []string{"enter"}},
		{act: "theme-cfg.save", chords: []string{"ctrl+s"}},
	},
	// theme-cfg-field
	"theme-cfg-field": {
		{act: "theme-cfg-field.close", chords: []string{"esc"}, flags: protected},
		{act: "theme-cfg-field.run", chords: []string{"enter"}},
		{act: "theme-cfg-field.erase", chords: []string{"backspace"}, flags: protected},
	},
	// ai-cfg
	"ai-cfg": {
		{act: "ai-cfg.close", chords: []string{"esc"}, flags: protected},
		{act: "ai-cfg.next", chords: []string{"j", "down"}},
		{act: "ai-cfg.prev", chords: []string{"k", "up"}},
		{act: "ai-cfg.select", chords: []string{"left", "right"}},
		{act: "ai-cfg.test", chords: []string{"t", "T"}},
		{act: "ai-cfg.open", chords: []string{"enter"}},
		{act: "ai-cfg.save", chords: []string{"ctrl+s"}},
	},
	// ai-cfg-field
	"ai-cfg-field": {
		{act: "ai-cfg-field.close", chords: []string{"esc"}, flags: protected},
		{act: "ai-cfg-field.run", chords: []string{"enter"}},
		{act: "ai-cfg-field.erase", chords: []string{"backspace"}, flags: protected},
	},
	// ai-select
	"ai-select": {
		{act: "ai-select.close", chords: []string{"esc"}, flags: protected},
		{act: "ai-select.up", chords: []string{"up"}},
		{act: "ai-select.down", chords: []string{"down"}},
		{act: "ai-select.top", chords: []string{"home", "g"}},
		{act: "ai-select.bottom", chords: []string{"end", "G"}},
		{act: "ai-select.run", chords: []string{"enter"}},
	},
	// completion
	"completion": {
		{act: "completion.close", chords: []string{"esc"}, flags: protected},
		{act: "completion.accept", chords: []string{"tab", "enter"}},
		{act: "completion.up", chords: []string{"up", "ctrl+p"}},
		{act: "completion.down", chords: []string{"down", "ctrl+n"}},
		{act: "completion.page-up", chords: []string{"pgup"}},
		{act: "completion.page-down", chords: []string{"pgdown"}},
		{act: "completion.caret", chords: []string{"left", "right", "home", "end"}},
	},
}

// paneUnbound is what paneCmd reports for a command the user switched off. It is
// deliberately not a name a `[keys]` line can produce: a text pane that lost its
// backspace must not type one into the field instead.
const paneUnbound keyAct = "pane.unbound"

// paneDefaults indexes the declarations chord-first, once, at init. Read-only
// data: it is what "as shipped" means for a pane, so an unbind is distinguishable
// from a chord the pane never had.
var paneDefaults = buildPaneDefaults()

func buildPaneDefaults() map[paneID]map[string]keyAct {
	out := make(map[paneID]map[string]keyAct, len(paneCommands))
	for p, cmds := range paneCommands {
		m := make(map[string]keyAct, len(cmds)*2)
		for _, c := range cmds {
			for _, chord := range c.chords {
				m[chord] = c.act
			}
		}
		out[p] = m
	}
	return out
}

// paneOfAct is the inverse index a [keys] line needs to find the pane that owns
// the name it wrote.
var paneOfAct = buildActOwners()

func buildActOwners() map[keyAct]paneID {
	out := map[keyAct]paneID{}
	for p, cmds := range paneCommands {
		for _, c := range cmds {
			out[c.act] = p
		}
	}
	return out
}

// paneActCommand looks one command up by name, and reports its pane.
func paneActCommand(act keyAct) (paneCommand, paneID, bool) {
	p, ok := paneOfAct[act]
	if !ok {
		return paneCommand{}, "", false
	}
	for _, c := range paneCommands[p] {
		if c.act == act {
			return c, p, true
		}
	}
	return paneCommand{}, "", false
}

// paneActNames lists every declared command, sorted, for --dump-keys actions.
func paneActNames() []string {
	out := make([]string, 0, len(paneOfAct))
	for act := range paneOfAct {
		out = append(out, string(act))
	}
	sort.Strings(out)
	return out
}

// paneCmd answers which command of this pane the chord runs: the action name when
// the pane answers it, paneUnbound when the user switched a pane command off, and
// "" when the chord is not one of the pane's keys at all — which is what leaves a
// typed character to the handler's default arm.
func (m *Model) paneCmd(p paneID, s string) keyAct {
	if m.keys == nil {
		// A model that never resolved a key map answers with the shipped chords:
		// that is exactly what an unconfigured editor resolves to, and handlers are
		// reached from tests as well as from New().
		if a, ok := paneDefaults[p][s]; ok {
			return a
		}
		return ""
	}
	if a, ok := m.keys.pane[p][s]; ok {
		return a
	}
	if _, ok := paneDefaults[p][s]; ok {
		return paneUnbound
	}
	return ""
}
