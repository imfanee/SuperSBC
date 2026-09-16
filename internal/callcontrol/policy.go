package callcontrol

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imfanee/supersbc/internal/model"
)

// Section 7 [M5] policies: media mode, DTMF interworking, privacy, SRTP and
// header manipulation. Pure functions, unit tested.

// MediaModeFor combines the customer and carrier media policies: anchoring
// wins over proxying, proxying over bypass (the most conservative side decides).
func MediaModeFor(customer, carrier string) string {
	if customer == "anchor" || carrier == "anchor" || customer == "" || carrier == "" {
		return "anchor"
	}
	if customer == "proxy" || carrier == "proxy" {
		return "proxy"
	}
	return "bypass"
}

// dtmfVars returns the b-leg variables for a carrier DTMF mode.
//
//	rfc2833: telephone-event negotiated
//	info:    SIP INFO
//	inband:  no telephone-event; tones detected (start_dtmf) and generated (start_dtmf_generate) in the audio
func dtmfVars(mode string) []string {
	switch mode {
	case "info":
		return []string{"dtmf_type=info"}
	case "inband":
		return []string{"dtmf_type=none", "execute_on_answer_1=start_dtmf", "execute_on_answer_2=start_dtmf_generate"}
	default:
		return []string{"dtmf_type=rfc2833"}
	}
}

// ALegDTMF returns the a-leg variables and applications for a customer DTMF mode.
func ALegDTMF(mode string) (vars map[string]string, apps []App) {
	switch mode {
	case "info":
		return map[string]string{"dtmf_type": "info"}, nil
	case "inband":
		return map[string]string{"dtmf_type": "none"}, []App{{App: "start_dtmf"}, {App: "start_dtmf_generate"}}
	default:
		return map[string]string{"dtmf_type": "rfc2833"}, nil
	}
}

// srtpVar maps an srtp_mode to the FreeSWITCH rtp_secure_media value.
func srtpVar(mode string) string {
	switch mode {
	case "mandatory":
		return "mandatory"
	case "optional":
		return "optional"
	default:
		return "false"
	}
}

// PrivacyVars returns the caller id and b-leg variables for a privacy call
// according to the carrier's privacy_mode.
//
//	anonymize: From is anonymous, Privacy: id, no identity header
//	pass:      real From, Privacy: id and P-Asserted-Identity with the real number (the carrier applies privacy)
//	ignore:    caller id as usual, no Privacy header
func PrivacyVars(mode, realCaller, nodeIP string) (callerID string, vars []string) {
	switch mode {
	case "pass":
		return realCaller, []string{"sip_h_Privacy=id", fmt.Sprintf("sip_h_P-Asserted-Identity=<sip:%s@%s>", realCaller, nodeIP)}
	case "ignore":
		return realCaller, nil
	default:
		return "anonymous", []string{"origination_privacy=hide_name:hide_number", "sip_h_Privacy=id"}
	}
}

// HeaderContext provides placeholder values for header rule values.
type HeaderContext struct {
	Caller   string
	Called   string
	Customer string
	Carrier  string
	CallUUID string
	NodeIP   string
}

func expand(v string, c HeaderContext) string {
	r := strings.NewReplacer("{caller}", c.Caller, "{called}", c.Called, "{customer}", c.Customer, "{carrier}", c.Carrier, "{call_uuid}", c.CallUUID, "{node_ip}", c.NodeIP)
	return r.Replace(v)
}

// HeaderPlan is the result of merging header rules for one call and carrier.
type HeaderPlan struct {
	// EgressVars are b-leg dial string variables (sip_h_<Header>=value).
	EgressVars []string
	// Passthrough lists a-leg headers Lua copies onto the carrier INVITE.
	Passthrough []string
	// ResponseVars are a-leg variables (sip_rh_<Header>=value) for responses to the customer.
	ResponseVars map[string]string
}

// ApplyHeaderRules merges customer rules then carrier rules (a carrier rule
// for the same header and direction overrides the customer's); "remove"
// cancels any earlier add or passthrough for that header.
func ApplyHeaderRules(customerRules, carrierRules []model.HeaderRule, ctx HeaderContext) HeaderPlan {
	type entry struct {
		action string
		value  string
		order  int
	}
	egress := map[string]entry{}
	response := map[string]entry{}
	order := 0
	apply := func(rules []model.HeaderRule) {
		sorted := append([]model.HeaderRule(nil), rules...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
		for _, r := range sorted {
			if !r.Enabled {
				continue
			}
			key := strings.ToLower(r.Header)
			target := egress
			if r.Direction == "response" {
				target = response
			}
			if r.Action == "remove" {
				delete(target, key)
				continue
			}
			order++
			target[key] = entry{action: r.Action, value: r.Value, order: order}
		}
	}
	apply(customerRules)
	apply(carrierRules)
	plan := HeaderPlan{ResponseVars: map[string]string{}}
	type kv struct {
		key string
		e   entry
	}
	sortedEntries := func(m map[string]entry) []kv {
		out := make([]kv, 0, len(m))
		for k, e := range m {
			out = append(out, kv{k, e})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].e.order < out[j].e.order })
		return out
	}
	for _, x := range sortedEntries(egress) {
		name := canonicalHeader(x.key)
		switch x.e.action {
		case "add":
			plan.EgressVars = append(plan.EgressVars, "sip_h_"+name+"="+escapeDialValue(expand(x.e.value, ctx)))
		case "passthrough":
			plan.Passthrough = append(plan.Passthrough, name)
		}
	}
	for _, x := range sortedEntries(response) {
		if x.e.action == "add" {
			plan.ResponseVars["sip_rh_"+canonicalHeader(x.key)] = expand(x.e.value, ctx)
		}
	}
	return plan
}

// canonicalHeader restores the conventional capitalisation (x-account-id -> X-Account-Id).
func canonicalHeader(lower string) string {
	parts := strings.Split(lower, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "-")
}

// escapeDialValue makes a value safe inside the {var=value,...} prefix of a
// dial string: commas separate variables and are escaped with a backslash.
func escapeDialValue(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, ",", "\\,")
	v = strings.ReplaceAll(v, "}", "")
	return v
}
