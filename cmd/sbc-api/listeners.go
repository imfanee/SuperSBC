package main

import (
	"context"
	"time"

	"github.com/imfanee/supersbc/internal/cache"
)

// listenConfigChanges re-renders FreeSWITCH configuration and flushes the
// source-address cache when carriers or customer IPs change on any node.
func (a *app) listenConfigChanges(ctx context.Context) {
	sub := a.rdb.Subscribe(ctx, cache.ChanCarriersChanged, cache.ChanCustomerIPs, cache.ChanBansChanged)
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
			case cache.ChanBansChanged:
				if err := a.renderer.RenderACLs(ctx); err != nil {
					a.log.Error("render acl after ban change", "error", err)
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

// expireBans removes expired bans every minute and re-renders the ACL.
func (a *app) expireBans(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := a.st.ExpireBans(ctx)
			if err != nil {
				a.log.Warn("expire bans", "error", err)
				continue
			}
			if n > 0 {
				a.log.Info("bans expired", "count", n)
				if err := a.renderer.RenderACLs(ctx); err != nil {
					a.log.Error("render acl after ban expiry", "error", err)
				}
			}
		}
	}
}
