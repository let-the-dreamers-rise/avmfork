// Run algosdk (JavaScript, v3) against a running avmfork MainNet fork.
// Usage: node check.mjs http://127.0.0.1:4002 https://mainnet-api.algonode.cloud
import algosdk from 'algosdk';

const [forkUrl, upstreamUrl] = process.argv.slice(2);
const fork = new algosdk.Algodv2('', forkUrl, '');
const up = new algosdk.Algodv2('', upstreamUrl, '');
const USDC = 31566704n;
let checks = 0;

function ok(cond, what) {
  if (!cond) {
    console.error(`FAIL: ${what}`);
    process.exit(1);
  }
  checks++;
  console.log('ok:', what);
}

const v = await fork.versionsCheck().do();
ok(v.genesisId === 'mainnet-v1.0', 'versions reports mainnet-v1.0');

const fa = await fork.getAssetByID(USDC).do();
const ua = await up.getAssetByID(USDC).do();
const js = (x) => JSON.stringify(x, (_, v) => (typeof v === 'bigint' ? v.toString() : v));
ok(js(fa) === js(ua), 'getAssetByID(USDC) equals MainNet');

const alice = algosdk.generateAccount();
const bob = algosdk.generateAccount();
const r = await fetch(`${forkUrl}/avmfork/set-balance`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ address: alice.addr.toString(), microalgos: 5000000 }),
});
ok(r.ok, 'set-balance');

const params = await fork.getTransactionParams().do();
const txn = algosdk.makePaymentTxnWithSuggestedParamsFromObject({
  sender: alice.addr,
  receiver: bob.addr,
  amount: 1000000,
  suggestedParams: params,
});
const { txid } = await fork.sendRawTransaction(txn.signTxn(alice.sk)).do();
const res = await algosdk.waitForConfirmation(fork, txid, 4);
ok(res.confirmedRound > 0n, 'payment confirmed (waitForConfirmation)');
const info = await fork.accountInformation(bob.addr).do();
ok(info.amount === 1000000n, 'receiver balance');

console.log(`${checks} checks passed`);
