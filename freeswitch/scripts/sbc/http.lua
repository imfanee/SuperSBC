-- HTTP client for the Lua pipeline, built on the mod_curl "curl" API (D-02).
--
-- mod_curl limits: at most 10 space separated arguments, argument data is
-- split on spaces, so the JSON body is encoded with every space, quote and
-- backslash as \uXXXX (see sbc.json). The hard timeout is enforced by
-- libcurl inside FreeSWITCH; when it expires the caller fails closed.
local json = require("sbc.json")

local http = {}

http.base_url = "http://api:8081"
http.secret = ""
http.timeout = 2

--- Configures the client from FreeSWITCH global variables set in vars.xml.
function http.configure(api)
  local base = api:executeString("global_getvar sbc_api_base")
  if base and base ~= "" and not base:find("^%-ERR") then
    http.base_url = base
  end
  local secret = api:executeString("global_getvar sbc_internal_secret")
  if secret and secret ~= "" and not secret:find("^%-ERR") then
    http.secret = secret
  end
end

--- POSTs a table as JSON. Returns (status_code, decoded_body) or (nil, err).
function http.post_json(api, path, body, call_uuid, timeout)
  local ok, payload = pcall(json.encode, body)
  if not ok then
    return nil, "encode: " .. tostring(payload)
  end
  local cmd = table.concat({
    http.base_url .. path,
    "json",
    "post", payload,
    "timeout", tostring(timeout or http.timeout),
    "append_headers", "X-SBC-Secret:" .. http.secret,
    "append_headers", "X-Call-UUID:" .. (call_uuid or "-"),
  }, " ")
  local raw = api:executeString("curl " .. cmd)
  if not raw or raw == "" or raw:find("^%-ERR") then
    return nil, "curl failed: " .. tostring(raw)
  end
  local env, err = json.decode(raw)
  if not env then
    return nil, "bad envelope: " .. tostring(err)
  end
  local status = tonumber(env.status_code)
  if not status or status == 0 then
    return nil, "no http status (timeout or connection refused)"
  end
  local decoded = nil
  if env.body and env.body ~= "" then
    decoded, err = json.decode(env.body)
    if decoded == nil and err then
      return status, nil, "bad body: " .. tostring(err)
    end
  end
  return status, decoded
end

return http
