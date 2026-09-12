-- Emmet-style HTML abbreviation expansion for dmed.
--
-- Put the cursor right after an abbreviation (e.g. "div>ul>li*3") and run the
-- palette command "Emmet: Expand" (Ctrl+P -> Emmet: Expand). The abbreviation
-- is replaced by the generated markup.
--
-- Supported syntax:
--   tag                 <tag></tag>
--   tag.class#id        with class and/or id
--   a>b>c               nesting (children)
--   a+b                 siblings
--   li*3                repetition
--   (a>b)+(c)           grouping (flattens into the parent, no wrapper)
--   (li>a)*2            repeat a whole group
--   div>{text}          literal text content

local M = {}

-- Parser: recursive descent over a cursor into `src`.
local src
local pos
local parseExpr, parseTerm, parseUnit, readText

local function peek()
  return src:sub(pos, pos)
end

local function readWhile(chars)
  local out = ""
  while true do
    local c = peek()
    if c == "" or not chars:find(c, 1, true) then
      break
    end
    out = out .. c
    pos = pos + 1
  end
  return out
end

-- readText consumes {…} and returns the literal text inside (spaces allowed).
readText = function()
  if peek() ~= "{" then
    return nil
  end
  pos = pos + 1
  local close = src:find("}", pos, true)
  local text
  if close then
    text = src:sub(pos, close - 1)
    pos = close + 1
  else
    text = src:sub(pos)
    pos = #src + 1
  end
  return text
end

parseTerm = function()
  local nodes = parseUnit()
  while peek() == ">" do
    pos = pos + 1
    local tail = nodes[#nodes]
    while #tail.children > 0 do
      tail = tail.children[#tail.children]
    end
    if peek() == "{" then
      -- A text node after '>' becomes the element's content (no wrapper).
      local t = readText()
      if tail.text then
        tail.text = tail.text .. t
      else
        tail.text = t
      end
    else
      local nxt = parseUnit()
      tail.children = nxt
    end
  end
  return nodes
end

parseExpr = function()
  local roots = {}
  while true do
    local nodes = parseTerm()
    for _, n in ipairs(nodes) do
      roots[#roots + 1] = n
    end
    if peek() == "+" then
      pos = pos + 1
    else
      break
    end
  end
  return roots
end

parseUnit = function()
  -- Group: (expr) optionally followed by *N.
  if peek() == "(" then
    pos = pos + 1
    local inner = parseExpr()
    if peek() == ")" then
      pos = pos + 1
    end
    local rep = 1
    if peek() == "*" then
      pos = pos + 1
      rep = tonumber(readWhile("0123456789")) or 1
    end
    local out = {}
    for _, n in ipairs(inner) do
      out[#out + 1] = { tag = n.tag, attrs = n.attrs, count = (n.count or 1) * rep, children = n.children, text = n.text }
    end
    return out
  end

  -- Element: name [.class]* [#id]? [*N] [{text}]
  local name = readWhile("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_")
  if name == "" then
    name = "div"
  end
  local cls = {}
  while peek() == "." do
    pos = pos + 1
    local c = readWhile("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_")
    if c ~= "" then
      cls[#cls + 1] = c
    end
  end
  local id
  if peek() == "#" then
    pos = pos + 1
    id = readWhile("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_")
  end
  local rep = 1
  if peek() == "*" then
    pos = pos + 1
    rep = tonumber(readWhile("0123456789")) or 1
  end
  local text = readText()

  local attrs = ""
  if id then
    attrs = attrs .. ' id="' .. id .. '"'
  end
  if #cls > 0 then
    attrs = attrs .. ' class="' .. table.concat(cls, " ") .. '"'
  end
  return { { tag = name, attrs = attrs, count = rep, children = {}, text = text } }
end

local function emit(node, out, indent)
  local pad = string.rep("  ", indent)
  local reps = node.count or 1
  for _ = 1, reps do
    local openTag = "<" .. node.tag .. node.attrs .. ">"
    local closeTag = "</" .. node.tag .. ">"
    if node.text then
      out[#out + 1] = pad .. openTag .. node.text .. closeTag
    elseif #node.children > 0 then
      out[#out + 1] = pad .. openTag
      for _, ch in ipairs(node.children) do
        emit(ch, out, indent + 1)
      end
      out[#out + 1] = pad .. closeTag
    else
      out[#out + 1] = pad .. openTag .. closeTag
    end
  end
end

-- expand turns an abbreviation into an HTML string (nil on parse failure).
function M.expand(abbr)
  src = abbr:gsub("%s+", "")
  if src == "" then
    return nil
  end
  pos = 1
  local roots = parseExpr()
  if #roots == 0 then
    return nil
  end
  local out = {}
  for _, n in ipairs(roots) do
    emit(n, out, 0)
  end
  return table.concat(out, "\n")
end

dmed.command("emmet_expand", "Emmet: Expand", "Expand an HTML abbreviation at the cursor", function()
  local p = dmed.cursor()
  local line_no, col = p.line, p.col
  local line = dmed.line(line_no)

  local prefix = line:sub(1, col)
  local token = prefix:match("([%w#.%[%]\"'=%+>^*(){}]+)$") or ""

  if token == "" then
    dmed.status("emmet: no abbreviation before cursor")
    return
  end

  local html = M.expand(token)
  if html == nil then
    dmed.status("emmet: cannot parse")
    return
  end

  dmed.set_cursor(line_no, math.max(0, col - #token))
  dmed.insert(html)
  dmed.status("emmet expanded")
end)