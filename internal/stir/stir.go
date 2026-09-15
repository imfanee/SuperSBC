// Package stir verifies STIR/SHAKEN Identity headers (RFC 8224, RFC 8225
// PASSporT, ATIS-1000074 "shaken" extension) on customer INVITEs (D-64).
//
// The verifier checks the ES256 signature with the certificate fetched from
// the x5u URL of the token (cached), the freshness of iat, and that the
// originating and destination numbers of the token match the call. Chain
// validation against a trusted CA list (the STI-PA list in the US) is
// enabled when a CA bundle file is configured; without one the certificate
// is only checked for validity dates and signature.
package stir

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Status values recorded in the CDR.
const (
	StatusNone     = "none"     // no Identity header
	StatusVerified = "verified" // signature, certificate, freshness and numbers all good
	StatusStale    = "stale"    // signature good but iat outside the freshness window
	StatusMismatch = "mismatch" // signature good but orig or dest does not match the call
	StatusNoCert   = "no_cert"  // x5u certificate could not be fetched or parsed
	StatusInvalid  = "invalid"  // malformed header, unsupported alg/ppt, bad signature or untrusted certificate
)

// Result is the outcome of a verification.
type Result struct {
	Status string `json:"status"`
	Attest string `json:"attest,omitempty"` // A, B or C
	OrigTN string `json:"orig_tn,omitempty"`
	OrigID string `json:"origid,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SIP response for a failed verification in "require" mode (RFC 8224 section 6.2.2).
func (r Result) RejectCode() (int, string) {
	switch r.Status {
	case StatusNone:
		return 428, "Use Identity Header"
	case StatusNoCert:
		return 436, "Bad Identity Info"
	case StatusStale, StatusMismatch, StatusInvalid:
		return 438, "Invalid Identity Header"
	}
	return 0, ""
}

// Config tunes the verifier.
type Config struct {
	MaxAge       time.Duration // freshness window for iat (RFC 8224 recommends 60 s)
	CAFile       string        // PEM bundle of trusted STI-CA roots; empty disables chain validation
	AllowHTTP    bool          // accept http:// x5u (tests only; production certificates are https)
	FetchTimeout time.Duration
	CacheTTL     time.Duration
}

// Verifier verifies Identity headers.
type Verifier struct {
	cfg   Config
	http  *http.Client
	roots *x509.CertPool
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	cert    *x509.Certificate
	err     error
	expires time.Time
}

// New creates a verifier. A missing CA file is an error; an empty CAFile is not.
func New(cfg Config) (*Verifier, error) {
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 60 * time.Second
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 1500 * time.Millisecond
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Hour
	}
	v := &Verifier{cfg: cfg, http: &http.Client{Timeout: cfg.FetchTimeout}, now: time.Now, cache: map[string]cached{}}
	if cfg.CAFile != "" {
		pemData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("stir ca file: %w", err)
		}
		v.roots = x509.NewCertPool()
		if !v.roots.AppendCertsFromPEM(pemData) {
			return nil, errors.New("stir ca file: no certificates found")
		}
	}
	return v, nil
}

type header struct {
	Alg string `json:"alg"`
	PPT string `json:"ppt"`
	Typ string `json:"typ"`
	X5U string `json:"x5u"`
}

type tns struct {
	TN []string `json:"tn"`
}

type payload struct {
	Attest string          `json:"attest"`
	Dest   tns             `json:"dest"`
	IAT    int64           `json:"iat"`
	Orig   json.RawMessage `json:"orig"`
	OrigID string          `json:"origid"`
}

// Verify checks the Identity header value of a call from caller to called
// (E.164 digits, with or without a leading plus).
func (v *Verifier) Verify(ctx context.Context, identity, caller, called string) Result {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return Result{Status: StatusNone}
	}
	// "<jws>;info=<url>;alg=ES256;ppt=shaken"
	parts := strings.Split(identity, ";")
	jws := strings.TrimSpace(parts[0])
	params := map[string]string{}
	for _, p := range parts[1:] {
		k, val, _ := strings.Cut(strings.TrimSpace(p), "=")
		params[strings.ToLower(k)] = strings.Trim(val, "<>\"")
	}
	segs := strings.Split(jws, ".")
	if len(segs) != 3 {
		return Result{Status: StatusInvalid, Error: "identity is not a compact JWS"}
	}
	hb, err1 := base64.RawURLEncoding.DecodeString(segs[0])
	pb, err2 := base64.RawURLEncoding.DecodeString(segs[1])
	sig, err3 := base64.RawURLEncoding.DecodeString(segs[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Result{Status: StatusInvalid, Error: "identity is not base64url"}
	}
	var h header
	var p payload
	if json.Unmarshal(hb, &h) != nil || json.Unmarshal(pb, &p) != nil {
		return Result{Status: StatusInvalid, Error: "identity header or payload is not JSON"}
	}
	if !strings.EqualFold(h.Alg, "ES256") || (h.PPT != "" && h.PPT != "shaken") || (params["alg"] != "" && !strings.EqualFold(params["alg"], "ES256")) {
		return Result{Status: StatusInvalid, Error: "unsupported alg or ppt"}
	}
	res := Result{Attest: strings.ToUpper(p.Attest), OrigID: p.OrigID}
	x5u := h.X5U
	if x5u == "" {
		x5u = params["info"]
	}
	if x5u == "" {
		return Result{Status: StatusInvalid, Error: "no x5u"}
	}
	cert, err := v.certificate(ctx, x5u)
	if err != nil {
		res.Status, res.Error = StatusNoCert, err.Error()
		return res
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		res.Status, res.Error = StatusInvalid, "certificate key is not ECDSA"
		return res
	}
	if len(sig) != 64 {
		res.Status, res.Error = StatusInvalid, "signature is not 64 bytes"
		return res
	}
	sum := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, sum[:], r, s) {
		res.Status, res.Error = StatusInvalid, "bad signature"
		return res
	}
	now := v.now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		res.Status, res.Error = StatusInvalid, "certificate expired or not yet valid"
		return res
	}
	if v.roots != nil {
		if _, err := cert.Verify(x509.VerifyOptions{Roots: v.roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			res.Status, res.Error = StatusInvalid, "certificate chain: "+err.Error()
			return res
		}
	}
	res.OrigTN = origTN(p.Orig)
	if res.Attest != "A" && res.Attest != "B" && res.Attest != "C" {
		res.Status, res.Error = StatusInvalid, "attest must be A, B or C"
		return res
	}
	iat := time.Unix(p.IAT, 0)
	if age := now.Sub(iat); age > v.cfg.MaxAge || age < -v.cfg.MaxAge {
		res.Status, res.Error = StatusStale, fmt.Sprintf("iat is %s old", age.Truncate(time.Second))
		return res
	}
	if !sameTN(res.OrigTN, caller) {
		res.Status, res.Error = StatusMismatch, "orig "+res.OrigTN+" is not the caller"
		return res
	}
	found := false
	for _, d := range p.Dest.TN {
		if sameTN(d, called) {
			found = true
		}
	}
	if !found {
		res.Status, res.Error = StatusMismatch, "called number is not in dest"
		return res
	}
	res.Status = StatusVerified
	return res
}

// origTN reads orig.tn, which the specification writes as a string and some
// implementations as a one element array.
func origTN(raw json.RawMessage) string {
	var s struct {
		TN string `json:"tn"`
	}
	if json.Unmarshal(raw, &s) == nil && s.TN != "" {
		return s.TN
	}
	var a tns
	if json.Unmarshal(raw, &a) == nil && len(a.TN) > 0 {
		return a.TN[0]
	}
	return ""
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sameTN(a, b string) bool {
	da, db := digits(a), digits(b)
	return da != "" && da == db
}

// certificate fetches (or serves from cache) the first certificate of the x5u.
func (v *Verifier) certificate(ctx context.Context, x5u string) (*x509.Certificate, error) {
	v.mu.Lock()
	if c, ok := v.cache[x5u]; ok && v.now().Before(c.expires) {
		v.mu.Unlock()
		return c.cert, c.err
	}
	v.mu.Unlock()
	cert, err := v.fetch(ctx, x5u)
	ttl := v.cfg.CacheTTL
	if err != nil {
		ttl = time.Minute // do not hammer a broken URL on every call
	}
	v.mu.Lock()
	v.cache[x5u] = cached{cert: cert, err: err, expires: v.now().Add(ttl)}
	if len(v.cache) > 10000 {
		v.cache = map[string]cached{}
	}
	v.mu.Unlock()
	return cert, err
}

func (v *Verifier) fetch(ctx context.Context, x5u string) (*x509.Certificate, error) {
	u, err := url.Parse(x5u)
	if err != nil || (u.Scheme != "https" && (!v.cfg.AllowHTTP || u.Scheme != "http")) {
		return nil, fmt.Errorf("x5u %q is not an https url", x5u)
	}
	ctx, cancel := context.WithTimeout(ctx, v.cfg.FetchTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, x5u, nil)
	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch x5u: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch x5u: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	var cert *x509.Certificate
	if block != nil {
		cert, err = x509.ParseCertificate(block.Bytes)
	} else {
		cert, err = x509.ParseCertificate(body) // DER
	}
	if err != nil {
		return nil, fmt.Errorf("parse x5u certificate: %w", err)
	}
	return cert, nil
}

// Sign builds an Identity header value (used by tests and by operators that
// hold their own STI certificate): a "shaken" PASSporT signed with ES256.
func Sign(key *ecdsa.PrivateKey, x5u, attest, orig, dest, origid string, iat time.Time) (string, error) {
	hdr, _ := json.Marshal(map[string]string{"alg": "ES256", "ppt": "shaken", "typ": "passport", "x5u": x5u})
	body, _ := json.Marshal(map[string]any{
		"attest": attest, "dest": map[string][]string{"tn": {dest}}, "iat": iat.Unix(), "orig": map[string]string{"tn": orig}, "origid": origid,
	})
	signing := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(cryptoRand, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ";info=<" + x5u + ">;alg=ES256;ppt=shaken", nil
}

var _ crypto.Hash = crypto.SHA256
