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
--   (a>b)+(c)           grouping
--   div>{text}          literal text content

local M = {}

-- splitTop splits s on sep, ignoring separators inside (), [] or quotes.
function M.splitTop(s, sep)
  local parts, depth, quote, cur = {}, 0, nil, ""
  for i = 1, #s do
    local c = s:sub(i, i)
    if quote then
      cur = cur .. c
      if c == quote then quote = nil end
    elseif c == '"' or c == "'" then
      quote = c
      cur = cur .. c
    elseif c == "(" then
      depth = depth + 1
      cur = cur .. c
    elseif c == ")" then
      depth = depth - 1
      cur = cur .. c
    elseif depth == 0 and c == sep then
      parts[#parts + 1] = cur
      cur = ""
    else
      cur = cur .. c
    end
  end
  parts[#parts + 1] = cur
  return parts
end

-- parseNode parses a single element: tag(.class)*(#id)?[*N], optionally with
-- a {text} literal and/or a (group) after it.
function M.parseNode(s)
  local node = { tag = "div", attrs = "", children = {}, text = nil }

  -- literal text content {..}
  local beforeText, text = s:match("^(.-)%{([^}]*)%}$")
  if beforeText then
    node.text = text
    s = beforeText
  end

  -- trailing group (sub-expression) becomes children
  local base, group = s:match("^(.-)%((.*)%)$")
  if base then
    s = base
    for _, e in ipairs(M.parseSeq(group)) do
      node.children[#node.children + 1] = e
    end
  end

  -- repeat *N
  local base2, rep = s:match("^(.-)%*([0-9]+)$")
  if base2 then
    node.count = tonumber(rep)
    s = base2
  end

  -- id #id
  local idPart = s:match("#([%w%-_]+)")
  -- classes .class
  local cls = {}
  for c in (s .. "."):gmatch("%.([%w%-_]+)") do
    cls[#cls + 1] = c
  end
  -- tag name
  local name = s:match("^[%w-]+") or "div"
  node.tag = name

  local a = ""
  if idPart then
    a = a .. ' id="' .. idPart .. '"'
  end
  if #cls > 0 then
    a = a .. ' class="' .. table.concat(cls, " ") .. '"'
  end
  node.attrs = a
  return node
end

-- parseChain parses a ">" chain into nested nodes.
function M.parseChain(s)
  local parts = M.splitTop(s, ">")
  local roots = {}
  local prev
  for _, p in ipairs(parts) do
    local node = M.parseNode(p)
    if prev then
      prev.children = { node }
    else
      roots[#roots + 1] = node
    end
    prev = node
  end
  return roots
end

-- parseSeq parses a "+" separated sequence of chains.
function M.parseSeq(s)
  local out = {}
  for _, p in ipairs(M.splitTop(s, "+")) do
    if p ~= "" then
      for _, e in ipairs(M.parseChain(p)) do
        out[#out + 1] = e
      end
    end
  end
  return out
end

function M.emitChildren(node, out, indent)
  for _, ch in ipairs(node.children) do
    M.emit(ch, out, indent)
  end
end

function M.emit(node, out, indent)
  local pad = string.rep("  ", indent)
  local reps = node.count or 1
  for _ = 1, reps do
    local openTag = "<" .. node.tag .. node.attrs .. ">"
    local closeTag = "</" .. node.tag .. ">"
    if #node.children == 0 and not node.text then
      out[#out + 1] = pad .. openTag .. closeTag
    elseif node.text then
      out[#out + 1] = pad .. openTag .. node.text .. closeTag
    else
      out[#out + 1] = pad .. openTag
      M.emitChildren(node, out, indent + 1)
      out[#out + 1] = pad .. closeTag
    end
  end
end

-- expand turns an abbreviation into an HTML string (nil on parse failure).
function M.expand(s)
  s = s:gsub("%s+", "")
  if s == "" then
    return nil
  end
  local els = M.parseSeq(s)
  if #els == 0 then
    return nil
  end
  local out = {}
  for _, e in ipairs(els) do
    M.emit(e, out, 0)
  end
  return table.concat(out, "\n")
end

dmed.command("emmet_expand", "Emmet: Expand", "Expand an HTML abbreviation at the cursor", function()
  local pos = dmed.cursor()
  local line_no, col = pos.line, pos.col
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