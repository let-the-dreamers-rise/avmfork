# Forum post (Developers category). Not the xGov proposal.

**Title:** avmfork: fork MainNet locally and test against real apps (open source)

**Body:**

I wanted to test contract changes against the real state of protocols already on MainNet, the way EVM developers use Anvil or Hardhat forks. LocalNet starts empty, the Python and TypeScript testing libraries don't run the compiled program, and simulate only runs one group with nothing carried over. So I built avmfork.

https://github.com/let-the-dreamers-rise/avmfork

```
avmfork --fork mainnet
```

starts a local node on port 4002 that looks like MainNet. Accounts, apps, boxes and assets are read from a MainNet node the first time you touch them; everything you do stays local. Point algosdk, AlgoKit utils or a generated app client at it as you would at algod.

Things LocalNet can't do:
- send from any address without its key (impersonate), e.g. a whale or an app's admin
- set a balance, an asset holding, global/local state or a box directly
- move the clock forward, mine empty blocks
- snapshot and revert the whole fork

It doesn't reimplement the AVM. It runs go-algorand v5.0.5's block evaluator as a library (the release MainNet runs), and algod's own REST handlers. To check it, a test takes the transaction groups from recent MainNet blocks, simulates each one on a MainNet node and on a fresh fork, and compares the full responses including opcode-level traces; then it sends the same group to the fork as real transactions and compares what was committed. In the latest CI run: [151 of 151] groups identical both ways, plus [105 of 105] app-call groups ([refresh these from the newest CI run]). Getting to zero differences found five bugs in avmfork, listed in the README. py-algorand-sdk and algosdk (JS) run unchanged against a fork in CI.

`avmfork --fork none` also gives you an empty chain without Docker.

What it doesn't do (yet): state at an old round (it reads the node's latest state when you first touch something), state proof and heartbeat transactions, consensus behaviour like proposer payouts. Related: d13co's dt-algorand serves state at past rounds; it could be a good upstream for avmfork.

AGPL-3.0, because it links go-algorand. I used AI to help write it; the MainNet comparison is how I check it.

If you have a contract on MainNet or TestNet, I'd like to know if it works on your setup and what's missing.
