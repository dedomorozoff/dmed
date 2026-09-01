# Plugins (Lua)

dmed plugins are plain `.lua` scripts that run against an embedded
[gopher-lua](https://github.com/yuin/gopher-lua) VM (pure Go, no cgo). They can
add keybindings, command-palette commands, and event hooks, and read/edit the
active buffer through a `dmed` global.

## Locations

Plugins are loaded at startup from:

- `~/.dmed/plugins/` — global, for all projects
- `<project>/.dmed/plugins/` — per-project

Every `*.lua` file in these directories is loaded (in sorted order). A plugin
that fails to parse is skipped and reported in the status line. Example plugins
live in [`examples/plugins/`](../examples/plugins/).

## Hot-reload

Plugins are watched and reloaded automatically whenever their `.lua` source
changes on disk — no editor restart needed. The plugin's `ready` event is fired
again after each reload so it can re-initialize. If the edited source fails to
parse, the previous version stays active and the error is shown in the status
line.

## Built-in store

The **Plugins: Install...** palette command (`Ctrl+P`) opens a store that
lists plugins with their install state; press `Enter` to install or remove one.
Two sources are merged:

- **Embedded** — a small always-available set baked into the binary (works
  offline): Emmet, Snippets.
- **Remote** — any `*.lua` under the `plugins/` directory of the configured
  GitHub repo, fetched live on open.

Remote source is set with `[plugins]` config keys (defaults shown):

```ini
[plugins]
repo   = dedomorozoff/dmed   # "owner/repo"
dir    = plugins             # directory holding .lua plugins
branch = main
```

`DMED_PLUGIN_REPO` overrides `repo`. Installed store plugins behave like any
other `.lua` plugin and hot-reload the same way. Add a new official plugin by
placing its `.lua` in the repo's `plugins/` directory.

## API

### Keybindings

```lua
dmed.on_key("ctrl+u", function()
  dmed.set_text(dmed.text():upper())
end)
```

Bindings use bubbletea key names (`ctrl+x`, `alt+d`, `f1`, `enter`, ...).
Plugin bindings are checked before built-in keys, so they can override them.

### Palette commands

```lua
dmed.command("my_id", "My: Do Thing", "short description", function()
  dmed.insert("hello")
end)
```

The command appears in the command palette (`Ctrl+P`) under the given title.

### Events

```lua
dmed.on("ready", function() end)      -- after all plugins load
dmed.on("file_open", function() end)  -- a file tab was opened
dmed.on("save", function() end)       -- the active buffer was saved
```

### Buffer / editor functions

| Function | Description |
| --- | --- |
| `dmed.text()` | full buffer text (string) |
| `dmed.set_text(s)` | replace the whole buffer |
| `dmed.line_count()` | number of lines |
| `dmed.line(i)` | text of line `i` (0-based) |
| `dmed.cursor()` | table `{line=, col=}` (0-based) |
| `dmed.set_cursor(line, col)` | move the cursor |
| `dmed.insert(s)` | insert text at the cursor |
| `dmed.status(msg)` | show a message in the status line |
| `dmed.save()` | save the active tab |

## Examples

`examples/plugins/uppercase.lua`:

```lua
dmed.on_key("ctrl+u", function()
  dmed.set_text(dmed.text():upper())
  dmed.status("uppercased")
end)

dmed.command("demo_upper", "Demo: Uppercase Buffer", "Convert the file to upper case", function()
  dmed.set_text(dmed.text():upper())
end)
```

Install by copying a `.lua` file into `~/.dmed/plugins/` (it loads on next
startup, or is hot-reloaded if already running).