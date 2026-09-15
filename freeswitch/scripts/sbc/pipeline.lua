-- The ingress call pipeline (Section 3). This module is a thin state
-- machine: every decision comes from sbc-api, every fact goes back to it.
-- Business logic lives in Go; this file only sequences bridges and relays
-- SIP responses. Fail-closed: any API failure ends the call with
-- "503 SBC internal error".
local http = require("sbc.http")
local logmod = require("sbc.log")

local pipeline = {}

local INTERNAL_ERROR = { code = 503, reason = "SBC internal error" }

-- Reads a microsecond timestamp from FreeSWITCH.
local function now_us(api)
  local v = api:executeString("strmicroepoch")
  return tonumber(v) or (os.time() * 1000000)
end

local function iso_from_us(us)
  local secs = math.floor(us / 1000000)
  local frac = us - secs * 1000000
  return os.date("!%Y-%m-%dT%H:%M:%S", secs) .. string.format(".%06dZ", frac)
end

local function set_vars(session, vars)
  for k, v in pairs(vars or {}) do
    session:setVariable(k, tostring(v))
  end
end

-- Privacy (Section 7): a Privacy header asking to hide identity, or an anonymous From.
local function privacy_requested(session)
  local priv = (session:getVariable("sip_Privacy") or session:getVariable("sip_i_privacy")
    or session:getVariable("sip_h_Privacy") or ""):lower()
  if priv:find("id", 1, true) or priv:find("user", 1, true) or priv:find("header", 1, true) then
    return true
  end
  local from = (session:getVariable("sip_from_user") or ""):lower()
  return from == "anonymous"
end

-- SRTP offered: crypto attributes or a secure profile in the received SDP
-- (the rtp_has_crypto variable only appears after negotiation).
local function srtp_offered(session)
  local sdp = session:getVariable("switch_r_sdp") or ""
  if sdp:find("a=crypto:", 1, true) or sdp:find("RTP/SAVP", 1, true) then
    return true
  end
  return (session:getVariable("rtp_has_crypto") or "") ~= ""
end

-- User part of P-Asserted-Identity when the customer sent one.
local function pai_user(session)
  local pai = session:getVariable("sip_P-Asserted-Identity") or session:getVariable("sip_h_P-Asserted-Identity") or ""
  local user = pai:match("sip:([^@>;]+)") or pai:match("tel:([^>;]+)")
  return user or ""
end

-- Executes the dialplan applications the API asked for on the a-leg.
local function run_apps(session, apps)
  for _, a in ipairs(apps or {}) do
    if a.app and a.app ~= "" then
      session:execute(a.app, a.data or "")
    end
  end
end

-- Sends a final response with the exact reason phrase and hangs up.
local function reject(session, log, code, reason, step)
  if session:ready() then
    session:setVariable("sbc_final_sip_code", tostring(code))
    session:setVariable("sbc_final_reason", reason)
    if step then
      session:setVariable("sbc_reject_step", step)
    end
    session:execute("respond", code .. " " .. reason)
    session:hangup()
  end
  log:info("rejected", { code = code, reason = reason, step = step })
end

-- Live channel timestamps (microseconds). The *_uepoch channel variables
-- only exist at hangup, so the caller profile times are read from uuid_dump.
local function channel_times(api, uuid)
  local dump = api:executeString("uuid_dump " .. uuid) or ""
  local function get(key)
    return tonumber(dump:match(key .. ":%s*(%d+)")) or 0
  end
  return {
    progress = get("Caller%-Channel%-Progress%-Time"),
    progress_media = get("Caller%-Channel%-Progress%-Media%-Time"),
    answered = get("Caller%-Channel%-Answered%-Time"),
  }
end

-- Parses "sip:503" style proto specific causes.
local function sip_code_from(session)
  local psc = session:getVariable("last_bridge_proto_specific_hangup_cause") or ""
  local code = tonumber(psc:match("^sip:(%d+)"))
  if code then
    return code
  end
  psc = session:getVariable("bridge_hangup_cause") or ""
  return tonumber(psc:match("^sip:(%d+)"))
end

--- Runs the whole pipeline for the current session.
function pipeline.run(session)
  local api = freeswitch.API()
  http.configure(api)
  local uuid = session:getVariable("uuid") or "?"
  local log = logmod.new(uuid)

  local req = {
    call_uuid = uuid,
    src_ip = session:getVariable("sip_network_ip") or "",
    src_port = tonumber(session:getVariable("sip_network_port")) or 0,
    transport = (session:getVariable("sip_via_protocol") or "udp"):lower(),
    -- The From user is the ANI (D-43). FreeSWITCH's caller_id_number would
    -- prefer a customer supplied P-Asserted-Identity / Remote-Party-ID.
    caller = session:getVariable("sip_from_user") or session:getVariable("caller_id_number") or "",
    called = session:getVariable("destination_number") or session:getVariable("sip_req_user") or "",
    sip_call_id = session:getVariable("sip_call_id") or "",
    offered_codecs = session:getVariable("ep_codec_string") or "",
    node = session:getVariable("switchname") or freeswitch.getGlobalVariable("hostname") or "",
    -- Section 7 [M5]: SRTP offered, privacy requested, network asserted identity
    srtp_offered = srtp_offered(session),
    privacy = privacy_requested(session),
    pai_number = pai_user(session),
    -- STIR/SHAKEN Identity header (D-64); Sofia exposes unknown headers as sip_h_<name>.
    identity = session:getVariable("sip_h_Identity") or "",
  }
  log:info("invite", { src = req.src_ip .. ":" .. req.src_port, caller = req.caller, called = req.called })

  -- Steps 1 to 7 in one round trip (D-16).
  local status, decision, derr = http.post_json(api, "/internal/v1/call/setup", req, uuid)
  if status ~= 200 or type(decision) ~= "table" then
    log:err("setup failed, failing closed", { status = tostring(status), err = tostring(derr or decision) })
    session:setVariable("sbc_reject_reason", "SBC internal error")
    return reject(session, log, INTERNAL_ERROR.code, INTERNAL_ERROR.reason, "setup")
  end
  set_vars(session, decision.vars)
  run_apps(session, decision.apps)

  if decision.action ~= "dial" then
    local r = decision.reject or INTERNAL_ERROR
    return reject(session, log, r.code, r.reason, decision.reject_step)
  end

  -- Step 8: dial with failover.
  local carriers = decision.carriers or {}
  local max_secs = tonumber(decision.max_call_seconds) or 0
  session:setVariable("continue_on_fail", "true")
  session:setVariable("hangup_after_bridge", "true")
  -- Header hygiene towards the customer: no Remote-Party-ID / P-Asserted-Identity
  -- in our responses, and RTP timeouts (media_timeout is milliseconds).
  session:setVariable("sip_cid_type", "none")
  -- Customer X-* headers stay on this side of the B2BUA (checked on the a-leg by mod_sofia).
  session:setVariable("sip_copy_custom_headers", "false")
  session:setVariable("media_timeout", tostring(decision.media_timeout_ms or 300000))
  session:setVariable("media_hold_timeout", tostring(decision.media_hold_timeout_ms or 1800000))
  session:setVariable("sbc_answered", "false")
  session:setVariable("sbc_attempts", tostring(#carriers))
  if max_secs > 0 then
    -- The customer can never talk past their money (Section 3 Step 5).
    session:setVariable("execute_on_answer", "sched_hangup +" .. max_secs .. " ALLOTTED_TIMEOUT")
  end
  -- Codec policy (Section 7): the customer leg only negotiates the customer's
  -- allowed codecs; the carrier leg gets the intersection in the dial string.
  -- FreeSWITCH transcodes when the two legs end up with different codecs.
  if decision.customer_codecs and decision.customer_codecs ~= "" then
    session:setVariable("absolute_codec_string", decision.customer_codecs)
  end
  -- Relay a ringing indication once the carrier does; nothing before.

  local relay_code, relay_reason = 503, "All carriers failed"
  local answered = false

  for i, c in ipairs(carriers) do
    if not session:ready() then
      log:info("customer gone before attempt", { seq = i })
      break
    end
    -- Carrier capacity check (Section 7): skip carriers at their channel or CPS limit.
    local bst, begin = http.post_json(api, "/internal/v1/call/attempt/begin",
      { call_uuid = uuid, seq = i, carrier_id = c.carrier_id }, uuid)
    if bst == 200 and type(begin) == "table" and begin.allowed == false then
      log:info("carrier skipped", { seq = i, carrier = c.name, reason = tostring(begin.reason) })
      goto continue
    end
    local started = now_us(api)
    local before = channel_times(api, uuid)
    local progress_before = math.max(before.progress, before.progress_media)
    session:setVariable("sbc_carrier_id", c.carrier_id)
    session:setVariable("sbc_carrier_name", c.name)
    session:setVariable("sbc_attempt_seq", tostring(i))
    -- Media mode for this customer and carrier pair (Section 7): anchor (default),
    -- proxy (signalling anchored, media relayed untouched) or bypass (media direct).
    session:setVariable("bypass_media", c.media_mode == "bypass" and "true" or "false")
    session:setVariable("proxy_media", c.media_mode == "proxy" and "true" or "false")
    -- Header passthrough rules: copy the named a-leg headers onto this INVITE.
    local dial_string = c.dial_string
    for _, name in ipairs(c.passthrough_headers or {}) do
      local v = session:getVariable("sip_h_" .. name)
      if v and v ~= "" then
        v = v:gsub(",", "\\,"):gsub("}", "")
        dial_string = dial_string:gsub("^{", "{sip_h_" .. name .. "=" .. v .. ",", 1)
      end
    end
    log:info("attempt", { seq = i, carrier = c.name, dial = c.dial_number, media = c.media_mode or "anchor" })

    session:execute("bridge", dial_string)

    local ended = now_us(api)
    local disposition = session:getVariable("originate_disposition") or ""
    local cause = session:getVariable("last_bridge_hangup_cause") or session:getVariable("originate_failed_cause") or disposition
    local sip_code = sip_code_from(session) or 0
    local aleg_gone = not session:ready()
    local was_answered = (disposition == "SUCCESS")
    local pdd_ms = nil
    local after = channel_times(api, uuid)
    local progress = math.max(after.progress, after.progress_media)
    local answer = after.answered
    if progress > progress_before and progress >= started then
      pdd_ms = math.floor((progress - started) / 1000)
    elseif answer >= started and answer > 0 then
      pdd_ms = math.floor((answer - started) / 1000)
    end
    local ring_seconds = 0
    if progress >= started and progress > 0 then
      local ring_end = ended
      if answer > progress then
        ring_end = answer
      end
      ring_seconds = math.floor((ring_end - progress) / 1000000)
    end
    if was_answered then
      answered = true
      session:setVariable("sbc_answered", "true")
      cause = session:getVariable("last_bridge_hangup_cause") or "NORMAL_CLEARING"
      sip_code = 200
    end

    local attempt = {
      call_uuid = uuid,
      seq = i,
      carrier_id = c.carrier_id,
      sip_code = sip_code,
      reason = "",
      hangup_cause = cause,
      answered = was_answered,
      aleg_gone = aleg_gone and not was_answered,
      ring_seconds = ring_seconds,
      pdd_ms = pdd_ms,
      started_at = iso_from_us(started),
      ended_at = iso_from_us(ended),
      buy_rate_id = c.buy_rate_id,
      buy_rate_per_min = c.buy_rate_per_min,
    }
    local st, verdict = http.post_json(api, "/internal/v1/call/attempt", attempt, uuid)
    log:info("attempt result", {
      seq = i, carrier = c.name, disposition = disposition, cause = cause, sip_code = sip_code,
      pdd_ms = tostring(pdd_ms), classification = (verdict and verdict.classification) or "api_error",
    })
    if was_answered then
      break
    end
    if aleg_gone then
      break
    end
    if st == 200 and type(verdict) == "table" then
      relay_code = verdict.relay_code or relay_code
      relay_reason = verdict.relay_reason or relay_reason
      if not verdict["continue"] then
        break
      end
    else
      -- API unreachable mid-call: do not keep dialling blind.
      log:err("attempt report failed, stopping failover", { status = tostring(st) })
      relay_code, relay_reason = INTERNAL_ERROR.code, INTERNAL_ERROR.reason
      break
    end
    ::continue::
  end

  if answered then
    -- hangup_after_bridge already tore the a-leg down; nothing to send.
    log:info("call ended after answer", { cause = session:getVariable("last_bridge_hangup_cause") or "" })
    return
  end
  if not session:ready() then
    session:setVariable("sbc_final_sip_code", "487")
    session:setVariable("sbc_final_reason", "Request Terminated")
    log:info("customer cancelled")
    return
  end
  return reject(session, log, relay_code, relay_reason, "dial")
end

return pipeline
