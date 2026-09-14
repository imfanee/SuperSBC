package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// TraceSink receives every log line that carries a call_uuid so the per-call
// trace page can show the Go side of a call next to the FreeSWITCH log.
type TraceSink func(callUUID, line string)

var (
	sinkMu sync.RWMutex
	sink   TraceSink
)

// SetTraceSink installs (or clears) the sink.
func SetTraceSink(s TraceSink) {
	sinkMu.Lock()
	sink = s
	sinkMu.Unlock()
}

// traceHandler wraps the JSON handler and tees call-scoped records.
type traceHandler struct {
	inner slog.Handler
	attrs []slog.Attr
}

func (h *traceHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	uuid := ""
	for _, a := range h.attrs {
		if a.Key == "call_uuid" {
			uuid = a.Value.String()
		}
	}
	fields := map[string]any{"time": r.Time.UTC().Format(time.RFC3339Nano), "level": r.Level.String(), "msg": r.Message}
	for _, a := range h.attrs {
		fields[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "call_uuid" {
			uuid = a.Value.String()
		}
		fields[a.Key] = a.Value.Any()
		return true
	})
	if uuid != "" {
		sinkMu.RLock()
		s := sink
		sinkMu.RUnlock()
		if s != nil {
			if b, err := json.Marshal(fields); err == nil {
				s(uuid, string(b))
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs), attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name), attrs: h.attrs}
}
