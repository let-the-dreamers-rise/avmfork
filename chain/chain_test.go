package chain

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/data/transactions/logic"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/forkledger"
)

// These tests run on an empty chain (no upstream node), so they need no
// network. The MainNet tests are in ../e2e.

type key struct {
	sk   *crypto.SignatureSecrets
	addr basics.Address
}

func newKey(t *testing.T) key {
	t.Helper()
	var seed crypto.Seed
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	sk := crypto.GenerateSignatureSecrets(seed)
	return key{sk: sk, addr: basics.Address(sk.SignatureVerifier)}
}

func newChain(t *testing.T) *Chain {
	t.Helper()
	l, err := forkledger.NewEmpty("avmfork-test", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return New(l)
}

func (c *Chain) header(t *testing.T, sender basics.Address) transactions.Header {
	t.Helper()
	return transactions.Header{
		Sender:      sender,
		Fee:         basics.MicroAlgos{Raw: 1000},
		FirstValid:  c.L.Latest(),
		LastValid:   c.L.Latest() + 1000,
		GenesisID:   c.L.GenesisID(),
		GenesisHash: c.L.GenesisHash(),
	}
}

func (c *Chain) balance(t *testing.T, a basics.Address) uint64 {
	t.Helper()
	ad, err := c.L.Account(a)
	if err != nil {
		t.Fatal(err)
	}
	return ad.MicroAlgos.Raw
}

func fund(t *testing.T, c *Chain, a basics.Address, amt uint64) {
	t.Helper()
	if err := c.L.SetBalance(a, basics.MicroAlgos{Raw: amt}); err != nil {
		t.Fatal(err)
	}
}

func pay(c *Chain, t *testing.T, from, to basics.Address, amt uint64) transactions.Transaction {
	return transactions.Transaction{
		Type:             protocol.PaymentTx,
		Header:           c.header(t, from),
		PaymentTxnFields: transactions.PaymentTxnFields{Receiver: to, Amount: basics.MicroAlgos{Raw: amt}},
	}
}

func TestPayment(t *testing.T) {
	c := newChain(t)
	alice, bob := newKey(t), newKey(t)
	fund(t, c, alice.addr, 10_000_000)
	sinkBefore := c.balance(t, forkledger.EmptyFeeSink)

	txn := pay(c, t, alice.addr, bob.addr, 2_000_000)
	rnd, err := c.Submit([]transactions.SignedTxn{txn.Sign(alice.sk)})
	if err != nil {
		t.Fatal(err)
	}
	if rnd != 1 {
		t.Fatalf("round %d, want 1", rnd)
	}
	if got := c.balance(t, bob.addr); got != 2_000_000 {
		t.Fatalf("bob %d", got)
	}
	if got := c.balance(t, alice.addr); got != 10_000_000-2_000_000-1000 {
		t.Fatalf("alice %d", got)
	}
	if got := c.balance(t, forkledger.EmptyFeeSink) - sinkBefore; got != 1000 {
		t.Fatalf("fee sink got %d", got)
	}
	if _, ok := c.Confirmed(txn.ID()); !ok {
		t.Fatal("payment not recorded as confirmed")
	}
}

func TestRejectsBadSignatureAndDuplicate(t *testing.T) {
	c := newChain(t)
	alice, mallory := newKey(t), newKey(t)
	fund(t, c, alice.addr, 10_000_000)
	txn := pay(c, t, alice.addr, mallory.addr, 1)

	var ge *GroupError
	if _, err := c.Submit([]transactions.SignedTxn{txn.Sign(mallory.sk)}); !errors.As(err, &ge) {
		t.Fatalf("wrong signer accepted: %v", err)
	}
	if _, err := c.Submit([]transactions.SignedTxn{{Txn: txn}}); err == nil {
		t.Fatal("unsigned transaction accepted without impersonation")
	}
	if _, err := c.Submit([]transactions.SignedTxn{txn.Sign(alice.sk)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit([]transactions.SignedTxn{txn.Sign(alice.sk)}); err == nil {
		t.Fatal("duplicate transaction accepted")
	}
}

func TestImpersonateRekeyedAccount(t *testing.T) {
	c := newChain(t)
	alice, auth, bob := newKey(t), newKey(t), newKey(t)
	fund(t, c, alice.addr, 10_000_000)

	rekey := pay(c, t, alice.addr, alice.addr, 0)
	rekey.RekeyTo = auth.addr
	if _, err := c.Submit([]transactions.SignedTxn{rekey.Sign(alice.sk)}); err != nil {
		t.Fatal(err)
	}
	// alice's own key no longer works.
	txn := pay(c, t, alice.addr, bob.addr, 1_000_000)
	if _, err := c.Submit([]transactions.SignedTxn{txn.Sign(alice.sk)}); err == nil {
		t.Fatal("old key accepted after rekey")
	}
	c.Impersonate(alice.addr, true)
	if _, err := c.Submit([]transactions.SignedTxn{{Txn: txn}}); err != nil {
		t.Fatalf("impersonated send: %v", err)
	}
	if got := c.balance(t, bob.addr); got != 1_000_000 {
		t.Fatalf("bob %d", got)
	}
	// The block records the real auth address, as the network would.
	conf, _ := c.Confirmed(txn.ID())
	if conf.Txn.AuthAddr != auth.addr {
		t.Fatalf("auth addr %v, want %v", conf.Txn.AuthAddr, auth.addr)
	}
	c.Impersonate(alice.addr, false)
	txn.Note = []byte("again")
	if _, err := c.Submit([]transactions.SignedTxn{{Txn: txn}}); err == nil {
		t.Fatal("unsigned send accepted after impersonation was turned off")
	}
}

const approval = `#pragma version 10
txn ApplicationID
bz create
txna ApplicationArgs 0
byte "set"
==
bnz set
byte "x"
app_global_get
txna ApplicationArgs 1
btoi
==
return
set:
byte "x"
txna ApplicationArgs 1
btoi
app_global_put
byte "b"
int 8
box_create
pop
int 1
return
create:
int 1
return
`

func assemble(t *testing.T, src string) []byte {
	t.Helper()
	ops, err := logic.AssembleString(src)
	if err != nil {
		t.Fatal(err)
	}
	return ops.Program
}

func appCall(c *Chain, t *testing.T, from basics.Address, app basics.AppIndex, args ...[]byte) transactions.Transaction {
	return transactions.Transaction{
		Type:   protocol.ApplicationCallTx,
		Header: c.header(t, from),
		ApplicationCallTxnFields: transactions.ApplicationCallTxnFields{
			ApplicationID:   app,
			ApplicationArgs: args,
			Boxes:           []transactions.BoxRef{{Index: 0, Name: []byte("b")}},
		},
	}
}

func itob(v uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}

func TestAppStateBoxesAndSnapshots(t *testing.T) {
	c := newChain(t)
	alice := newKey(t)
	fund(t, c, alice.addr, 100_000_000)

	create := transactions.Transaction{
		Type:   protocol.ApplicationCallTx,
		Header: c.header(t, alice.addr),
		ApplicationCallTxnFields: transactions.ApplicationCallTxnFields{
			ApprovalProgram:   assemble(t, approval),
			ClearStateProgram: assemble(t, "#pragma version 10\nint 1"),
			GlobalStateSchema: basics.StateSchema{NumUint: 1},
		},
	}
	if _, err := c.Submit([]transactions.SignedTxn{create.Sign(alice.sk)}); err != nil {
		t.Fatal(err)
	}
	conf, _ := c.Confirmed(create.ID())
	app := conf.Txn.ApplyData.ApplicationID
	if app == 0 {
		t.Fatal("no app id in apply data")
	}
	fund(t, c, app.Address(), 1_000_000) // box minimum balance

	snap := c.Snapshot()
	set := appCall(c, t, alice.addr, app, []byte("set"), itob(42))
	if _, err := c.Submit([]transactions.SignedTxn{set.Sign(alice.sk)}); err != nil {
		t.Fatal(err)
	}
	check := appCall(c, t, alice.addr, app, []byte("check"), itob(42))
	if _, err := c.Submit([]transactions.SignedTxn{check.Sign(alice.sk)}); err != nil {
		t.Fatalf("check 42: %v", err)
	}
	if v, err := c.L.Box(app, "b"); err != nil || len(v) != 8 {
		t.Fatalf("box %v %v", v, err)
	}

	// Change the state behind the program's back.
	if err := c.L.SetAppState(app, nil, "x", &basics.TealValue{Type: basics.TealUintType, Uint: 7}); err != nil {
		t.Fatal(err)
	}
	check = appCall(c, t, alice.addr, app, []byte("check"), itob(7))
	if _, err := c.Submit([]transactions.SignedTxn{check.Sign(alice.sk)}); err != nil {
		t.Fatalf("check 7 after cheat: %v", err)
	}
	if err := c.L.SetAppState(app, nil, "y", &basics.TealValue{Type: basics.TealUintType, Uint: 1}); err == nil {
		t.Fatal("cheat exceeded the global schema")
	}

	c.Revert(snap)
	if v, err := c.L.Box(app, "b"); err != nil || v != nil {
		t.Fatalf("box survived revert: %v %v", v, err)
	}
	if _, ok := c.Confirmed(set.ID()); ok {
		t.Fatal("reverted transaction still confirmed")
	}
	// The same transaction can be sent again after a revert.
	if _, err := c.Submit([]transactions.SignedTxn{set.Sign(alice.sk)}); err != nil {
		t.Fatalf("resend after revert: %v", err)
	}
}

func TestClockJump(t *testing.T) {
	c := newChain(t)
	start := c.L.Latest()
	c.WarpTime(30 * 24 * time.Hour)
	rnd, err := c.AdvanceBlocks(1)
	if err != nil {
		t.Fatal(err)
	}
	// One empty clock block, then the requested one.
	if rnd != start+2 {
		t.Fatalf("round %d, want %d", rnd, start+2)
	}
	before, _ := c.L.BlockHdr(start)
	after, _ := c.L.BlockHdr(rnd)
	if after.TimeStamp-before.TimeStamp < 30*24*3600 {
		t.Fatalf("clock moved %d s", after.TimeStamp-before.TimeStamp)
	}
	// Ordinary blocks after the jump need no extra block.
	if rnd2, err := c.AdvanceBlocks(1); err != nil || rnd2 != rnd+1 {
		t.Fatalf("next block %d %v", rnd2, err)
	}
}
