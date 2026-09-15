export interface Listing<T> {
  items: T[];
  total: number;
  page: number;
  per_page: number;
}

export interface Me {
  id: string;
  email: string;
  role: "admin" | "operator" | "viewer";
  totp_enabled: boolean;
  csrf_token: string;
  via_api_key?: boolean;
}

export interface Customer {
  id: string;
  name: string;
  status: "active" | "suspended" | "blocked";
  rate_group_id: string | null;
  route_group_id: string | null;
  max_concurrent_calls: number;
  max_cps: number;
  allowed_codecs: string[];
  tech_prefix: string | null;
  default_country_code: string | null;
  intl_prefix: string;
  trust_pai: boolean;
  blocked_prefixes_enabled: boolean;
  media_mode: "anchor" | "proxy" | "bypass";
  dtmf_mode: "rfc2833" | "info" | "inband";
  srtp_mode: "off" | "optional" | "mandatory";
  require_tls: boolean;
  notes: string;
  created_at: string;
  updated_at: string;
}

export interface CustomerRow extends Customer {
  balance: string | null;
  allowed_credit: string | null;
  reserved: string | null;
  available: string | null;
  currency: string | null;
  ip_count: number;
}

export interface CustomerIP {
  id: string;
  customer_id: string;
  ip_cidr: string;
  port: number | null;
  transport: string;
  created_at: string;
}

export interface Account {
  id: string;
  owner_type: string;
  owner_id: string;
  currency: string;
  balance: string;
  allowed_credit: string;
  reserved: string;
}

export interface LedgerEntry {
  id: number;
  account_id: string;
  call_uuid: string | null;
  type: string;
  amount: string;
  balance_after: string;
  description: string;
  created_by: string | null;
  created_at: string;
}

export interface Carrier {
  id: string;
  name: string;
  status: "active" | "disabled";
  rate_group_id: string | null;
  gateway_host: string;
  gateway_port: number;
  transport: string;
  dni_prefix: string;
  ani_prefix: string;
  strip_digits: number;
  auth_username: string | null;
  from_domain: string | null;
  register: boolean;
  allowed_codecs: string[];
  max_concurrent_calls: number;
  max_cps: number;
  failover_sip_codes: number[] | null;
  sip_options_ping: boolean;
  charge_failed_attempts: boolean;
  ignore_early_media: boolean;
  media_mode: "anchor" | "proxy" | "bypass";
  dtmf_mode: "rfc2833" | "info" | "inband";
  srtp_mode: "off" | "optional" | "mandatory";
  privacy_mode: "anonymize" | "pass" | "ignore";
  notes: string;
  created_at: string;
}

export interface CarrierRow extends Carrier {
  balance: string | null;
  currency: string | null;
  rate_group_name: string | null;
  gateway_state: string;
  degraded: boolean;
}

export interface CarrierStatus {
  gateway: string;
  state: string;
  ping_enabled: boolean;
  since?: string;
  degraded?: boolean;
  degraded_reason?: string;
  attempts_5m?: number;
  answered_5m?: number;
  consecutive_faults?: number;
  calls_in_progress?: number;
  carrier_id?: string;
  name?: string;
  status?: string;
}

export interface RateGroup {
  id: string;
  name: string;
  currency: string;
  description: string;
  rate_count?: number;
  customer_count?: number;
  carrier_count?: number;
}

export interface Rate {
  id: string;
  rate_group_id: string;
  prefix: string;
  destination: string;
  rate_per_min: string;
  connect_fee: string;
  initial_increment: number;
  subsequent_increment: number;
  min_duration: number;
  effective_from: string;
  effective_to: string | null;
  enabled: boolean;
}

export interface RouteGroup {
  id: string;
  name: string;
  description: string;
  lcr_mode: boolean;
  route_count?: number;
  customer_count?: number;
}

export interface RouteCarrier {
  id?: string;
  route_id?: string;
  carrier_id: string;
  priority: number;
  weight: number;
  enabled: boolean;
  carrier_name?: string;
  carrier_status?: string;
}

export interface Route {
  id: string;
  route_group_id: string;
  prefix: string;
  destination: string;
  enabled: boolean;
  lcr_mode?: boolean;
  carriers: RouteCarrier[];
}

export interface Attempt {
  seq: number;
  carrier_id: string;
  carrier_name: string;
  gateway: string;
  sip_code: number;
  reason: string;
  hangup_cause: string;
  started_at: string;
  ended_at: string | null;
  pdd_ms: number | null;
  classification: string;
  buy_rate_per_min?: string;
}

export interface CDR {
  call_uuid: string;
  customer_id: string | null;
  carrier_id: string | null;
  customer_name: string | null;
  carrier_name: string | null;
  src_ip: string | null;
  src_port: number | null;
  caller_number: string;
  called_number: string;
  called_number_raw: string;
  start_time: string;
  progress_time: string | null;
  answer_time: string | null;
  end_time: string | null;
  pdd_ms: number | null;
  ring_seconds: number;
  billsec: number;
  duration: number;
  sip_final_code: number | null;
  sip_final_reason: string | null;
  hangup_cause: string | null;
  disposition: string;
  reject_reason: string | null;
  sell_rate_per_min: string | null;
  sell_billed_seconds: number;
  sell_price: string;
  sell_destination: string | null;
  sell_currency: string | null;
  buy_rate_per_min: string | null;
  buy_billed_seconds: number;
  cost: string;
  buy_currency: string | null;
  margin: string;
  negative_margin: boolean;
  reserved_amount: string;
  charged_amount: string;
  released_amount: string;
  attempts: Attempt[];
  failover_depth: number;
  codec_in: string | null;
  codec_out: string | null;
  media_mode: string | null;
  rtp_stats: Record<string, unknown> | null;
  transport_in: string | null;
  transport_out: string | null;
  srtp_in: boolean;
  srtp_out: boolean;
  privacy: boolean;
  sbc_node: string | null;
}

export interface ActiveCall {
  call_uuid: string;
  customer_id: string;
  customer_name: string;
  caller_number: string;
  called_number: string;
  src_ip: string | null;
  carrier_name: string | null;
  reserved_amount: string;
  max_call_seconds: number;
  started_at: string;
  answer_time: string | null;
  attempts: number;
  elapsed_seconds: number;
  state: string;
  freeswitch_seen: boolean;
}

export interface User {
  id: string;
  email: string;
  role: string;
  totp_enabled: boolean;
  status: string;
  last_login_at: string | null;
  created_at: string;
}

export interface APIKey {
  id: string;
  user_id: string;
  name: string;
  key_prefix: string;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export interface AuditEntry {
  id: number;
  actor_email: string | null;
  action: string;
  entity_type: string;
  entity_id: string | null;
  before: unknown;
  after: unknown;
  remote_ip: string | null;
  created_at: string;
}

export interface BlockedPrefix {
  id: string;
  customer_id: string | null;
  prefix: string;
  reason: string;
  enabled: boolean;
}

export interface FXRate {
  id: string;
  base: string;
  quote: string;
  rate: string;
  effective_from: string;
}

export interface SimulationStep {
  step: string;
  ok: boolean;
  detail: unknown;
}

export interface Simulation {
  customer: Customer;
  input_number: string;
  called?: string;
  steps: SimulationStep[];
  expected_sip: string;
  carriers?: Array<{
    seq: number;
    name: string;
    gateway: string;
    priority: number;
    weight: number;
    dial_number: string;
    caller_id: string;
    buy_rate_per_min: string | null;
    buy_destination: string;
    negative_margin: boolean;
    degraded: boolean;
    margin_per_min: string | null;
    dial_string: string;
  }>;
}

export interface HeaderRule {
  id: string;
  owner_type: "customer" | "carrier";
  owner_id: string;
  direction: "egress" | "response";
  action: "add" | "passthrough" | "remove";
  header: string;
  value: string;
  priority: number;
  enabled: boolean;
}

export interface BannedIP {
  ip: string;
  reason: string;
  hits: number;
  manual: boolean;
  banned_at: string;
  expires_at: string | null;
}
