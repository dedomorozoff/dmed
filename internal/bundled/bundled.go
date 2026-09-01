// Package bundled ships a small set of official plugins embedded in the
// binary so users can install them from the built-in plugin store without a
// network connection. Installed plugins are copied into ~/.dmed/plugins and
// behave like any other Lua plugin.
package bundled

import "embed"

//go:embed *.lua
var fs embed.FS

// Plugin describes one plugin offered in the built-in store.
type Plugin struct {
	File string
	Name string
	Desc string
}

// Store lists the installable plugins, in display order.
var Store = []Plugin{
	{File: "emmet.lua", Name: "Emmet", Desc: "Expand HTML/CSS abbreviations (e.g. div>ul>li*3)"},
	{File: "snippets.lua", Name: "Snippets", Desc: "Insert common code snippets via palette commands"},
}

// Source returns the .lua source for a bundled plugin file, or "" if unknown.
func Source(file string) string {
	data, err := fs.ReadFile(file)
	if err != nil {
		return ""
	}
	return string(data)
}
