// Package logging configures structured JSON logging (D-13). Every log line
// emitted while handling a call carries the call_uuid attribute so Lua, Go and
// FreeSWITCH lines can be stitched together with one grep.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New returns a JSON logger writing to stdout at the given level.
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

// WithCallUUID returns a context carrying a logger bound to the call uuid.
func WithCallUUID(ctx context.Context, base *slog.Logger, callUUID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, base.With("call_uuid", callUUID))
}

// FromContext returns the logger stored in ctx, or the fallback.
func FromContext(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return fallback
}
