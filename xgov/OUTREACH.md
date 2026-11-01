# Outreach texts (Ash sends; nothing here costs money)

## 1. Message to SatyawanSingh (forum DM, or a reply in topic 15522)

Hi, I read your algo-property-test thread. Your tool runs on algorand-typescript-testing, so it checks the contract's logic but not the compiled program on real state.

I built avmfork: it forks MainNet locally using go-algorand's own evaluator and speaks the algod API, so algosdk and AlgoKit clients work against it unchanged. You can impersonate accounts, set balances and app state, snapshot and revert. https://github.com/let-the-dreamers-rise/avmfork

Would it be useful as a second backend for your property tests, running the same call sequences against the compiled TEAL on a fork of a real deployment? If you try it and something is missing or broken, an issue on the repo would help a lot.

## 2. Electric Capital registration (required by xGov)

GitHub, from your browser (needs a fork of their repo, so Ash does this):
1. Go to https://github.com/electric-capital/crypto-ecosystems and click Fork.
2. In your fork, create a file named `migrations/2026-11-01T120000_add_avmfork` (use the current date and time when you do it) with this one line:

```
repadd Algorand https://github.com/let-the-dreamers-rise/avmfork #developer-tool
```

3. Commit, then open a pull request to electric-capital/crypto-ecosystems with the title "Add avmfork to Algorand".
4. Tell a Claude thread the PR link so it can check the CI on that PR.
