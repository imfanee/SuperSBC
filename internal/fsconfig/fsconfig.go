// Package fsconfig renders FreeSWITCH gateway and ACL XML from the database
// into the shared configuration volume and asks FreeSWITCH to reload (D-04).
package fsconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opensbc/opensbc/internal/esl"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/store"
)

// Renderer writes gateway and ACL files.
type Renderer struct {
	dir     string
	aclMode string // "dialplan" (default) or "strict"
	// banFile mirrors the ban list for the host firewall (D-66); empty disables.
	banFile string
	nodeIP  string
	st      *store.Store
	esl     *esl.Supervisor
	log     *slog.Logger

	mu       sync.Mutex
	gateways map[string]string // name -> content hash of last render
}

// New creates a renderer writing into dir/gateways and dir/acl.
func New(dir, aclMode, nodeIP string, st *store.Store, sup *esl.Supervisor, log *slog.Logger) *Renderer {
	return &Renderer{dir: dir, aclMode: aclMode, nodeIP: nodeIP, st: st, esl: sup, log: log, gateways: map[string]string{}}
}

// RenderAll renders gateways and ACLs and reloads FreeSWITCH.
func (r *Renderer) RenderAll(ctx context.Context) error {
	if err := r.RenderGateways(ctx); err != nil {
		return err
	}
	return r.RenderACLs(ctx)
}

// RenderGateways writes one XML file per active carrier and rescans the
// egress profile. Changed or removed gateways are killed first so the
// rescan picks up the new definition.
func (r *Renderer) RenderGateways(ctx context.Context) error {
	carriers, err := r.st.Carriers(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.dir, "gateways")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]string{}
	for _, c := range carriers {
		if c.Status != "active" {
			continue
		}
		want[c.GatewayName()] = GatewayXML(c, r.nodeIP)
	}
	var killed []string
	// remove stale files
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".xml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".xml")
		if _, ok := want[name]; !ok {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			killed = append(killed, name)
			delete(r.gateways, name)
		}
	}
	for name, xml := range want {
		h := hash(xml)
		path := filepath.Join(dir, name+".xml")
		if r.gateways[name] == h {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		if err := writeAtomic(path, xml); err != nil {
			return err
		}
		if old, ok := r.gateways[name]; ok && old != h {
			killed = append(killed, name)
		}
		r.gateways[name] = h
	}
	if r.esl != nil && r.esl.Connected() {
		for _, name := range killed {
			_, _ = r.esl.API(ctx, "sofia profile external-egress killgw "+name)
		}
		if _, err := r.esl.API(ctx, "sofia profile external-egress rescan"); err != nil {
			r.log.Warn("sofia rescan failed", "error", err)
		}
	}
	r.log.Info("gateways rendered", "count", len(want), "killed", killed)
	return nil
}

// RenderACLs writes acl/customers.xml and acl/carriers.xml and reloads.
func (r *Renderer) RenderACLs(ctx context.Context) error {
	ips, err := r.st.AllCustomerIPs(ctx)
	if err != nil {
		return err
	}
	carriers, err := r.st.Carriers(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.dir, "acl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	banned, err := r.st.BannedIPs(ctx)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "customers.xml"), CustomersACL(ips, banned, r.aclMode)); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "carriers.xml"), CarriersACL(carriers)); err != nil {
		return err
	}
	if r.esl != nil && r.esl.Connected() {
		if _, err := r.esl.API(ctx, "reloadacl"); err != nil {
			r.log.Warn("reloadacl failed", "error", err)
		}
	}
	// Ingress profile fragment (D-67): the TLS client certificate subjects of
	// the customers. Sofia reads it when the profile (re)starts.
	idir := filepath.Join(r.dir, "ingress")
	if err := os.MkdirAll(idir, 0o755); err != nil {
		return err
	}
	customers, err := r.st.Customers(ctx)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(idir, "tls_subjects.xml"), TLSSubjects(customers)); err != nil {
		return err
	}
	if r.banFile != "" {
		if err := writeAtomic(r.banFile, BanExport(banned, time.Now())); err != nil {
			r.log.Warn("ban export failed", "file", r.banFile, "error", err)
		}
	}
	r.log.Info("acls rendered", "customer_ips", len(ips), "banned", len(banned), "mode", r.aclMode)
	return nil
}

// TLSSubjects renders the tls-verify-in-subjects parameter of the ingress
// profile from the customers' certificate subjects (D-67). Sofia matches a
// presented certificate's CN and subject alternative names against the list
// when tls-verify-policy includes subjects_in.
func TLSSubjects(customers []model.Customer) string {
	seen := map[string]bool{}
	var subjects []string
	for _, c := range customers {
		if c.Status != "active" {
			continue
		}
		for _, s := range strings.Split(c.TLSSubject, ",") {
			s = strings.TrimSpace(s)
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			subjects = append(subjects, s)
		}
	}
	sort.Strings(subjects)
	var b strings.Builder
	b.WriteString("<!-- generated by sbc-api: customer TLS client certificate subjects (D-67); restart the ingress profile to apply -->\n")
	b.WriteString("<include>\n")
	fmt.Fprintf(&b, "  <param name=\"tls-verify-in-subjects\" value=\"%s\"/>\n", html.EscapeString(strings.Join(subjects, ",")))
	b.WriteString("</include>\n")
	return b.String()
}

// SetBanExportFile enables the ban list export (D-66).
func (r *Renderer) SetBanExportFile(path string) { r.banFile = path }

// BanExport renders the ban list for deploy/live/ban-sync.sh: one line per
// address, "ip seconds_left" (0 = permanent), expired entries omitted.
func BanExport(banned []model.BannedIP, now time.Time) string {
	var b strings.Builder
	b.WriteString("# OpenSBC banned addresses; generated " + now.UTC().Format(time.RFC3339) + "\n")
	for _, ip := range banned {
		secs := 0
		if ip.ExpiresAt != nil {
			left := ip.ExpiresAt.Sub(now)
			if left <= 0 {
				continue
			}
			secs = int(left.Seconds()) + 1
		}
		fmt.Fprintf(&b, "%s %d\n", ip.IP, secs)
	}
	return b.String()
}

// GatewayXML renders one carrier as a Sofia gateway.
func GatewayXML(c model.Carrier, nodeIP string) string {
	proxy := net.JoinHostPort(c.GatewayHost, fmt.Sprint(c.GatewayPort))
	if c.Transport == "tcp" || c.Transport == "tls" {
		proxy += ";transport=" + c.Transport
	}
	user := c.Name
	if c.AuthUsername != nil && *c.AuthUsername != "" {
		user = *c.AuthUsername
	}
	pass := "none"
	if c.AuthPassword != nil && *c.AuthPassword != "" {
		pass = *c.AuthPassword
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- rendered by sbc-api at %s from carrier %s; do not edit -->\n", time.Now().UTC().Format(time.RFC3339), c.ID)
	fmt.Fprintf(&b, "<gateway name=%q>\n", c.Name)
	fmt.Fprintf(&b, "  <param name=\"proxy\" value=%q/>\n", proxy)
	fmt.Fprintf(&b, "  <param name=\"register\" value=\"%t\"/>\n", c.Register)
	fmt.Fprintf(&b, "  <param name=\"username\" value=%q/>\n", user)
	fmt.Fprintf(&b, "  <param name=\"password\" value=%q/>\n", pass)
	// From host: the carrier's from_domain when set, else our own address
	// (topology hiding: the carrier only ever sees the SBC), else the proxy.
	fromDomain := c.GatewayHost
	if nodeIP != "" {
		fromDomain = nodeIP
	}
	if c.FromDomain != nil && *c.FromDomain != "" {
		fromDomain = *c.FromDomain
	}
	fmt.Fprintf(&b, "  <param name=\"from-domain\" value=%q/>\n", fromDomain)
	fmt.Fprintf(&b, "  <param name=\"realm\" value=%q/>\n", c.GatewayHost)
	if c.Transport == "tcp" || c.Transport == "tls" {
		fmt.Fprintf(&b, "  <param name=\"register-transport\" value=%q/>\n", c.Transport)
	}
	fmt.Fprintf(&b, "  <param name=\"caller-id-in-from\" value=\"true\"/>\n")
	fmt.Fprintf(&b, "  <param name=\"extension-in-contact\" value=\"true\"/>\n")
	fmt.Fprintf(&b, "  <param name=\"retry-seconds\" value=\"30\"/>\n")
	if c.SIPOptionsPing {
		fmt.Fprintf(&b, "  <param name=\"ping\" value=\"30\"/>\n")
		fmt.Fprintf(&b, "  <param name=\"ping-max\" value=\"2\"/>\n")
		fmt.Fprintf(&b, "  <param name=\"ping-min\" value=\"1\"/>\n")
	}
	fmt.Fprintf(&b, "  <variables>\n")
	fmt.Fprintf(&b, "    <variable name=\"sbc_carrier_name\" value=%q direction=\"outbound\"/>\n", c.Name)
	if len(c.AllowedCodecs) > 0 {
		fmt.Fprintf(&b, "    <variable name=\"sbc_carrier_codecs\" value=%q direction=\"outbound\"/>\n", strings.Join(c.AllowedCodecs, ","))
	}
	fmt.Fprintf(&b, "  </variables>\n")
	fmt.Fprintf(&b, "</gateway>\n")
	return b.String()
}

// CustomersACL renders the customers network list. In "strict" mode unknown
// addresses are dropped by Sofia before the dialplan; in "dialplan" mode the
// list allows everything so the Lua pipeline can answer 403 IP not authorized.
func CustomersACL(ips []model.CustomerIP, banned []model.BannedIP, mode string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- rendered by sbc-api at %s; mode=%s; do not edit -->\n", time.Now().UTC().Format(time.RFC3339), mode)
	if mode == "strict" {
		b.WriteString("<list name=\"customers\" default=\"deny\">\n")
	} else {
		b.WriteString("<list name=\"customers\" default=\"allow\">\n")
	}
	seen := map[string]bool{}
	var cidrs []string
	for _, ip := range ips {
		if !seen[ip.IPCIDR] {
			seen[ip.IPCIDR] = true
			cidrs = append(cidrs, ip.IPCIDR)
		}
	}
	sort.Strings(cidrs)
	for _, c := range cidrs {
		fmt.Fprintf(&b, "  <node type=\"allow\" cidr=%q/>\n", c)
	}
	// Banned addresses come last: FreeSWITCH evaluates every node and the last
	// match wins, so a ban overrides an allowed range (scanner protection).
	for _, x := range banned {
		cidr := x.IP
		if !strings.Contains(cidr, "/") {
			if strings.Contains(cidr, ":") {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}
		fmt.Fprintf(&b, "  <node type=\"deny\" cidr=%q/>\n", cidr)
	}
	b.WriteString("</list>\n")
	return b.String()
}

// CarriersACL renders the carriers network list (allow gateway hosts).
func CarriersACL(carriers []model.Carrier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- rendered by sbc-api at %s; do not edit -->\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("<list name=\"carriers\" default=\"allow\">\n")
	for _, c := range carriers {
		if ip := net.ParseIP(c.GatewayHost); ip != nil {
			bits := "/32"
			if ip.To4() == nil {
				bits = "/128"
			}
			fmt.Fprintf(&b, "  <node type=\"allow\" cidr=%q/>\n", ip.String()+bits)
		}
	}
	b.WriteString("</list>\n")
	return b.String()
}

func hash(s string) string {
	// Ignore the timestamp comment line so unchanged content hashes equal.
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func writeAtomic(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
