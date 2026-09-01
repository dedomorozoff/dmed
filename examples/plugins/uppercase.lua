-- Example plugin: Ctrl+U uppercases the whole buffer, and a palette command
-- "Demo: Uppercase" does the same. Demonstrates keybindings, commands and the
-- buffer API (dmed.text / dmed.set_text).
dmed.on_key("ctrl+u", function()
  dmed.set_text(dmed.text():upper())
  dmed.status("uppercased")
end)

dmed.command("demo_upper", "Demo: Uppercase Buffer", "Convert the whole file to upper case", function()
  dmed.set_text(dmed.text():upper())
end)