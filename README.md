# avmfork

A local Algorand node that forks MainNet (or TestNet, or any network you have an algod for) and lets you test against real state.

```
$ avmfork --fork mainnet
avmfork: forked mainnet-v1.0 (https://mainnet-api.algonode.cloud) at round 66530533
avmfork: algod API on http://127.0.0.1:4002
```

`avmfork --fork none` starts an empty chain instead: no Docker, no upstream, a new block per transaction group. Fund accounts with `/avmfork/set-balance`.

Point algosdk, AlgoKit utils, a generated app client or Lora at `http://127.0.0.1:4002`. Accounts, apps, boxes and assets are read from the upstream node the first time they are touched (at the node's latest round, see Limits) and kept from then on. Every change you make stays local.

## Why

LocalNet starts empty, so a test that touches a real protocol (a DEX pool, a lending market, the xGov registry) has to redeploy and reseed it, and the result is only as good as the reseeding. The language-level emulators (`algorand-python-testing`, `algorand-typescript-testing`) do not run the compiled program at all. Simulate on a public node runs the real program on real state, but only one group at a time and with nothing carried over.

avmfork runs the real evaluator on real state and keeps the result, block after block.

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

algod only serves its latest state and MainNet moves every ~2.8 seconds. The tester records every remote read and checks it against the node's state deltas. If the two responses differ and something avmfork read changed in between, the case is reported as `stale` and not counted.

algod v5.0.x lists some arrays in a simulate response (`initial-states` and the state deltas) in Go map order, which is random, so those arrays are sorted on both sides before comparing.

avmfork is pinned to the go-algorand release MainNet runs (see `GO_ALGORAND_COMMIT` in the Makefile). `tools/sync-upstream.py` re-copies the simulator and the simulate encoder when the pin moves.

CI runs it on every push and once a day. Reports are uploaded as build artifacts.

Results so far (30 MainNet blocks per run, every group in each block):

| Date | avmfork commit | Identical | Different | Stale (not counted) |
|---|---|---|---|---|
| 2026-11-01 | `f434470` | 221 | 0 | 8 |

Getting there found four bugs, each fixed with a commit that says what the check showed: avmfork was built on go-algorand's main branch, whose simulate reports opcode budget differently from the v5.0.5 release MainNet runs (`986759b`); accounts with no balance came back from algod with a reward base the ledger does not have (`25f126e`); the balance cheat then paid such accounts every reward since genesis (`2591e48`); and leases held on MainNet from before the fork were unknown (`f434470`).

## Building

avmfork links go-algorand, which needs go-algorand's own libsodium build:

```
make deps     # clones go-algorand at the pinned commit and builds libsodium
make build    # bin/avmfork and bin/avmfork-difftest
make test     # offline tests on an empty chain
make e2e      # end-to-end tests against a MainNet fork (needs network)
```

You need Go (see `go.mod`), git, make, autoconf, automake and libtool.

## Limits

- State is read at the upstream node's latest round, not at the fork round. Reads made a while after forking see newer state. Pin with `--fork-round` only for recent rounds.
- Transaction ids and leases from the 1000 rounds before the fork are fetched in the background at startup (a transaction with a lease waits for them). Until that finishes, a plain transaction that duplicates one confirmed on the network just before the fork is not caught.
- State proof and heartbeat transactions are not supported. Nobody is suspended for absence, and proposer payouts are zero.
- Simulate runs only on top of the latest fork round.
- The upstream node must serve `/v2/deltas` for forking (Nodely/AlgoNode public nodes do).
- `GET /v2/accounts/{addr}` asks the upstream node for the full account each time, and box listings page through all of an app's boxes upstream. Slow for accounts with many assets or apps with many boxes.
- Cheats change what you tell them to and keep the money totals and the account's counters right, but nothing else: `set-asset-holding` does not take the amount from the creator's reserve, and `set-box` does not check the app account's minimum balance.
- The control endpoints (`/avmfork/*`) are open to anything that can reach the port. They send no CORS headers, so web pages can't call them, and the default listen address is 127.0.0.1. Use `--token` if you expose the port.

## License

AGPL-3.0, because avmfork links go-algorand, which is AGPL-3.0.
