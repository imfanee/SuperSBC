package callcontrol

import "strings"

// Codec selection for the carrier leg (D-83, D-85).
//
// The rule: if a codec the caller offered in the ingress INVITE is allowed by
// both the customer and the carrier, the egress INVITE offers only the
// matching codecs. The carrier then has nothing else to choose from, both legs
// run the same codec, and the media can be proxied with no decode and
// re-encode. If the caller offered nothing the carrier allows, the egress
// INVITE offers the carrier's codecs and the call is transcoded, which is the
// only way to bridge two different codecs.
//
// This is what keeps a G.729 only caller alive on an image whose mod_g729 can
// only pass through: the carrier answers G.729 because that is all it was
// offered, instead of answering PCMA and asking FreeSWITCH for an encoder it
// does not have.

// codecStringFor builds the carrier leg's codec list. offered is the a-leg's
// FreeSWITCH ep_codec_string.
func codecStringFor(customer, carrier []string, offered string) string {
	if len(carrier) == 0 {
		return strings.Join(customer, ",")
	}
	if len(customer) == 0 {
		return strings.Join(carrier, ",")
	}
	allowed := allowedCodecs(customer, carrier)
	if len(allowed) == 0 {
		// Misconfiguration: the two lists have nothing in common. The carrier
		// decides, as it did before any of this.
		return strings.Join(carrier, ",")
	}
	if matched := matchedCodecs(offered, customer, carrier); len(matched) > 0 {
		return strings.Join(matched, ",")
	}
	return strings.Join(allowed, ",")
}

// allowedCodecs is the intersection of the customer's and the carrier's
// allowed lists, in the carrier's order of preference.
func allowedCodecs(customer, carrier []string) []string {
	var out []string
	for _, c := range carrier {
		if containsFold(customer, c) {
			out = append(out, c)
		}
	}
	return out
}

// matchedCodecs returns the codecs the caller offered that both the customer
// and the carrier allow, in the order the caller offered them, spelled as the
// carrier's configuration spells them. Empty means the call has to be
// transcoded.
func matchedCodecs(offered string, customer, carrier []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range offeredCodecs(offered) {
		key := strings.ToUpper(w)
		if seen[key] || !containsFold(customer, w) {
			continue
		}
		for _, c := range carrier {
			if strings.EqualFold(c, w) {
				out = append(out, c)
				seen[key] = true
				break
			}
		}
	}
	return out
}

// offeredCodecs parses a FreeSWITCH ep_codec_string
// ("G729@8000h@20i@8000b,PCMU@8000h@20i@64000b") into plain codec names.
// telephone-event is not an audio codec and is dropped.
func offeredCodecs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		if name == "" || strings.EqualFold(name, "telephone-event") {
			continue
		}
		out = append(out, name)
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, c := range list {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}
