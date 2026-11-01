"""Run py-algorand-sdk against a running avmfork MainNet fork and check the
answers against a MainNet node. Usage:

    python check.py http://127.0.0.1:4002 https://mainnet-api.algonode.cloud
"""
import base64
import json
import sys
import urllib.request

from algosdk import account, encoding, transaction
from algosdk.atomic_transaction_composer import (
    AccountTransactionSigner,
    AtomicTransactionComposer,
    TransactionWithSigner,
)
from algosdk.v2client.algod import AlgodClient
from algosdk.v2client.models import SimulateRequest, SimulateTraceConfig

USDC = 31566704
USDC_RESERVE = "2UEQTE5QDNXPI7M3TU44G6SYKLFWLPQO7EBZM7K7MHMQQMFI4QJPLHQFHM"
BOX_APP = 3147789458

fork_url, upstream_url = sys.argv[1], sys.argv[2]
fork = AlgodClient("", fork_url)
up = AlgodClient("", upstream_url)
checks = 0


def ok(cond, what):
    global checks
    if not cond:
        sys.exit(f"FAIL: {what}")
    checks += 1
    print("ok:", what)


def cheat(path, body):
    req = urllib.request.Request(fork_url + "/avmfork/" + path, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as r:
        return json.load(r)


ok(fork.versions()["genesis_id"] == "mainnet-v1.0", "versions reports mainnet-v1.0")
sp = fork.suggested_params()
ok(sp.gen == "mainnet-v1.0" and sp.min_fee >= 1000, "suggested params")

ok(fork.asset_info(USDC) == up.asset_info(USDC), "asset_info(USDC) equals MainNet")

fa, ua = fork.application_info(BOX_APP), up.application_info(BOX_APP)
ok(fa["params"]["approval-program"] == ua["params"]["approval-program"], "application_info program equals MainNet")
ok(fa["params"]["creator"] == ua["params"]["creator"], "application_info creator equals MainNet")

fb = fork.application_boxes(BOX_APP)["boxes"]
ub = up.application_boxes(BOX_APP)["boxes"]
ok(sorted(b["name"] for b in fb) == sorted(b["name"] for b in ub), f"application_boxes lists the same {len(ub)} boxes")
name = base64.b64decode(ub[0]["name"])
ok(fork.application_box_by_name(BOX_APP, name) == up.application_box_by_name(BOX_APP, name), "box value equals MainNet")

fh = fork.account_asset_info(USDC_RESERVE, USDC)["asset-holding"]["amount"]
uh = up.account_asset_info(USDC_RESERVE, USDC)["asset-holding"]["amount"]
ok(fh == uh, "account_asset_info of the USDC reserve equals MainNet")

# Payment from a funded new account.
sk, addr = account.generate_account()
_, bob = account.generate_account()
cheat("set-balance", {"address": addr, "microalgos": 5_000_000})
ok(fork.account_info(addr)["amount"] == 5_000_000, "set-balance shows in account_info")
txid = fork.send_transaction(transaction.PaymentTxn(addr, fork.suggested_params(), bob, 1_000_000).sign(sk))
res = transaction.wait_for_confirmation(fork, txid, 4)
ok(res["confirmed-round"] > 0, "payment confirmed (wait_for_confirmation)")
ok(fork.account_info(bob)["amount"] == 1_000_000, "receiver balance")

# Opt in to USDC, then send USDC from the reserve without its key.
fork.send_transaction(transaction.AssetOptInTxn(addr, fork.suggested_params(), USDC).sign(sk))
cheat("impersonate", {"address": USDC_RESERVE})
xfer = transaction.AssetTransferTxn(USDC_RESERVE, fork.suggested_params(), addr, 25_000_000, USDC)
# py-algorand-sdk has no way to send an unsigned transaction, so post the
# msgpack of a SignedTransaction with no signature directly.
raw = base64.b64decode(encoding.msgpack_encode({"txn": xfer.dictify()}))
req = urllib.request.Request(fork_url + "/v2/transactions", data=raw, headers={"Content-Type": "application/x-binary"})
with urllib.request.urlopen(req) as r:
    txid = json.load(r)["txId"]
transaction.wait_for_confirmation(fork, txid, 4)
ok(fork.account_asset_info(addr, USDC)["asset-holding"]["amount"] == 25_000_000, "impersonated USDC transfer")

# Simulate through the AtomicTransactionComposer, with an execution trace.
atc = AtomicTransactionComposer()
atc.add_transaction(TransactionWithSigner(transaction.PaymentTxn(addr, fork.suggested_params(), bob, 1), AccountTransactionSigner(sk)))
sim = atc.simulate(fork, SimulateRequest(txn_groups=[], exec_trace_config=SimulateTraceConfig(enable=True)))
ok(sim.failure_message == "" and len(sim.tx_ids) == 1, "ATC simulate")

print(f"{checks} checks passed")
