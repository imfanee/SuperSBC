std = "lua52"
globals = { "freeswitch", "session", "argv", "env", "api", "stream", "event" }
max_line_length = 140
ignore = { "212/self" }
files["freeswitch/scripts/sbc/json.lua"] = { ignore = { ".*" } }
