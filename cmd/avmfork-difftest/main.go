// Command avmfork-difftest replays recent transaction groups from a real
// network through avmfork and through the network's own algod, and reports
// whether the simulate results are identical. See package difftest.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/difftest"
)

func main() {
	upstream := flag.String("upstream", "https://mainnet-api.algonode.cloud", "algod URL of the reference node")
	token := flag.String("token", "", "API token for the reference node")
	blocks := flag.Int("blocks", 20, "number of recent blocks to sample")
	perBlock := flag.Int("per-block", 5, "maximum groups to replay per block (0 = all)")
	appOnly := flag.Bool("app-calls-only", false, "only replay groups containing an app call")
	out := flag.String("out", "difftest-out", "directory for the report and any mismatches")
	flag.Parse()

	t, err := difftest.New(*upstream, *token)
	if err != nil {
		fatal(err)
	}
	latest, err := t.Remote.LatestRound()
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	start := time.Now()
	var cases []difftest.Case
	counts := map[difftest.Outcome]int{}
	for r := latest - basics.Round(*blocks) + 1; r <= latest; r++ {
		blk, err := t.Remote.Block(r)
		if err != nil {
			fatal(err)
		}
		groups, err := difftest.Groups(blk)
		if err != nil {
			fatal(err)
		}
		n := 0
		for gi, g := range groups {
			if *perBlock > 0 && n >= *perBlock {
				break
			}
			if *appOnly && !hasAppCall(g) {
				continue
			}
			prepared, err := difftest.Prepare(g, []byte(fmt.Sprintf("|avmfork-difftest %d.%d", r, gi)))
			if err != nil {
				counts[difftest.Skipped]++
				continue
			}
			n++
			c := t.Compare(prepared, r)
			counts[c.Outcome]++
			if c.Outcome == difftest.Mismatch {
				name := filepath.Join(*out, fmt.Sprintf("mismatch-%d-%d.json", r, gi))
				b, _ := json.MarshalIndent(c, "", "  ")
				os.WriteFile(name, b, 0o644)
			}
			c.Upstream, c.Fork = nil, nil
			cases = append(cases, c)
			fmt.Printf("round %d group %d (%s): %s %s\n", r, gi, strings.Join(c.Types, ","), c.Outcome, c.Detail)
		}
	}
	report := map[string]any{
		"upstream":     *upstream,
		"started":      start.UTC().Format(time.RFC3339),
		"duration-sec": int(time.Since(start).Seconds()),
		"blocks":       fmt.Sprintf("%d-%d", latest-basics.Round(*blocks)+1, latest),
		"counts":       counts,
		"cases":        cases,
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "report.json"), b, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("\nmatch %d, mismatch %d, stale %d, error %d, skipped %d\n",
		counts[difftest.Match], counts[difftest.Mismatch], counts[difftest.Stale], counts[difftest.Errored], counts[difftest.Skipped])
	if counts[difftest.Mismatch] > 0 || counts[difftest.Errored] > 0 {
		os.Exit(1)
	}
}

func hasAppCall(g []transactions.SignedTxn) bool {
	for _, s := range g {
		if s.Txn.Type == protocol.ApplicationCallTx {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "avmfork-difftest:", err)
	os.Exit(2)
}
