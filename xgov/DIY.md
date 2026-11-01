# Algorand xGov: what Ash does, in order

Product: avmfork, https://github.com/let-the-dreamers-rise/avmfork (CI green; MainNet differential check: every compared group identical, by simulate and by real submission, on 1 Nov 2026).

xGov is retroactive and the community votes after at least 3 weeks of forum discussion. Nothing here needs money until step 5.

## Now (free)
1. **Make the first release** (2 minutes, free): on https://github.com/let-the-dreamers-rise/avmfork click Releases → "Draft a new release" → "Choose a tag" → type `v0.1.0` → "Create new tag on publish" → title `v0.1.0` → Publish. A workflow then builds and attaches Linux and macOS binaries (about 10 minutes). Then click Watch on the repo so you see issues people open, and answer them briefly yourself.
2. **Forum "show and tell" post** (free, your account): paste `FORUM-POST.md` into a new topic at https://forum.algorand.co in the Developers category. This is not the proposal; it is to get real users. Reply to every question within a day. If you don't know the answer, say "let me check" and ask a Claude thread.
3. **One message to a likely first user**: SatyawanSingh (built algo-property-test, forum topic 15522). Text is in `OUTREACH.md`. Do not mass-message.
4. **Register with Electric Capital** (required by xGov): add the repo to Electric Capital's crypto-ecosystems list under Algorand. Steps in `OUTREACH.md`.

## Wait for these before submitting
- Code public about 2 months (the Council's "battle-testing" idea): earliest about **early January 2027**.
- At least one person outside you has used it (an issue, a star with a comment, a forum reply saying it worked).
- A new xGov Council is in place (the old term ended Sep 2026; proposers were told to wait).
- Another grant or bounty has paid, so the fees below are covered.

## Filling the [brackets] in FORM.md (no Claude needed)
Open https://github.com/let-the-dreamers-rise/avmfork/actions, click the newest green "ci" run, open the "difftest" job and copy its summary lines: groups matched, succeeded, with app calls, failed on both, commit path. Count the green scheduled runs in the same list. Stars, forks and issues are on the repo page. If any number shows a mismatch, do not submit.

## Step 5: submit (costs money)
- Go to https://xgov.algorand.co, connect a wallet, complete KYC.
- Fees: proposer fee 100 ALGO + open-proposal fee 100 ALGO (both non-refundable) + commitment of 3% of the ask (returned unless vetoed).
- Recommended ask: **10,000 ALGO** (smallest tier). Total cash needed: 100 + 100 + 300 = 500 ALGO (about $48 at $0.095). If you want the bare minimum: ask 5,000 ALGO, needs 350 ALGO.
- Paste the fields from `FORM.md`. Link the forum post from step 2.
- Then open the proposal discussion thread on the forum (text in `FORM.md`, "Forum thread" section) and keep it alive for 3 weeks: answer every question, add nothing promotional.
- After 3 weeks, move it to voting on the platform.

## Rules for every reply you write
- Short and plain. No marketing words. If something does not work yet, say so.
- If asked "did you use AI?": yes, to help write code; correctness is checked against real MainNet transactions in CI (say "every day" only once there are ~60 green scheduled runs).
- Never promise future work in the proposal; xGov pays only for what exists.
