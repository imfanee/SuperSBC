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

// staticPayloads are the RTP audio payload types with a fixed meaning
// (RFC 3551), which an SDP offer is allowed to leave out of its rtpmap lines.
var staticPayloads = map[string]string{
	"0":  "PCMU",
	"3":  "GSM",
	"4":  "G723",
	"8":  "PCMA",
	"9":  "G722",
	"18": "G729",
}

// notAudio are payloads that appear in an audio offer but are not codecs to
// bridge: DTMF events and comfort noise.
func notAudio(name string) bool {
	return strings.EqualFold(name, "telephone-event") || strings.EqualFold(name, "CN")
}

// offeredFrom returns the codecs the customer offered, in the order they
// offered them, from the INVITE's SDP, falling back to FreeSWITCH's parsed
// ep_codec_string when there is no usable SDP. The SDP has to come first:
// ep_codec_string only lists codecs the sofia profile declares, so a G.729
// only INVITE arrives as nil there and the whole of D-85 would quietly become
// a no-op (D-87).
func offeredFrom(sdp, epCodecString string) string {
	if codecs := sdpAudioCodecs(sdp); len(codecs) > 0 {
		return strings.Join(codecs, ",")
	}
	return epCodecString
}

// sdpAudioCodecs reads the codec names out of an SDP offer's first audio
// media section, in the order the m= line lists them.
func sdpAudioCodecs(sdp string) []string {
	if sdp == "" {
		return nil
	}
	var payloads []string
	names := map[string]string{}
	inAudio := false
	for _, raw := range strings.Split(sdp, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "m="):
			if inAudio {
				// a second media section: the audio one is done
				return namedCodecs(payloads, names)
			}
			if !strings.HasPrefix(line, "m=audio ") {
				continue
			}
			inAudio = true
			// m=audio <port> <proto> <pt> <pt> ...
			if f := strings.Fields(line); len(f) > 3 {
				payloads = append(payloads, f[3:]...)
			}
		case inAudio && strings.HasPrefix(line, "a=rtpmap:"):
			// a=rtpmap:<pt> <name>/<clock>[/<channels>]
			f := strings.Fields(strings.TrimPrefix(line, "a=rtpmap:"))
			if len(f) < 2 {
				continue
			}
			name := f[1]
			if i := strings.IndexByte(name, '/'); i >= 0 {
				name = name[:i]
			}
			names[f[0]] = name
		}
	}
	return namedCodecs(payloads, names)
}

// namedCodecs turns a payload list into codec names, using the offer's rtpmap
// lines and then the static payload assignments.
func namedCodecs(payloads []string, names map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, pt := range payloads {
		name := names[pt]
		if name == "" {
			name = staticPayloads[pt]
		}
		if name == "" || notAudio(name) || seen[strings.ToUpper(name)] {
			continue
		}
		seen[strings.ToUpper(name)] = true
		out = append(out, name)
	}
	return out
}

// offeredCodecs parses a FreeSWITCH ep_codec_string into plain codec names.
// Each entry is "<module>.<codec>@<rate>h@<ptime>i@<bitrate>b", for example
// "CORE_PCM_MODULE.PCMU@8000h@20i@64000b,mod_bcg729.G729@8000h@20i@8000b".
// Both the module prefix and the parameters have to go: comparing
// "CORE_PCM_MODULE.PCMU" with the configured "PCMU" matches nothing, which is
// how the narrowing in D-85 came to be a no-op on real calls. The prefix is
// not always there, and no codec name contains a dot, so everything up to the
// last dot is dropped. telephone-event is not an audio codec.
func offeredCodecs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		if name == "" || notAudio(name) {
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
