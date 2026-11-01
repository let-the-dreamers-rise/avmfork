#!/usr/bin/env python3
"""Copy go-algorand's simulator and REST simulate helpers into avmfork.

avmfork keeps two pieces of go-algorand as copies:

  internal/simulation/  from ledger/simulation/ (the simulator takes
                        *data.Ledger upstream; the copy takes any ledger)
  server/algodutils.go  from daemon/algod/api/server/v2/utils.go (so that
                        simulate responses are encoded exactly like algod's)

Run this after changing the pinned go-algorand version in the Makefile:

  tools/sync-upstream.py third_party/go-algorand

It copies the files and applies the same small, listed edits each time, and
fails loudly if an edit no longer applies.
"""
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SIM_FILES = ["initialStates.go", "resources.go", "simulator.go", "trace.go", "tracer.go"]


def edit(text, old, new, path, count=1):
    n = text.count(old)
    if n != count:
        sys.exit(f"{path}: expected {count} of {old!r}, found {n}")
    return text.replace(old, new)


def sub(text, pattern, repl, path):
    out, n = re.subn(pattern, repl, text, flags=re.S)
    if n != 1:
        sys.exit(f"{path}: pattern {pattern!r} matched {n} times")
    return out


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    ga = sys.argv[1]
    commit = subprocess.check_output(["git", "-C", ga, "rev-parse", "HEAD"], text=True).strip()

    for name in SIM_FILES:
        src = open(os.path.join(ga, "ledger/simulation", name)).read()
        if name == "simulator.go":
            p = "simulator.go"
            src = edit(src, '\t"github.com/algorand/go-algorand/data"\n', "", p)
            src = sub(src,
                      r"// simulatorLedger patches the ledger interface.*?\n// not a data\.Ledger\. This ensures our overridden LookupLatest method will be used\.\n",
                      """// Ledger is what the simulator needs from a ledger. avmfork change: the
// upstream simulator wraps *data.Ledger; this copy takes any ledger that the
// block evaluator accepts and that can report its latest round.
type Ledger interface {
	eval.LedgerForEvaluator
	Latest() basics.Round
}

// simulatorLedger pins the round the simulation runs on top of.
type simulatorLedger struct {
	Ledger
	start basics.Round
}

// LookupAccount returns account data at rnd.
func (l simulatorLedger) LookupAccount(rnd basics.Round, addr basics.Address) (ledgercore.AccountData, basics.Round, basics.MicroAlgos, error) {
	ad, r, err := l.Ledger.LookupWithoutRewards(rnd, addr)
	return ad, r, ad.MicroAlgos, err
}

// StartEvaluator starts a block evaluator on the wrapped ledger.
""", p)
            src = edit(src, "return eval.StartEvaluator(&l, hdr,", "return eval.StartEvaluator(l.Ledger, hdr,", p)
            src = edit(src, "func MakeSimulator(ledger *data.Ledger, developerAPI bool) *Simulator {",
                       "func MakeSimulator(ledger Ledger, developerAPI bool) *Simulator {", p)
        open(os.path.join(HERE, "internal/simulation", name), "w").write(src)

    p = "utils.go"
    src = open(os.path.join(ga, "daemon/algod/api/server/v2/utils.go")).read()
    src = edit(src, "package v2\n", f"""// avmfork: copied from go-algorand daemon/algod/api/server/v2/utils.go at
// commit {commit} by tools/sync-upstream.py, so that simulate results
// from avmfork's copy of the simulator are encoded exactly like algod's.
// Only the package name, the simulation import and type qualifiers changed.

package server
""", p)
    src = edit(src, '\t"github.com/algorand/go-algorand/ledger/simulation"\n',
               '\tv2 "github.com/algorand/go-algorand/daemon/algod/api/server/v2"\n'
               '\tupstreamsim "github.com/algorand/go-algorand/ledger/simulation"\n'
               '\t"github.com/let-the-dreamers-rise/avmfork/internal/simulation"\n', p)
    for t in ["LedgerForAPI", "PreEncodedTxInfo", "PreEncodedSimulateTxnResult",
              "PreEncodedSimulateTxnGroupResult", "PreEncodedSimulateResponse", "PreEncodedSimulateRequest"]:
        src = re.sub(r"(?<![\w.])" + t + r"\b", "v2." + t, src)
    src = edit(src, "ExecTraceConfig: result.TraceConfig,",
               "ExecTraceConfig: upstreamsim.ExecTraceConfig{Enable: result.TraceConfig.Enable, Stack: result.TraceConfig.Stack, Scratch: result.TraceConfig.Scratch, State: result.TraceConfig.State},", p)
    src = edit(src, "TraceConfig:           request.ExecTraceConfig,",
               "TraceConfig:           simulation.ExecTraceConfig{Enable: request.ExecTraceConfig.Enable, Stack: request.ExecTraceConfig.Stack, Scratch: request.ExecTraceConfig.Scratch, State: request.ExecTraceConfig.State},", p)
    open(os.path.join(HERE, "server/algodutils.go"), "w").write(src)

    tag = subprocess.run(["git", "-C", ga, "describe", "--tags", "--exact-match"],
                         capture_output=True, text=True).stdout.strip()
    release = f"release {tag}, commit\n{commit}" if tag else f"commit {commit}"
    open(os.path.join(HERE, "internal/simulation/NOTICE.md"), "w").write(f"""This package is a copy of go-algorand's `ledger/simulation` package
(github.com/algorand/go-algorand, {release}, Copyright (C) 2019-2026 Algorand
Foundation Ltd., AGPL-3.0). `tools/sync-upstream.py` makes the copy.

avmfork's only change: `simulator.go` takes any ledger that the block evaluator
accepts (`Ledger` interface) instead of `*data.Ledger`, so the simulate endpoint
runs against the fork. Everything else is unchanged so results match algod.
""")
    subprocess.check_call(["gofmt", "-w", os.path.join(HERE, "server/algodutils.go"), os.path.join(HERE, "internal/simulation")])
    print("synced from", commit)


if __name__ == "__main__":
    main()
