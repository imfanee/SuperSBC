-- OpenSBC ingress entry point: runs the pipeline for every INVITE that
-- reaches the "public" dialplan context. Any Lua error is caught and turned
-- into "503 SBC internal error" so a bug can never leave a call hanging.
local ok, err = pcall(function()
  local pipeline = require("sbc.pipeline")
  pipeline.run(session)
end)
if not ok then
  local uuid = session:getVariable("uuid") or "?"
  freeswitch.consoleLog("err", "[sbc uuid=" .. uuid .. "] pipeline error: " .. tostring(err) .. "\n")
  if session:ready() then
    session:setVariable("sbc_final_sip_code", "503")
    session:setVariable("sbc_final_reason", "SBC internal error")
    session:setVariable("sbc_reject_reason", "lua error")
    session:execute("respond", "503 SBC internal error")
    session:hangup()
  end
end
