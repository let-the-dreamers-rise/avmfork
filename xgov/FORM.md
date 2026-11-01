# xGov proposal: paste-ready fields (fill the [brackets] from CI on the day you submit)

Do not submit before the conditions in DIY.md are met.

**Title**
avmfork: fork Algorand MainNet locally and test against real state

**Focus / category**
Libraries / Tooling

**Funding type**
Retroactive

**Amount requested**
10,000 ALGO

**Open source / license**
Yes. AGPL-3.0 (required, because avmfork links go-algorand, which is AGPL-3.0). https://github.com/let-the-dreamers-rise/avmfork

**Description**

avmfork is a local Algorand node that forks MainNet, TestNet or any network you have an algod for. You run `avmfork --fork mainnet` and get an algod API on localhost. Accounts, apps, boxes and assets are read from the real network the first time you touch them; everything you do stays local.

On top of that it has what EVM developers get from Anvil or Hardhat forks: send transactions from any address without its key, set balances, asset holdings, app state and boxes directly, move the clock, mine blocks, snapshot and revert the whole fork. `--fork none` gives an empty chain without Docker.

It does not reimplement the AVM. It runs go-algorand v5.0.5's block evaluator and simulator as a library (the release MainNet runs) and algod's own REST handlers, so the unmodified Python and JavaScript SDKs work against it. Both are tested in CI.

How we know it behaves like MainNet: `avmfork-difftest` takes every transaction group from recent MainNet blocks, simulates each on a MainNet node and on a fresh fork, and compares the full responses, including opcode-level traces. It then sends each group to the fork as real transactions and compares what was committed with the MainNet node's result. Latest results: [N] groups compared by simulate, 0 differences ([S] succeeded, [A] with app calls, [F] failed on both sides); commit path [C] of [N] identical. It has run on a schedule since [date]: [K] runs, all green. Getting to zero differences found five bugs in avmfork, each listed in the README with its fix.

What exists today: the fork node, the control endpoints, the empty-chain mode, the differential tester, SDK checks for Python and JavaScript, release binaries for Linux and macOS. What it does not do, on the record: read state at an old round (it reads upstream's latest state when first touched), state proof or heartbeat transactions, proposer payouts.

**Why this is not covered already**
LocalNet starts empty. algorand-python-testing and algorand-typescript-testing don't run the compiled program. Simulate runs one group with nothing carried over and no cheats. dt-algorand (d13co) is an algod that answers state at past rounds; it is a node you sync, not a local fork you can change, and the two fit together (it could be avmfork's upstream). We searched GitHub, the forum and the web on 1 Nov 2026 and found no tool that forks an Algorand network locally.

**Adoption metrics**
[stars, forks, issues opened by others, named users with links, release downloads. Fill from GitHub on the day. If there are no outside users yet, do not submit.]

**About the team**
Ashwin Goyal (GitHub let-the-dreamers-rise), independent developer. https://ashwin-goyal-portfolio.vercel.app/
Merged upstream work: x402-foundation/x402#3674 (Cardano payments: submit the client's signed transaction bytes unchanged; fixed a CBOR re-serialization that made nodes reject valid payments), bitcoin/bips#2273, astrayama/yggdrasil#22.
Earlier grants: 0G Buildathon (USD 67), Genkai Agentic Superengineering (USD 200). No prior xGov or Algorand Foundation funding.
AI assistance: I used Claude to help write the code; commits are marked. The correctness claim rests on the MainNet comparison in CI, which anyone can rerun with `make difftest`.

**Additional info**
- Repository: https://github.com/let-the-dreamers-rise/avmfork
- CI (MainNet end-to-end tests, SDK checks, differential check): https://github.com/let-the-dreamers-rise/avmfork/actions
- Forum discussion: [link to the proposal thread]
- Why 10,000 ALGO: it pays for a working, verified tool at the smallest tier. I am not claiming hours.

## Forum thread for the proposal (post when submitting)

**Title:** xGov proposal: avmfork, a local MainNet fork for Algorand (#[proposal id])

**Body:** paste the Description, "Why this is not covered already" and the Amount sections above, then:

Happy to answer questions. If you have a contract on MainNet or TestNet and it doesn't work on avmfork, please open an issue; that is the most useful feedback.
