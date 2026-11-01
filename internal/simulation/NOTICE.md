This package is a copy of go-algorand's `ledger/simulation` package
(github.com/algorand/go-algorand, commit 66fdf4ad60479aa41e66cc39b65e7ed118834616,
Copyright (C) 2019-2026 Algorand Foundation Ltd., AGPL-3.0).

avmfork's only change: `simulator.go` takes any ledger that the block evaluator
accepts (`Ledger` interface) instead of `*data.Ledger`, so the simulate endpoint
runs against the fork. Everything else is unchanged so results match algod.
