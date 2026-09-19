// Package sipcapture reads the SIP messages of a call from the HOMER
// capture database that heplify-server fills from FreeSWITCH's HEP stream
// (D-71). The customer leg is found by its Call-ID (stored on the CDR), the
// carrier legs by the X-SBC-Call header the SBC puts on every INVITE it
// sends; the responses of those legs share the carrier leg Call-IDs.
package sipcapture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Message is one captured SIP message.
type Message struct {
	ID        int64     `json:"id"`
	Time      time.Time `json:"time"`
	SrcIP     string    `json:"src_ip"`
	SrcPort   int       `json:"src_port"`
	DstIP     string    `json:"dst_ip"`
	DstPort   int       `json:"dst_port"`
	Transport string    `json:"transport"`
	CallID    string    `json:"call_id"`
	// Leg is "customer" or "carrier", by which side of the SBC the remote address is on.
	Leg string `json:"leg"`
	// Method is the request method or the response status code ("INVITE", "200").
	Method string `json:"method"`
	// Summary is the first line of the message.
	Summary string `json:"summary"`
	Raw     string `json:"raw"`
}

// Trace is the result for one call.
type Trace struct {
	CallUUID       string    `json:"call_uuid"`
	CustomerCallID string    `json:"customer_call_id"`
	CarrierCallIDs []string  `json:"carrier_call_ids"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Messages       []Message `json:"messages"`
}

// ErrNotConfigured is returned when no capture database is configured.
var ErrNotConfigured = errors.New("SIP capture store is not configured (SBC_HEP_DATABASE_URL)")

// Store reads the capture database. The pool is created lazily so a
// stopped HOMER does not stop the API from starting.
type Store struct {
	url  string
	mu   sync.Mutex
	pool *pgxpool.Pool
}

// New creates a store; an empty url disables it.
func New(url string) *Store { return &Store{url: url} }

// Enabled reports whether a capture database is configured.
func (s *Store) Enabled() bool { return s != nil && s.url != "" }

func (s *Store) db(ctx context.Context) (*pgxpool.Pool, error) {
	if !s.Enabled() {
		return nil, ErrNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		return s.pool, nil
	}
	cfg, err := pgxpool.ParseConfig(s.url)
	if err != nil {
		return nil, fmt.Errorf("capture database url: %w", err)
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s.pool = pool
	return pool, nil
}

// Query describes the call to look up.
type Query struct {
	CallUUID       string
	CustomerCallID string    // may be empty for CDRs written before D-71
	CustomerIP     string    // to label legs
	From, To       time.Time // capture window (call start and end with a margin)
}

// Trace returns every captured message of the call, oldest first.
func (s *Store) Trace(ctx context.Context, q Query) (*Trace, error) {
	pool, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := &Trace{CallUUID: q.CallUUID, CustomerCallID: q.CustomerCallID, From: q.From, To: q.To, CarrierCallIDs: []string{}, Messages: []Message{}}
	// Carrier legs: INVITEs the SBC sent carry X-SBC-Call.
	rows, err := pool.Query(ctx, `SELECT DISTINCT sid FROM hep_proto_1_call
		WHERE create_date >= $1 AND create_date < $2 AND data_header->>'method' = 'INVITE' AND raw LIKE $3`,
		q.From, q.To, "%X-SBC-Call: "+q.CallUUID+"%")
	if err != nil {
		return nil, fmt.Errorf("capture query: %w", err)
	}
	sids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	sort.Strings(sids)
	out.CarrierCallIDs = sids
	all := append([]string{}, sids...)
	if q.CustomerCallID != "" {
		all = append(all, q.CustomerCallID)
	}
	if len(all) == 0 {
		return out, nil
	}
	rows, err = pool.Query(ctx, `SELECT id, sid, create_date, protocol_header, raw FROM hep_proto_1_call
		WHERE create_date >= $1 AND create_date < $2 AND sid = ANY($3) ORDER BY create_date, id LIMIT 2000`, q.From, q.To, all)
	if err != nil {
		return nil, fmt.Errorf("capture query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		var hdr map[string]any
		if err := rows.Scan(&m.ID, &m.CallID, &m.Time, &hdr, &m.Raw); err != nil {
			return nil, err
		}
		m.SrcIP, _ = hdr["srcIp"].(string)
		m.DstIP, _ = hdr["dstIp"].(string)
		m.SrcPort = intOf(hdr["srcPort"])
		m.DstPort = intOf(hdr["dstPort"])
		switch intOf(hdr["protocol"]) {
		case 6:
			m.Transport = "tcp"
		case 17:
			m.Transport = "udp"
		default:
			m.Transport = "tls"
		}
		m.Summary, m.Method = firstLine(m.Raw)
		m.Leg = "carrier"
		if m.CallID == q.CustomerCallID || sameHost(m.SrcIP, q.CustomerIP) || sameHost(m.DstIP, q.CustomerIP) {
			m.Leg = "customer"
		}
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(b); err == nil {
		b = h
	}
	return a == b
}

// firstLine returns the request or status line and the method or status code.
func firstLine(raw string) (line, method string) {
	line = strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
	f := strings.Fields(line)
	if len(f) >= 2 && strings.HasPrefix(f[0], "SIP/2.0") {
		return line, f[1]
	}
	if len(f) >= 1 {
		return line, f[0]
	}
	return line, ""
}
