// Package trace stitches the Go, Lua and FreeSWITCH log lines of one call
// (Section 7, per-call trace page) and manages the SIP trace toggle.
package trace

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/esl"
	"github.com/opensbc/opensbc/internal/logging"
)

const (
	keepLines = 500
	keepFor   = 24 * time.Hour
	maxScan   = 64 << 20 // bytes of FreeSWITCH log scanned from the end
)

// Store keeps Go log lines per call in Redis and reads the FreeSWITCH log.
type Store struct {
	rdb     *redis.Client
	fsLog   string
	esl     *esl.Supervisor
	mu      sync.Mutex
	offAt   time.Time
	scope   string
	offTime *time.Timer
}

// New creates the store and installs the log sink.
func New(rdb *redis.Client, fsLogPath string, sup *esl.Supervisor) *Store {
	s := &Store{rdb: rdb, fsLog: fsLogPath, esl: sup}
	logging.SetTraceSink(func(callUUID, line string) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		key := "trace:" + callUUID
		pipe := rdb.TxPipeline()
		pipe.RPush(ctx, key, line)
		pipe.LTrim(ctx, key, -keepLines, -1)
		pipe.Expire(ctx, key, keepFor)
		_, _ = pipe.Exec(ctx)
	})
	return s
}

// APILines returns the Go log lines recorded for the call.
func (s *Store) APILines(ctx context.Context, callUUID string) []string {
	lines, err := s.rdb.LRange(ctx, "trace:"+callUUID, 0, -1).Result()
	if err != nil {
		return nil
	}
	return lines
}

// FreeSWITCHLines greps the FreeSWITCH log (current file and the previous
// rotation) for lines containing the needle, most recent 64 MB only.
func (s *Store) FreeSWITCHLines(needle string, limit int) []string {
	var out []string
	for _, path := range []string{s.fsLog + ".1", s.fsLog} {
		out = append(out, grepFile(path, needle, limit-len(out))...)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func grepFile(path, needle string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > maxScan {
		_, _ = f.Seek(st.Size()-maxScan, io.SeekStart)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []string
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, needle) {
			out = append(out, line)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// SIPTraceState describes the current trace toggle.
type SIPTraceState struct {
	Enabled bool      `json:"enabled"`
	Scope   string    `json:"scope"`
	Until   time.Time `json:"until"`
}

// EnableSIPTrace turns on Sofia SIP tracing for the scope ("global",
// "external-ingress" or "external-egress") for the given duration.
func (s *Store) EnableSIPTrace(ctx context.Context, scope string, d time.Duration) error {
	if _, err := s.esl.API(ctx, sipTraceCmd(scope, "on")); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offTime != nil {
		s.offTime.Stop()
	}
	s.scope = scope
	s.offAt = time.Now().Add(d)
	s.offTime = time.AfterFunc(d, func() { _ = s.DisableSIPTrace(context.Background()) })
	return nil
}

// DisableSIPTrace turns tracing off.
func (s *Store) DisableSIPTrace(ctx context.Context) error {
	s.mu.Lock()
	scope := s.scope
	s.offAt = time.Time{}
	s.scope = ""
	if s.offTime != nil {
		s.offTime.Stop()
		s.offTime = nil
	}
	s.mu.Unlock()
	if scope == "" {
		scope = "global"
	}
	if s.esl == nil || !s.esl.Connected() {
		return nil
	}
	_, err := s.esl.API(ctx, sipTraceCmd(scope, "off"))
	return err
}

// State returns the toggle state.
func (s *Store) State() SIPTraceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SIPTraceState{Enabled: time.Now().Before(s.offAt), Scope: s.scope, Until: s.offAt}
}

func sipTraceCmd(scope, onOff string) string {
	if scope == "" || scope == "global" {
		return "sofia global siptrace " + onOff
	}
	return "sofia profile " + scope + " siptrace " + onOff
}

// SIPMessages returns the SIP messages from the FreeSWITCH log that involve
// the address (a customer IP or a carrier IP). FreeSWITCH writes each traced
// message as "send/recv N bytes to/from udp/[ip]:port at ...", a line of
// dashes, then the message; the block ends at the next header line.
func (s *Store) SIPMessages(ip string, limit int) []string {
	var blocks []string
	flush := func(cur []string) {
		for len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1]) == "" {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 1 {
			blocks = append(blocks, strings.Join(cur, "\n"))
		}
	}
	for _, path := range []string{s.fsLog + ".1", s.fsLog} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if st, err := f.Stat(); err == nil && st.Size() > maxScan {
			_, _ = f.Seek(st.Size()-maxScan, io.SeekStart)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var cur []string
		in := false
		for sc.Scan() {
			line := sc.Text()
			isHeader := (strings.HasPrefix(line, "send ") || strings.HasPrefix(line, "recv ")) && strings.Contains(line, " bytes ")
			// a timestamped log line also ends a block
			isLog := len(line) > 20 && line[4] == '-' && line[7] == '-' && line[10] == ' '
			if isHeader || isLog {
				if in {
					flush(cur)
				}
				cur = nil
				in = isHeader && strings.Contains(line, "["+ip+"]")
				if in {
					cur = []string{line}
				}
				continue
			}
			if in && !strings.HasPrefix(line, "-----") {
				cur = append(cur, line)
			}
		}
		if in {
			flush(cur)
		}
		_ = f.Close()
	}
	if len(blocks) > limit {
		blocks = blocks[len(blocks)-limit:]
	}
	return blocks
}
