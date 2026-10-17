package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/imfanee/supersbc/internal/cache"
)

// Web authentication abuse (D-81). A wrong password is normal; a hundred of
// them from one address in an hour is a credential stuffer, and the only
// cheap place to stop it is the kernel, so the address goes on the same ban
// list the SIP scanner protection uses and the firewall drops it.
const (
	authAbuseKeyPrefix = "webauth:"
	authAbuseFallback  = 40 // tries in the window before a ban, when nothing is configured
)

// noteAuthAbuse counts one failed authentication from an address and bans it
// once it crosses the threshold. Every address is counted and reported; only
// an unknown public one is banned.
func (h *Handler) noteAuthAbuse(ctx context.Context, ip, what string) {
	if h.Redis == nil || ip == "" || net.ParseIP(ip) == nil {
		return
	}
	window := h.Cfg.Ban.Window
	if window <= 0 {
		window = time.Hour
	}
	key := authAbuseKeyPrefix + ip
	n, err := h.Redis.Incr(ctx, key).Result()
	if err != nil {
		return
	}
	if n == 1 {
		_ = h.Redis.Expire(ctx, key, window).Err()
	}
	threshold := h.Cfg.Ban.WebThreshold
	if threshold <= 0 {
		threshold = authAbuseFallback
	}
	if n != int64(threshold) { // act once, not on every try past the line
		return
	}
	reason := fmt.Sprintf("%d failed %s attempts in %s", n, what, window)

	// A customer or carrier address is reported, never banned: the ban list
	// is dropped before the rules that let their SIP in, so a ban over a
	// mistyped password would take their calls down with it.
	if known, err := h.Store.KnownSourceIP(ctx, ip); err != nil || known {
		h.Store.NoteSecurityEvent(ctx, "auth_abuse_known_source", ip, reason)
		h.Log.Warn("authentication abuse from a known source, not banned", "ip", ip, "reason", reason)
		return
	}
	// Neither is this node's own address or anything inside our own
	// networks, which would lock an operator out of their own box.
	if !bannableIP(ip) || h.ownAddress(ip) {
		h.Store.NoteSecurityEvent(ctx, "auth_abuse_local_source", ip, reason)
		h.Log.Warn("authentication abuse from a local address, not banned", "ip", ip, "reason", reason)
		return
	}
	duration := h.Cfg.Ban.Duration
	if duration <= 0 {
		duration = 24 * time.Hour
	}
	expires := time.Now().Add(duration)
	if _, err := h.Store.Ban(ctx, ip, "web "+reason, int(n), false, &expires); err != nil {
		h.Log.Error("ban web scanner", "ip", ip, "error", err)
		return
	}
	h.Store.NoteSecurityEvent(ctx, "auth_abuse_banned", ip, reason)
	h.publish(ctx, cache.ChanBansChanged, ip)
	h.Log.Warn("banned an address for authentication abuse", "ip", ip, "reason", reason, "until", expires)
}

// ownAddress reports whether an address is this node's own: its advertised
// signalling address or one of its interfaces. Banning it would make the box
// drop traffic from itself.
func (h *Handler) ownAddress(ip string) bool {
	if ip == "" {
		return false
	}
	if ip == h.Cfg.FSNodeIP {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == ip {
			return true
		}
	}
	return false
}

// bannableIP keeps the ban list to addresses that can safely be dropped: a
// ban on the loopback or a docker bridge would lock the operator out of their
// own node.
func bannableIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

// noteAuthFailure is the one call site helper: record the attempt and count
// it towards a ban.
func (h *Handler) noteAuthFailure(r *http.Request, email, what string) {
	ip := remoteIP(r)
	if email != "" {
		h.Store.RecordLoginAttempt(r.Context(), email, ip, false)
	}
	h.noteAuthAbuse(r.Context(), ip, what)
}
