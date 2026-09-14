-- OpenSBC ingress pipeline entry point. Milestone 1 replaces this stub with
-- the full state machine (see docs/CALL_FLOW.md).
local uuid = session:getVariable("uuid") or "?"
freeswitch.consoleLog("warning", "[sbc uuid=" .. uuid .. "] pipeline not installed yet, rejecting\n")
session:execute("respond", "503 SBC internal error")
