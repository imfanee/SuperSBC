-- Structured-ish logging for the Lua pipeline. Every line carries the
-- a-leg uuid so it can be stitched with sbc-api and FreeSWITCH logs (D-13).
local log = {}
log.__index = log

function log.new(uuid)
  return setmetatable({ uuid = uuid or "?" }, log)
end

local function fmt(self, msg, fields)
  local parts = { "[sbc uuid=" .. self.uuid .. "] " .. msg }
  if fields then
    local keys = {}
    for k in pairs(fields) do
      keys[#keys + 1] = k
    end
    table.sort(keys)
    for _, k in ipairs(keys) do
      parts[#parts + 1] = k .. "=" .. tostring(fields[k])
    end
  end
  return table.concat(parts, " ") .. "\n"
end

function log:info(msg, fields)
  freeswitch.consoleLog("info", fmt(self, msg, fields))
end

function log:warn(msg, fields)
  freeswitch.consoleLog("warning", fmt(self, msg, fields))
end

function log:err(msg, fields)
  freeswitch.consoleLog("err", fmt(self, msg, fields))
end

function log:debug(msg, fields)
  freeswitch.consoleLog("debug", fmt(self, msg, fields))
end

return log
