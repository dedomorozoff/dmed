-- Example plugin: a couple of snippet commands and a save-status hook.
--
--   * Palette command "Snippet: Go main" inserts a Go main function at the
--     cursor.
--   * The "save" event shows "saved (plugin)" in the status line.
dmed.command("snippet_go_main", "Snippet: Go main", "Insert a Go main function", function()
  dmed.insert("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n")
end)

dmed.on("save", function()
  dmed.status("saved (plugin)")
end)