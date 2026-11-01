// End-to-end tests against a fork of MainNet. They need network access and
// run only when AVMFORK_E2E=1.
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/daemon/algod/api/client"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/chain"
	"github.com/let-the-dreamers-rise/avmfork/forkledger"
	"github.com/let-the-dreamers-rise/avmfork/server"
)

// USDC on MainNet.
const usdc = basics.AssetIndex(31566704)

type env struct {
	t   *testing.T
	srv *httptest.Server
	rc  client.RestClient
	l   *forkledger.Ledger
}

func setup(t *testing.T) *env {
	if os.Getenv("AVMFORK_E2E") != "1" {
		t.Skip("set AVMFORK_E2E=1 to run tests against MainNet")
	}
	upstream := os.Getenv("AVMFORK_UPSTREAM")
	if upstream == "" {
		upstream = "https://mainnet-api.algonode.cloud"
	}
	remote, err := forkledger.NewRemote(upstream, "")
	if err != nil {
		t.Fatal(err)
	}
	l, err := forkledger.Open(remote)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.NewNode(chain.New(l)), server.Options{}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &env{t: t, srv: srv, rc: client.MakeRestClient(*u, ""), l: l}
}

func (e *env) cheat(path string, body any) map[string]any {
	e.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(e.srv.URL+"/avmfork/"+path, "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("%s: HTTP %d %v", path, resp.StatusCode, out)
	}
	return out
}

func newKey(t *testing.T) (*crypto.SignatureSecrets, basics.Address) {
	var seed crypto.Seed
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	s := crypto.GenerateSignatureSecrets(seed)
	return s, basics.Address(s.SignatureVerifier)
}

func (e *env) params() transactions.Header {
	e.t.Helper()
	p, err := e.rc.SuggestedParams()
	if err != nil {
		e.t.Fatal(err)
	}
	var gh crypto.Digest
	copy(gh[:], p.GenesisHash)
	return transactions.Header{
		Fee:         basics.MicroAlgos{Raw: p.MinFee},
		FirstValid:  p.LastRound + 1,
		LastValid:   p.LastRound + 1000,
		GenesisID:   p.GenesisId,
		GenesisHash: gh,
	}
}

func (e *env) send(stxn transactions.SignedTxn) {
	e.t.Helper()
	if _, err := e.rc.SendRawTransaction(stxn); err != nil {
		e.t.Fatal(err)
	}
	info, err := e.rc.PendingTransactionInformation(stxn.ID().String())
	if err != nil {
		e.t.Fatal(err)
	}
	if info.ConfirmedRound == nil || *info.ConfirmedRound == 0 {
		e.t.Fatalf("not confirmed: %+v", info)
	}
}

func (e *env) balance(a basics.Address) uint64 {
	e.t.Helper()
	acct, err := e.rc.AccountInformation(a.String(), false)
	if err != nil {
		e.t.Fatal(err)
	}
	return acct.Amount
}

func TestPaymentWithCheatedBalance(t *testing.T) {
	e := setup(t)
	sk, alice := newKey(t)
	_, bob := newKey(t)
	e.cheat("set-balance", map[string]any{"address": alice.String(), "microalgos": 10_000_000})

	hdr := e.params()
	hdr.Sender = alice
	txn := transactions.Transaction{
		Type:   protocol.PaymentTx,
		Header: hdr,
		PaymentTxnFields: transactions.PaymentTxnFields{
			Receiver: bob,
			Amount:   basics.MicroAlgos{Raw: 2_000_000},
		},
	}
	e.send(txn.Sign(sk))
	if got := e.balance(bob); got != 2_000_000 {
		t.Fatalf("bob has %d, want 2000000", got)
	}
	if got := e.balance(alice); got != 10_000_000-2_000_000-hdr.Fee.Raw {
		t.Fatalf("alice has %d", got)
	}
}

func TestImpersonateRealAccount(t *testing.T) {
	e := setup(t)
	// Send from an existing MainNet account without its key: the USDC
	// creator/reserve account must hold USDC on MainNet.
	reserve, err := basics.UnmarshalChecksumAddress("2UEQTE5QDNXPI7M3TU44G6SYKLFWLPQO7EBZM7K7MHMQQMFI4QJPLHQFHM")
	if err != nil {
		t.Fatal(err)
	}
	sk, alice := newKey(t)
	e.cheat("set-balance", map[string]any{"address": alice.String(), "microalgos": 1_000_000})
	hdr := e.params()
	hdr.Sender = alice
	optin := transactions.Transaction{
		Type:                   protocol.AssetTransferTx,
		Header:                 hdr,
		AssetTransferTxnFields: transactions.AssetTransferTxnFields{XferAsset: usdc, AssetReceiver: alice},
	}
	e.send(optin.Sign(sk))

	e.cheat("impersonate", map[string]any{"address": reserve.String()})
	hdr = e.params()
	hdr.Sender = reserve
	xfer := transactions.Transaction{
		Type:   protocol.AssetTransferTx,
		Header: hdr,
		AssetTransferTxnFields: transactions.AssetTransferTxnFields{
			XferAsset: usdc, AssetReceiver: alice, AssetAmount: 5_000_000,
		},
	}
	e.send(transactions.SignedTxn{Txn: xfer}) // no signature
	h, err := e.rc.AccountAssetInformation(alice.String(), usdc)
	if err != nil {
		t.Fatal(err)
	}
	if h.AssetHolding == nil || h.AssetHolding.Amount != 5_000_000 {
		t.Fatalf("alice USDC holding %+v", h.AssetHolding)
	}
	// Without impersonation the same unsigned transfer is rejected.
	e.cheat("impersonate", map[string]any{"address": reserve.String(), "enabled": false})
	xfer.Note = []byte("again")
	if _, err := e.rc.SendRawTransaction(transactions.SignedTxn{Txn: xfer}); err == nil {
		t.Fatal("unsigned transfer accepted without impersonation")
	}
}

func TestSnapshotRevert(t *testing.T) {
	e := setup(t)
	_, alice := newKey(t)
	snap := e.cheat("snapshot", map[string]any{})
	start := e.l.Latest()
	e.cheat("set-balance", map[string]any{"address": alice.String(), "microalgos": 7})
	e.cheat("mine", map[string]any{"blocks": 3})
	if e.l.Latest() != start+3 {
		t.Fatalf("latest %d, want %d", e.l.Latest(), start+3)
	}
	e.cheat("revert", map[string]any{"id": snap["id"]})
	if e.l.Latest() != start {
		t.Fatalf("after revert latest %d, want %d", e.l.Latest(), start)
	}
	if got := e.balance(alice); got != 0 {
		t.Fatalf("after revert alice has %d", got)
	}
}

func TestWarp(t *testing.T) {
	e := setup(t)
	before, err := e.l.BlockHdr(e.l.Latest())
	if err != nil {
		t.Fatal(err)
	}
	e.cheat("warp", map[string]any{"seconds": 30 * 24 * 3600})
	e.cheat("mine", map[string]any{"blocks": 1})
	after, err := e.l.BlockHdr(e.l.Latest())
	if err != nil {
		t.Fatal(err)
	}
	if after.TimeStamp-before.TimeStamp < 30*24*3600 {
		t.Fatalf("timestamp moved %d s", after.TimeStamp-before.TimeStamp)
	}
}
