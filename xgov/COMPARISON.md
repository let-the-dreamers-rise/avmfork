# avmfork vs. past xGov proposals (read in full, 1 Nov 2026)

Threads saved in research/forum/. What each had when it asked, what voters pushed on, and where avmfork stands.

| | C# SDK (scholtz) | AlgoKit-Core mobile | Biatec Scan | Algonaut Rust SDK | avmfork (now) |
|---|---|---|---|---|---|
| Ask | 200k cut to 120k ALGO | 88,888 ALGO | 190k ALGO | 128k ALGO | not set (min 5k) |
| Age of work at ask | 19 months of maintenance | 2 months, repo public since Jan | 12 months, ~750 commits | 6 years, 11 releases | days |
| Outside users | 19,740 NuGet downloads | a live mobile wallet, Gem Wallet interested | 28M trades indexed, feeds Biatec DEX | 17,651 crates downloads, 70 stars, 37 forks, 14 contributors | none yet |
| Proof of correctness | releases track algod API | wallet in production | live site | official sdk-testing Gherkin suite in CI + compatibility matrix | MainNet e2e tests, Python and JS SDK checks, and a differential check against a MainNet node (simulate and commit path) in CI; daily schedule just started |
| Duplication question | none | overlap between two proposals by same team | "another explorer", look-alike of Lora/Vestige | "two Rust SDKs?" | no fork tool found; dt-algorand (historical-state algod) is related and complementary, must be named |
| AI suspicion | none | none | site "appears AI-assisted", ask number from AI | "zombie-prompting" warning | high risk: new repo, new account activity |
| Result | passed after lowering ask | in discussion (council term ended) | heavy pushback on uniqueness and ask | strong yes from voters, waits for council | not submitted |

## What won votes
1. A test the voter can run that proves the thing is correct against something trusted (Algonaut: the Foundation's own SDK test suite). emg110 turned from doubt to "big yes" on that alone.
2. Users the proposer does not control (downloads, a wallet in production, a DEX feed).
3. Honest, named gaps ("Open gaps, on the record").
4. Asking for the unique part only, not the bundle (Atsoc1993 to Biatec).
5. Price per hour that looks fair; hours by themselves don't count for much.

## What lost votes or slowed things
- Weak uniqueness, look-alikes of existing tools.
- AI-written text or AI-picked ask amount.
- Promises about future work (retro only; move roadmap to "Additional info").
- No usage numbers.
- Council politics: term ended Sep 2026, proposers told to hold until a new council (meeting notes 2 Sep 2026). Council "battle-testing" idea: code public at least ~2 months before asking.

## Where avmfork beats them
- Uniqueness: the strongest of the five. Nothing on Algorand forks MainNet locally; EVM has Anvil/Hardhat, Aptos has Forklift.
- Correctness proof can be stronger than Algonaut's: it uses go-algorand's own evaluator and checks itself against a MainNet node, opcode by opcode, on real recent groups, through both simulate and real commits. It only becomes a track record after weeks of scheduled green runs.
- Can't be "done in-house by AI": the Foundation would have to build and maintain the same thing; it is already built, tested against MainNet, and open.

## Where avmfork loses today (fix before submitting)
- Zero outside users. Fix: get 2 to 3 named teams to try it on their contracts (algo-property-test author SatyawanSingh is a natural first: his tool runs on the TS emulator; avmfork gives it real bytecode on real state). Post a forum "show and tell" from Ash's account.
- Age. Fix: submit no earlier than ~2 months after first public commit (~early Jan 2027), which also matches waiting for a payout and a new council.
- Ask size. Fix: ask small for the first round (the cheapest believable figure for real hours), not a big number.
- AI risk. Fix: hand-written README and posts in Ash's own voice, show the difftest report and e2e logs, disclose AI help the way x402#3674 did.

## After the independent review (grants/reviews/algorand.md)
The reviewer would vote no today and accept 5,000-10,000 ALGO "built and verified" now, more after: ~60 days of green scheduled checks, 1,000+ successful app-call matches, a commit-path check (added), fixes for the code issues (done), 2-3 named outside users, a tagged release with binaries, and dt-algorand in the table (added).
