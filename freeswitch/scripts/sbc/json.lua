-- Minimal JSON encoder/decoder for the FreeSWITCH Lua 5.2 VM.
--
-- Encoding is tuned for mod_curl (D-02): the "curl" API splits its argument
-- string on spaces, treats single quotes as grouping and backslash as an
-- escape character. Therefore every character that could confuse it (space,
-- double quote, single quote, backslash, control characters) is emitted as a
-- \uXXXX escape, which mod_curl passes through untouched and every JSON
-- parser accepts.
local json = {}

local escape_map = {}
for i = 0, 31 do
  escape_map[string.char(i)] = string.format("\\u%04x", i)
end
escape_map[" "] = "\\u0020"
escape_map['"'] = "\\u0022"
escape_map["'"] = "\\u0027"
escape_map["\\"] = "\\u005c"
escape_map["\127"] = "\\u007f"

local function encode_string(s)
  return '"' .. s:gsub("[%c \"'\\\127]", escape_map) .. '"'
end

local function is_array(t)
  local n = 0
  for k in pairs(t) do
    if type(k) ~= "number" then
      return false
    end
    n = n + 1
  end
  return n == #t
end

local encode_value

local function encode_table(t)
  if next(t) == nil then
    -- Empty tables encode as {} unless marked as an array with json.array().
    if getmetatable(t) == json.array_mt then
      return "[]"
    end
    return "{}"
  end
  if getmetatable(t) == json.array_mt or is_array(t) then
    local out = {}
    for i = 1, #t do
      out[i] = encode_value(t[i])
    end
    return "[" .. table.concat(out, ",") .. "]"
  end
  local keys = {}
  for k in pairs(t) do
    keys[#keys + 1] = tostring(k)
  end
  table.sort(keys)
  local out = {}
  for _, k in ipairs(keys) do
    local v = t[k]
    if v == nil then
      v = t[tonumber(k)]
    end
    out[#out + 1] = encode_string(k) .. ":" .. encode_value(v)
  end
  return "{" .. table.concat(out, ",") .. "}"
end

encode_value = function(v)
  local tv = type(v)
  if v == nil or v == json.null then
    return "null"
  elseif tv == "boolean" then
    return v and "true" or "false"
  elseif tv == "number" then
    if v ~= v or v == math.huge or v == -math.huge then
      return "null"
    end
    if math.floor(v) == v and math.abs(v) < 1e15 then
      return string.format("%d", v)
    end
    return string.format("%.14g", v)
  elseif tv == "string" then
    return encode_string(v)
  elseif tv == "table" then
    return encode_table(v)
  end
  error("json: cannot encode " .. tv)
end

json.null = setmetatable({}, { __tostring = function() return "null" end })
json.array_mt = {}

--- Marks a table as a JSON array (so an empty one encodes as []).
function json.array(t)
  return setmetatable(t or {}, json.array_mt)
end

--- Encodes a Lua value as compact JSON safe for mod_curl.
function json.encode(v)
  return encode_value(v)
end

-- ---------------------------------------------------------------- decoder

local decode_value

local function skip_ws(s, i)
  return s:find("[^ \t\r\n]", i) or (#s + 1)
end

local escapes = { ['"'] = '"', ["\\"] = "\\", ["/"] = "/", b = "\b", f = "\f", n = "\n", r = "\r", t = "\t" }

local function utf8_char(cp)
  if cp < 0x80 then
    return string.char(cp)
  elseif cp < 0x800 then
    return string.char(0xC0 + math.floor(cp / 0x40), 0x80 + cp % 0x40)
  elseif cp < 0x10000 then
    return string.char(0xE0 + math.floor(cp / 0x1000), 0x80 + math.floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
  end
  return string.char(0xF0 + math.floor(cp / 0x40000), 0x80 + math.floor(cp / 0x1000) % 0x40,
    0x80 + math.floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
end

local function decode_string(s, i)
  -- s[i] == '"'
  local out = {}
  local j = i + 1
  while true do
    local c = s:sub(j, j)
    if c == "" then
      error("json: unterminated string")
    elseif c == '"' then
      return table.concat(out), j + 1
    elseif c == "\\" then
      local e = s:sub(j + 1, j + 1)
      if e == "u" then
        local hex = s:sub(j + 2, j + 5)
        local cp = tonumber(hex, 16)
        if not cp then
          error("json: bad unicode escape")
        end
        j = j + 6
        if cp >= 0xD800 and cp <= 0xDBFF and s:sub(j, j + 1) == "\\u" then
          local lo = tonumber(s:sub(j + 2, j + 5), 16)
          if lo and lo >= 0xDC00 and lo <= 0xDFFF then
            cp = 0x10000 + (cp - 0xD800) * 0x400 + (lo - 0xDC00)
            j = j + 6
          end
        end
        out[#out + 1] = utf8_char(cp)
      else
        local r = escapes[e]
        if not r then
          error("json: bad escape \\" .. e)
        end
        out[#out + 1] = r
        j = j + 2
      end
    else
      local k = s:find('["\\]', j) or (#s + 1)
      out[#out + 1] = s:sub(j, k - 1)
      j = k
    end
  end
end

local function decode_number(s, i)
  local j = s:find("[^-+.eE%d]", i) or (#s + 1)
  local n = tonumber(s:sub(i, j - 1))
  if not n then
    error("json: bad number at " .. i)
  end
  return n, j
end

decode_value = function(s, i)
  i = skip_ws(s, i)
  local c = s:sub(i, i)
  if c == "{" then
    local obj = {}
    i = skip_ws(s, i + 1)
    if s:sub(i, i) == "}" then
      return obj, i + 1
    end
    while true do
      i = skip_ws(s, i)
      if s:sub(i, i) ~= '"' then
        error("json: expected key at " .. i)
      end
      local k
      k, i = decode_string(s, i)
      i = skip_ws(s, i)
      if s:sub(i, i) ~= ":" then
        error("json: expected ':' at " .. i)
      end
      local v
      v, i = decode_value(s, i + 1)
      obj[k] = v
      i = skip_ws(s, i)
      local d = s:sub(i, i)
      if d == "," then
        i = i + 1
      elseif d == "}" then
        return obj, i + 1
      else
        error("json: expected ',' or '}' at " .. i)
      end
    end
  elseif c == "[" then
    local arr = json.array({})
    i = skip_ws(s, i + 1)
    if s:sub(i, i) == "]" then
      return arr, i + 1
    end
    while true do
      local v
      v, i = decode_value(s, i)
      arr[#arr + 1] = v
      i = skip_ws(s, i)
      local d = s:sub(i, i)
      if d == "," then
        i = i + 1
      elseif d == "]" then
        return arr, i + 1
      else
        error("json: expected ',' or ']' at " .. i)
      end
    end
  elseif c == '"' then
    return decode_string(s, i)
  elseif s:sub(i, i + 3) == "true" then
    return true, i + 4
  elseif s:sub(i, i + 4) == "false" then
    return false, i + 5
  elseif s:sub(i, i + 3) == "null" then
    return nil, i + 4
  elseif c:match("[-%d]") then
    return decode_number(s, i)
  end
  error("json: unexpected character '" .. c .. "' at " .. i)
end

--- Decodes JSON. Returns the value, or nil and an error message.
function json.decode(s)
  if type(s) ~= "string" then
    return nil, "json: input is not a string"
  end
  local ok, v, i = pcall(decode_value, s, 1)
  if not ok then
    return nil, v
  end
  i = skip_ws(s, i)
  if i <= #s then
    return nil, "json: trailing garbage at " .. i
  end
  return v
end

return json
