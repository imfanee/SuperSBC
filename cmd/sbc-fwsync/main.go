// sbc-fwsync keeps the host nftables sets (customers, carriers, banned) of
// the SuperSBC firewall equal to the export sbc-api writes on every change
// (D-69). It runs on the host as a systemd service because only the host
// may change nftables; the API container has no CAP_NET_ADMIN.
//
// It polls the export file (atomic renames by the API do not always raise
// inotify events across a bind mount), resolves carrier hostnames, applies
// the three sets in one nft transaction when anything changed, and writes a
// boot file that the ruleset includes so the sets are populated before the
// containers start.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/imfanee/supersbc/internal/firewall"
)

func main() {
	file := flag.String("file", "/root/supersbc/deploy/live/state/firewall.json", "export written by sbc-api")
	boot := flag.String("boot-file", "/root/supersbc/deploy/live/state/firewall-sets.nft", "nft script included by the ruleset at boot")
	table := flag.String("table", "inet sbc", "nftables table holding the sets")
	nftBin := flag.String("nft", "nft", "nft binary")
	interval := flag.Duration("interval", time.Second, "poll interval")
	reresolve := flag.Duration("resolve-every", 5*time.Minute, "re-resolve carrier hostnames this often")
	once := flag.Bool("once", false, "apply once and exit")
	dry := flag.Bool("dry-run", false, "print the nft script instead of applying it")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sbc-fwsync")

	var lastMod time.Time
	var lastScript string
	lastResolve := time.Time{}
	apply := func(force bool) {
		st, err := os.Stat(*file)
		if err != nil {
			if force {
				log.Warn("export missing", "file", *file, "error", err)
			}
			return
		}
		now := time.Now()
		if !force && st.ModTime().Equal(lastMod) && now.Sub(lastResolve) < *reresolve {
			return
		}
		b, err := os.ReadFile(*file)
		if err != nil {
			log.Error("read export", "error", err)
			return
		}
		ex, err := firewall.Parse(b)
		if err != nil {
			log.Error("parse export", "error", err)
			return
		}
		script, problems := firewall.Script(ex, *table, net.LookupHost)
		for _, p := range problems {
			log.Warn("firewall export problem", "problem", p)
		}
		lastMod, lastResolve = st.ModTime(), now
		if script == lastScript && !force {
			return
		}
		if *dry {
			fmt.Print(script)
		} else {
			if err := runNFT(*nftBin, script); err != nil {
				log.Error("nft apply failed", "error", err)
				return
			}
			if err := writeAtomic(*boot, script); err != nil {
				log.Warn("boot file not written", "file", *boot, "error", err)
			}
		}
		lastScript = script
		log.Info("firewall sets applied", "customers", len(ex.Customers), "carriers", len(ex.Carriers), "banned", len(ex.Banned), "export_generated_at", ex.GeneratedAt)
	}
	apply(true)
	if *once {
		return
	}
	t := time.NewTicker(*interval)
	defer t.Stop()
	for range t.C {
		apply(false)
	}
}

// runNFT feeds the script to "nft -f -" so the sets change in one transaction.
func runNFT(bin, script string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

func writeAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
