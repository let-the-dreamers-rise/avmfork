// Package chain turns transaction groups into blocks on top of a fork,
// using go-algorand's own block evaluator.
package chain

import (
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/data/committee"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/data/transactions/logic"
	"github.com/algorand/go-algorand/data/transactions/verify"
	"github.com/algorand/go-algorand/ledger/eval"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/forkledger"
	"github.com/let-the-dreamers-rise/avmfork/internal/simulation"
)

// proxySigner signs impersonated transactions so they pass the signature
// check. Its key has no authority over any account; the evaluator then
// checks authorization against the account's real auth address, which the
// chain fills in. The value only has to be a valid key.
var proxySecrets = func() *crypto.SignatureSecrets {
	var seed crypto.Seed
	copy(seed[:], []byte("avmfork impersonation proxy key."))
	return crypto.GenerateSignatureSecrets(seed)
}()

// Confirmed records where a transaction landed.
type Confirmed struct {
	Round basics.Round
	Txn   transactions.SignedTxnInBlock
	// Index within the block's payset.
	Index int
}

// Chain produces one block per submitted group on top of a fork.
type Chain struct {
	mu sync.Mutex
	L  *forkledger.Ledger

	blocks       map[basics.Round]bookkeeping.Block
	deltas       map[basics.Round]ledgercore.StateDelta
	confirmed    map[transactions.Txid]Confirmed
	impersonated map[basics.Address]bool
	timeOffset   int64 // seconds added to wall-clock for new blocks
	nextTime     int64 // if non-zero, exact timestamp of the next block
	tracer       logic.EvalTracer

	// Subscribers are woken when a block is committed.
	waiters []chan struct{}
}

// New returns a chain on top of l.
func New(l *forkledger.Ledger) *Chain {
	return &Chain{
		L:            l,
		blocks:       map[basics.Round]bookkeeping.Block{},
		deltas:       map[basics.Round]ledgercore.StateDelta{},
		confirmed:    map[transactions.Txid]Confirmed{},
		impersonated: map[basics.Address]bool{},
	}
}

// SetTracer installs an evaluation tracer used for every block.
func (c *Chain) SetTracer(t logic.EvalTracer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tracer = t
}

// Impersonate lets addr's transactions through without a valid signature.
func (c *Chain) Impersonate(addr basics.Address, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.impersonated[addr] = true
	} else {
		delete(c.impersonated, addr)
	}
}

// Impersonating reports whether addr is impersonated.
func (c *Chain) Impersonating(addr basics.Address) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.impersonated[addr]
}

// WarpTime moves the clock for future blocks forward by d.
func (c *Chain) WarpTime(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeOffset += int64(d / time.Second)
}

// TimeOffset is the number of seconds added to wall-clock time for new blocks.
func (c *Chain) TimeOffset() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeOffset
}

// SetTimeOffset sets the clock offset in seconds.
func (c *Chain) SetTimeOffset(off int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeOffset = off
}

// SimulateRequest runs fn against the fork's ledger while no block can be
// committed. fn must not change the ledger.
func (c *Chain) SimulateRequest(fn func(l simulation.Ledger) (simulation.Result, error)) (simulation.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fn(c.L)
}

// SetNextTimestamp fixes the timestamp of the next block (unix seconds).
func (c *Chain) SetNextTimestamp(ts int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextTime = ts
}

// Block returns a block produced by the fork.
func (c *Chain) Block(rnd basics.Round) (bookkeeping.Block, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.blocks[rnd]
	return b, ok
}

// Delta returns the state delta of a block produced by the fork.
func (c *Chain) Delta(rnd basics.Round) (ledgercore.StateDelta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.deltas[rnd]
	return d, ok
}

// Confirmed returns where txid landed, if the fork confirmed it.
func (c *Chain) Confirmed(txid transactions.Txid) (Confirmed, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cf, ok := c.confirmed[txid]
	return cf, ok
}

// Wait returns a channel closed at the next committed block.
func (c *Chain) Wait() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan struct{})
	c.waiters = append(c.waiters, ch)
	return ch
}

// GroupError is returned when a group fails. Index is the failing
// transaction's position in the group, or -1 if unknown.
type GroupError struct {
	Index int
	Err   error
}

func (e *GroupError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("transaction %d: %v", e.Index, e.Err)
	}
	return e.Err.Error()
}

func (e *GroupError) Unwrap() error { return e.Err }

// nextHeader builds the header for the block after the latest.
func (c *Chain) nextHeader() (bookkeeping.BlockHeader, error) {
	prev, err := c.L.BlockHdr(c.L.Latest())
	if err != nil {
		return bookkeeping.BlockHeader{}, err
	}
	hdr := bookkeeping.MakeBlock(prev).BlockHeader
	ts := time.Now().Unix() + c.timeOffset
	if c.nextTime != 0 {
		ts = c.nextTime
		c.nextTime = 0
	}
	if ts < prev.TimeStamp {
		ts = prev.TimeStamp
	}
	hdr.TimeStamp = ts
	return hdr, nil
}

// seedFor gives each fork block a deterministic, distinct seed.
func seedFor(rnd basics.Round) committee.Seed {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(rnd))
	return committee.Seed(sha512.Sum512_256(append([]byte("avmfork seed"), b[:]...)))
}

// prepare fills in auth addresses for impersonated transactions and returns
// the copy that should be signature-checked.
func (c *Chain) prepare(group []transactions.SignedTxn) ([]transactions.SignedTxn, []transactions.SignedTxn, error) {
	evalGroup := make([]transactions.SignedTxn, len(group))
	verifyGroup := make([]transactions.SignedTxn, len(group))
	rekeyed := map[basics.Address]basics.Address{}
	for i, stxn := range group {
		evalGroup[i] = stxn
		verifyGroup[i] = stxn
		sender := stxn.Txn.Sender
		if !stxn.HasSignature() && c.impersonated[sender] {
			auth, seen := rekeyed[sender]
			if !seen {
				ad, err := c.L.Account(sender)
				if err != nil {
					return nil, nil, err
				}
				auth = ad.AuthAddr
			}
			if auth == sender {
				auth = basics.Address{}
			}
			evalGroup[i].AuthAddr = auth
			verifyGroup[i] = stxn.Txn.Sign(proxySecrets)
		}
		if !stxn.Txn.RekeyTo.IsZero() {
			rekeyed[sender] = stxn.Txn.RekeyTo
		}
	}
	return evalGroup, verifyGroup, nil
}

// Submit evaluates group as the only group of a new block and commits it.
func (c *Chain) Submit(group []transactions.SignedTxn) (basics.Round, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(group) == 0 {
		return 0, errors.New("empty transaction group")
	}
	for i, stxn := range group {
		if stxn.Txn.Type == protocol.StateProofTx {
			return 0, &GroupError{Index: i, Err: errors.New("state proof transactions are not supported")}
		}
	}
	hdr, err := c.nextHeader()
	if err != nil {
		return 0, err
	}
	evalGroup, verifyGroup, err := c.prepare(group)
	if err != nil {
		return 0, err
	}
	if _, err := verify.TxnGroup(verifyGroup, &hdr, nil, c.L); err != nil {
		idx := -1
		var ge *verify.TxGroupError
		if errors.As(err, &ge) {
			idx = ge.GroupIndex
		}
		return 0, &GroupError{Index: idx, Err: err}
	}
	vb, err := c.evaluate(hdr, evalGroup)
	if err != nil {
		return 0, err
	}
	return c.commitLocked(vb)
}

// AdvanceBlocks commits n empty blocks.
func (c *Chain) AdvanceBlocks(n int) (basics.Round, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var rnd basics.Round
	for i := 0; i < n; i++ {
		hdr, err := c.nextHeader()
		if err != nil {
			return 0, err
		}
		vb, err := c.evaluate(hdr, nil)
		if err != nil {
			return 0, err
		}
		rnd, err = c.commitLocked(vb)
		if err != nil {
			return 0, err
		}
	}
	return rnd, nil
}

// Simulate evaluates group on top of the latest round without committing.
// Signatures are checked unless allowEmptySigs is set, in which case
// unsigned transactions are treated like impersonated ones.
func (c *Chain) Simulate(group []transactions.SignedTxn, allowEmptySigs bool, tracer logic.EvalTracer) (ledgercore.ValidatedBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hdr, err := c.nextHeader()
	if err != nil {
		return ledgercore.ValidatedBlock{}, err
	}
	saved := c.impersonated
	if allowEmptySigs {
		c.impersonated = map[basics.Address]bool{}
		for k := range saved {
			c.impersonated[k] = true
		}
		for _, s := range group {
			c.impersonated[s.Txn.Sender] = true
		}
	}
	evalGroup, verifyGroup, err := c.prepare(group)
	c.impersonated = saved
	if err != nil {
		return ledgercore.ValidatedBlock{}, err
	}
	if _, err := verify.TxnGroup(verifyGroup, &hdr, nil, c.L); err != nil {
		idx := -1
		var ge *verify.TxGroupError
		if errors.As(err, &ge) {
			idx = ge.GroupIndex
		}
		return ledgercore.ValidatedBlock{}, &GroupError{Index: idx, Err: err}
	}
	snap := c.L.Snapshot()
	defer c.L.Revert(snap)
	saveTracer := c.tracer
	if tracer != nil {
		c.tracer = tracer
	}
	defer func() { c.tracer = saveTracer }()
	return c.evaluate(hdr, evalGroup)
}

func (c *Chain) evaluate(hdr bookkeeping.BlockHeader, group []transactions.SignedTxn) (ledgercore.ValidatedBlock, error) {
	ev, err := eval.StartEvaluator(c.L, hdr, eval.EvaluatorOptions{
		PaysetHint: len(group),
		Generate:   true,
		Validate:   true,
		Tracer:     c.tracer,
	})
	if err != nil {
		return ledgercore.ValidatedBlock{}, err
	}
	if len(group) > 0 {
		withAD := make([]transactions.SignedTxnWithAD, len(group))
		for i := range group {
			withAD[i].SignedTxn = group[i]
		}
		if err := ev.TransactionGroup(withAD...); err != nil {
			return ledgercore.ValidatedBlock{}, &GroupError{Index: -1, Err: err}
		}
	}
	ub, err := ev.GenerateBlock(nil)
	if err != nil {
		return ledgercore.ValidatedBlock{}, err
	}
	blk := ub.FinishBlock(seedFor(hdr.Round), basics.Address{}, false)
	return ledgercore.MakeValidatedBlock(blk, ub.UnfinishedDeltas()), nil
}

func (c *Chain) commitLocked(vb ledgercore.ValidatedBlock) (basics.Round, error) {
	blk := vb.Block()
	if err := c.L.Commit(blk, vb.Delta()); err != nil {
		return 0, err
	}
	rnd := blk.Round()
	c.blocks[rnd] = blk
	c.deltas[rnd] = vb.Delta()
	for i, stib := range blk.Payset {
		stxn, _, err := blk.DecodeSignedTxn(stib)
		if err != nil {
			return 0, err
		}
		c.confirmed[stxn.ID()] = Confirmed{Round: rnd, Txn: stib, Index: i}
	}
	for _, w := range c.waiters {
		close(w)
	}
	c.waiters = nil
	return rnd, nil
}

// Snapshot saves the fork's state and its block history.
type Snapshot struct {
	ledger forkledger.Snapshot
	round  basics.Round
	offset int64
}

// Snapshot saves the current state.
func (c *Chain) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{ledger: c.L.Snapshot(), round: c.L.Latest(), offset: c.timeOffset}
}

// Revert restores a snapshot and forgets the blocks produced after it.
func (c *Chain) Revert(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.Revert(s.ledger)
	c.timeOffset = s.offset
	for r := range c.blocks {
		if r > s.round {
			delete(c.blocks, r)
			delete(c.deltas, r)
		}
	}
	for id, cf := range c.confirmed {
		if cf.Round > s.round {
			delete(c.confirmed, id)
		}
	}
}

// Proto returns the consensus parameters of the latest round.
func (c *Chain) Proto() (config.ConsensusParams, error) {
	h, err := c.L.BlockHdr(c.L.Latest())
	if err != nil {
		return config.ConsensusParams{}, err
	}
	p, ok := config.Consensus[h.CurrentProtocol]
	if !ok {
		return config.ConsensusParams{}, fmt.Errorf("unknown protocol %s", h.CurrentProtocol)
	}
	return p, nil
}
