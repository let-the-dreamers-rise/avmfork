# avmfork

A local Algorand node that forks MainNet (or TestNet, or any network you have an algod for) and lets you test against real state.

```
$ avmfork --fork mainnet
avmfork: forked mainnet-v1.0 (https://mainnet-api.algonode.cloud) at round 66530533
avmfork: algod API on http://127.0.0.1:4002
```

Point algosdk, AlgoKit utils, a generated app client or Lora at `http://127.0.0.1:4002` and you are talking to a copy of MainNet. Accounts, apps, boxes and assets are loaded from the upstream node the first time they are read. Every change you make stays local.

## Why

LocalNet starts empty, so a test that touches a real protocol (a DEX pool, a lending market, the xGov registry) has to redeploy and reseed it, and the result is only as good as the reseeding. The language-level emulators (`algorand-python-testing`, `algorand-typescript-testing`) do not run the compiled program at all. Simulate on a public node runs the real program on real state, but only one group at a time and with nothing carried over.

avmfork is the missing piece: real state, the real evaluator, and as many blocks as you want on top.

## How it works

avmfork does not reimplement the AVM. It imports go-algorand and runs its block evaluator (`ledger/eval`) on a ledger that reads missing state from algod's REST API and keeps every write in memory. Transactions are signature-checked with go-algorand's `verify` package. The HTTP API is go-algorand's own v2 handler set, so responses have exactly algod's shape. Simulate runs a copy of go-algorand's simulator, with one change: it takes avmfork's ledger instead of algod's (see `internal/simulation/NOTICE.md`).

Each submitted group becomes its own block immediately. There is no pool and no consensus.

Block timestamps follow the wall clock plus any warp. Consensus allows a block to be at most 25 seconds newer than its parent, so when the clock has moved further than that (the fork sat idle, or you warped), avmfork first commits one empty block at the new time and then the block you asked for. That empty block is the only one evaluated without go-algorand's header checks.

## Cheats

The `/avmfork/*` endpoints change the fork directly. Bodies are JSON; byte strings are base64.

| Endpoint | Body | Effect |
|---|---|---|
| `POST /avmfork/impersonate` | `{"address", "enabled"}` | unsigned transactions from `address` are accepted |
| `POST /avmfork/set-balance` | `{"address", "microalgos"}` | sets the balance (money totals stay consistent) |
| `POST /avmfork/set-asset-holding` | `{"address", "asset", "amount", "frozen"}` | opts in if needed and sets the holding |
| `POST /avmfork/set-state` | `{"app", "address"?, "key", "uint"\|"bytes"\|"delete"}` | writes global state, or local state if `address` is set; must fit the schema |
| `POST /avmfork/set-box` | `{"app", "name", "value"}` | writes a box (`value: null` deletes it) |
| `POST /avmfork/warp` | `{"seconds"}` or `{"timestamp"}` | moves the clock for the next blocks |
| `POST /avmfork/mine` | `{"blocks"}` | commits empty blocks |
| `POST /avmfork/snapshot`, `/avmfork/revert` | `{}`, `{"id"}` | saves and restores the whole fork |
| `GET /avmfork/info` | | fork round, latest round, network |

algod's dev-mode endpoint `POST /v2/devmode/blocks/offset/{seconds}` also works, so existing LocalNet code that moves time keeps working.

## Checking it against the real network

`avmfork-difftest` takes transaction groups from recent MainNet blocks, gives each a new note so it is a fresh transaction, drops the signatures, and simulates it with full execution traces on a MainNet node and on a new fork. The two responses must be identical, opcode by opcode.

algod only serves its latest state and MainNet moves every ~2.8 seconds. The tester records every remote read and checks it against the node's state deltas. If something it read changed in between, the case is reported as `stale` and not counted.

CI runs it on every push and once a day. Reports are uploaded as build artifacts.

## Building

avmfork links go-algorand, which needs go-algorand's own libsodium build:

```
make deps     # clones go-algorand at the pinned commit and builds libsodium
make build    # bin/avmfork and bin/avmfork-difftest
make test
make e2e      # end-to-end tests against a MainNet fork (needs network)
```

You need Go (see `go.mod`), git, make, autoconf, automake and libtool.

## Limits

- State is read at the upstream node's latest round, not at the fork round. Reads made a while after forking see newer state. Pin with `--fork-round` only for recent rounds.
- Transactions confirmed on the real network before the fork are not known to the duplicate check.
- State proof and heartbeat transactions are not supported. Nobody is suspended for absence, and proposer payouts are zero.
- Simulate runs only on top of the latest fork round.
- The upstream node must serve `/v2/deltas` for forking (Nodely/AlgoNode public nodes do).

## License

AGPL-3.0, because avmfork links go-algorand, which is AGPL-3.0.
