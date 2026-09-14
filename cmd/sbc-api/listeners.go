package main

import (
	"context"

	"github.com/opensbc/opensbc/internal/cache"
)

// listenConfigChanges re-renders FreeSWITCH configuration and flushes the
// source-address cache when carriers or customer IPs change on any node.
func (a *app) listenConfigChanges(ctx context.Context) {
	sub := a.rdb.Subscribe(ctx, cache.ChanCarriersChanged, cache.ChanCustomerIPs)
	defer func() { _ = sub.Close() }()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			switch m.Channel {
			case cache.ChanCarriersChanged:
				if err := a.renderer.RenderAll(ctx); err != nil {
					a.log.Error("render after carrier change", "error", err)
				}
			case cache.ChanCustomerIPs:
				if err := a.pipe.IPCache().Flush(ctx); err != nil {
					a.log.Warn("flush ip cache", "error", err)
				}
				if err := a.renderer.RenderACLs(ctx); err != nil {
					a.log.Error("render acl after ip change", "error", err)
				}
			}
		}
	}
}
