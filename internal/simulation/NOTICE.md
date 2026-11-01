This package is a copy of go-algorand's `ledger/simulation` package
(github.com/algorand/go-algorand, release v5.0.5-stable, commit
31186e99a69e74483ad95e9ce872903b74758abf, Copyright (C) 2019-2026 Algorand
Foundation Ltd., AGPL-3.0). `tools/sync-upstream.py` makes the copy.

avmfork's only change: `simulator.go` takes any ledger that the block evaluator
accepts (`Ledger` interface) instead of `*data.Ledger`, so the simulate endpoint
runs against the fork. Everything else is unchanged so results match algod.
