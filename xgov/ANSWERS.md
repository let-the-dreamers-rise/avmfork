# The two questions that sank earlier applications

Prepared answers for the forum thread and the xGov form. Every claim points at something a reader can check. Numbers must be refreshed from CI on the day of posting (see "Numbers to refresh").

## 1. "The Foundation could build this in-house" / "anyone could build it with AI now"

- It is built, public (AGPL-3.0, like go-algorand) and tested against MainNet in CI. xGov pays for work that exists and is useful; whether someone else could have built it is not the test.
- The Foundation's own focus is TypeScript and Python tooling (it handed algokit-core to the community, forum t/15373). We found no plan for a MainNet-forking tool.
- The hard part is being exactly right: fees, minimum balances, opcode costs and error messages must match a real node. avmfork doesn't reimplement them. It runs go-algorand v5.0.5's block evaluator and simulator as a library, the same release MainNet nodes run.
- That is checked, not assumed. `avmfork-difftest` takes the transaction groups from recent MainNet blocks, simulates each on a MainNet node and on a fresh fork, and compares the full responses including opcode-level traces. Then it sends each group to the fork as real transactions and compares what was committed with the MainNet node's result. Anyone can rerun it (`make difftest`).
- Getting this to zero differences found five bugs in avmfork; each fix names what the check showed (README, "Checking it against the real network").
- AI helped write the code, and the commits say so. The claim that avmfork behaves like MainNet rests on the comparison, not on trusting the code.

## 2. "The ecosystem already covers that"

| Existing tool | What it is | Difference |
|---|---|---|
| AlgoKit LocalNet | real algod in Docker, empty chain | no MainNet state, no impersonation or cheats |
| algorand-python-testing / algorand-typescript-testing | language-level emulators | don't run the compiled program or the ledger rules |
| algod simulate | real program on real state | one group at a time, nothing carried over, no cheats; avmfork serves simulate too, on top of your changes |
| Lora / AVM debugger | trace viewers | read-only |
| algo-property-test (Oct 2026) | property tests on the TypeScript emulator | complementary; avmfork could be a backend for it |
| dt-algorand (d13co, Oct 2026) | a fork of algod that answers state queries at past rounds (an archival node you sync) | complementary: it is a node with history, not a local fork you can change. It could serve as avmfork's upstream to read state exactly at the fork round, which is avmfork's main limit today |
| avm-rs, teal-interpreter, AlgoFuzz | reimplementations / research prototypes | no network fork |

Searches on 1 Nov 2026: GitHub repositories and code ("algorand fork mainnet", "algorand anvil", "impersonate", "algod fork", "cheatcodes", "simulate fork", `LedgerForEvaluator`, `eval.StartEvaluator`), forum.algorand.co, the web. We found no tool that forks an Algorand network locally. Other chains have one (Anvil and Hardhat for EVM, Forklift for Aptos).

## What avmfork does
1. `avmfork --fork mainnet` (or testnet, or any algod URL) starts a local node; accounts, apps, boxes and assets are read from upstream when first touched and kept from then on.
2. Impersonate any account; set balances, asset holdings, app state and boxes; move time; mine; snapshot and revert.
3. Serves the algod REST API: py-algorand-sdk and algosdk (JS) run unchanged against it in CI.
4. `avmfork --fork none` is an empty chain with no Docker.

## Open gaps, on the record
- State is read at the upstream node's latest round when first touched, not at the fork round. A long session can mix state from different times.
- No state proof or heartbeat transactions; no proposer payouts or absence suspensions.
- Account and box listing endpoints ask upstream every time (slow for very large accounts/apps).
- Cheats keep money totals and counters right but don't enforce other invariants (asset reserve, box min balance).

## Numbers to refresh before posting
From the latest scheduled CI run's summary lines: simulate matches (succeeded / with app calls / failed on both), commit-path matches, stale count, and the number of consecutive green scheduled runs. Never write "every day" until there are at least ~60 green scheduled runs.
