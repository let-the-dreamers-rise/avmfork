// Package server serves a fork over algod's own REST API, so existing SDKs
// and tools work against it unchanged.
package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/algorand/avm-abi/apps"
	"github.com/algorand/go-algorand/agreement"
	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	v2 "github.com/algorand/go-algorand/daemon/algod/api/server/v2"
	"github.com/algorand/go-algorand/data/account"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/data/transactions/logic"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	upstreamsim "github.com/algorand/go-algorand/ledger/simulation"
	"github.com/algorand/go-algorand/network"
	"github.com/algorand/go-algorand/node"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/chain"
	"github.com/let-the-dreamers-rise/avmfork/forkledger"
	"github.com/let-the-dreamers-rise/avmfork/internal/simulation"
)

var errNotSupported = errors.New("not supported by avmfork")

// Node adapts a chain to algod's v2.NodeInterface.
type Node struct {
	C       *chain.Chain
	started time.Time
}

// NewNode wraps c.
func NewNode(c *chain.Chain) *Node {
	return &Node{C: c, started: time.Now()}
}

// LedgerForAPI returns the API view of the fork.
func (n *Node) LedgerForAPI() v2.LedgerForAPI { return &apiLedger{c: n.C, l: n.C.L} }

// Status reports the fork's latest round.
func (n *Node) Status() (node.StatusReport, error) {
	l := n.C.L
	hdr, err := l.BlockHdr(l.Latest())
	if err != nil {
		return node.StatusReport{}, err
	}
	return node.StatusReport{
		LastRound:             hdr.Round,
		LastVersion:           hdr.CurrentProtocol,
		NextVersion:           hdr.NextProtocol,
		NextVersionRound:      hdr.NextProtocolSwitchOn,
		NextVersionSupported:  true,
		LastRoundTimestamp:    time.Unix(hdr.TimeStamp, 0),
		HasSyncedSinceStartup: true,
	}, nil
}

// GenesisID of the forked network.
func (n *Node) GenesisID() string { return n.C.L.GenesisID() }

// GenesisHash of the forked network.
func (n *Node) GenesisHash() crypto.Digest { return n.C.L.GenesisHash() }

// BroadcastSignedTxGroup evaluates the group into a new block immediately.
// algod returns pool errors from this call, and so does the fork.
func (n *Node) BroadcastSignedTxGroup(txgroup []transactions.SignedTxn) error {
	_, err := n.C.Submit(txgroup)
	return err
}

// AsyncBroadcastSignedTxGroup is the same as BroadcastSignedTxGroup.
func (n *Node) AsyncBroadcastSignedTxGroup(txgroup []transactions.SignedTxn) error {
	return n.BroadcastSignedTxGroup(txgroup)
}

// Simulate is unused: the server routes simulate to avmfork's copy of the
// simulator directly (see SimulateTransaction).
func (n *Node) Simulate(upstreamsim.Request) (upstreamsim.Result, error) {
	return upstreamsim.Result{}, errNotSupported
}

// simulate runs avmfork's copy of algod's simulator on the fork.
func (n *Node) simulate(req simulation.Request) (simulation.Result, error) {
	return n.C.SimulateRequest(func(l simulation.Ledger) (simulation.Result, error) {
		return simulation.MakeSimulator(l, true).Simulate(req)
	})
}

// GetPeers returns no peers.
func (n *Node) GetPeers() ([]network.Peer, []network.Peer, error) { return nil, nil, nil }

// GetPendingTransaction finds a transaction the fork confirmed. The fork has
// no pool: a group is either in a block or was rejected on submit.
func (n *Node) GetPendingTransaction(txID transactions.Txid) (node.TxnWithStatus, bool) {
	cf, ok := n.C.Confirmed(txID)
	if !ok {
		return node.TxnWithStatus{}, false
	}
	blk, ok := n.C.Block(cf.Round)
	if !ok {
		return node.TxnWithStatus{}, false
	}
	stxn, ad, err := blk.DecodeSignedTxn(cf.Txn)
	if err != nil {
		return node.TxnWithStatus{}, false
	}
	return node.TxnWithStatus{Txn: stxn, ConfirmedRound: cf.Round, ApplyData: ad}, true
}

// GetPendingTxnsFromPool returns nothing; the fork has no pool.
func (n *Node) GetPendingTxnsFromPool() ([]transactions.SignedTxn, error) { return nil, nil }

// SuggestedFee is the protocol minimum.
func (n *Node) SuggestedFee() basics.MicroAlgos { return basics.MicroAlgos{} }

// StartCatchup is not supported.
func (n *Node) StartCatchup(string) error { return errNotSupported }

// AbortCatchup is not supported.
func (n *Node) AbortCatchup(string) error { return errNotSupported }

// Config returns a node config with the developer API on, which algod
// requires for compile, disassemble and state deltas.
func (n *Node) Config() config.Local {
	c := config.GetDefaultLocal()
	c.EnableDeveloperAPI = true
	c.EnableExperimentalAPI = true
	c.EnableFollowMode = true
	return c
}

// InstallParticipationKey is not supported.
func (n *Node) InstallParticipationKey([]byte) (account.ParticipationID, error) {
	return account.ParticipationID{}, errNotSupported
}

// ListParticipationKeys returns none.
func (n *Node) ListParticipationKeys() ([]account.ParticipationRecord, error) { return nil, nil }

// GetParticipationKey is not supported.
func (n *Node) GetParticipationKey(account.ParticipationID) (account.ParticipationRecord, error) {
	return account.ParticipationRecord{}, errNotSupported
}

// RemoveParticipationKey is not supported.
func (n *Node) RemoveParticipationKey(account.ParticipationID) error { return errNotSupported }

// AppendParticipationKeys is not supported.
func (n *Node) AppendParticipationKeys(account.ParticipationID, account.StateProofKeys) error {
	return errNotSupported
}

// SetSyncRound is not supported.
func (n *Node) SetSyncRound(basics.Round) error { return errNotSupported }

// GetSyncRound returns 0.
func (n *Node) GetSyncRound() basics.Round { return 0 }

// UnsetSyncRound does nothing.
func (n *Node) UnsetSyncRound() {}

// GetBlockTimeStampOffset reports the fork's clock offset, matching algod's
// dev-mode endpoint so existing LocalNet tooling can move time.
func (n *Node) GetBlockTimeStampOffset() (*int64, error) {
	off := n.C.TimeOffset()
	return &off, nil
}

// SetBlockTimeStampOffset sets the fork's clock offset in seconds.
func (n *Node) SetBlockTimeStampOffset(off int64) error {
	n.C.SetTimeOffset(off)
	return nil
}

// apiLedger implements v2.LedgerForAPI over the fork.
type apiLedger struct {
	c *chain.Chain
	l *forkledger.Ledger
}

func (a *apiLedger) LookupAccount(rnd basics.Round, addr basics.Address) (ledgercore.AccountData, basics.Round, basics.MicroAlgos, error) {
	ad, r, err := a.l.LookupWithoutRewards(a.l.Latest(), addr)
	if err != nil {
		return ledgercore.AccountData{}, 0, basics.MicroAlgos{}, err
	}
	without := ad.MicroAlgos
	hdr, err := a.l.BlockHdr(r)
	if err != nil {
		return ledgercore.AccountData{}, 0, basics.MicroAlgos{}, err
	}
	ad = ad.WithUpdatedRewards(config.Consensus[hdr.CurrentProtocol].RewardUnit, hdr.RewardsLevel)
	return ad, r, without, nil
}

func (a *apiLedger) LookupLatest(addr basics.Address) (basics.AccountData, basics.Round, basics.MicroAlgos, error) {
	rnd := a.l.Latest()
	base, _, without, err := a.LookupAccount(rnd, addr)
	if err != nil {
		return basics.AccountData{}, 0, basics.MicroAlgos{}, err
	}
	var full basics.AccountData
	if r := a.l.Remote(); r != nil {
		full, _, err = r.FullAccount(addr)
		if err != nil {
			return basics.AccountData{}, 0, basics.MicroAlgos{}, err
		}
	}
	out := basics.AccountData{}
	ledgercore.AssignAccountData(&out, base)
	out.AssetParams = map[basics.AssetIndex]basics.AssetParams{}
	out.Assets = map[basics.AssetIndex]basics.AssetHolding{}
	out.AppParams = map[basics.AppIndex]basics.AppParams{}
	out.AppLocalStates = map[basics.AppIndex]basics.AppLocalState{}
	for k, v := range full.AssetParams {
		out.AssetParams[k] = v
	}
	for k, v := range full.Assets {
		out.Assets[k] = v
	}
	for k, v := range full.AppParams {
		out.AppParams[k] = v
	}
	for k, v := range full.AppLocalStates {
		out.AppLocalStates[k] = v
	}
	localAssets, localApps := a.l.LocalResources(addr)
	for id, r := range localAssets {
		delete(out.AssetParams, id)
		delete(out.Assets, id)
		if r.AssetParams != nil {
			out.AssetParams[id] = *r.AssetParams
		}
		if r.AssetHolding != nil {
			out.Assets[id] = *r.AssetHolding
		}
	}
	for id, r := range localApps {
		delete(out.AppParams, id)
		delete(out.AppLocalStates, id)
		if r.AppParams != nil {
			out.AppParams[id] = *r.AppParams
		}
		if r.AppLocalState != nil {
			out.AppLocalStates[id] = *r.AppLocalState
		}
	}
	if len(out.AssetParams) == 0 {
		out.AssetParams = nil
	}
	if len(out.Assets) == 0 {
		out.Assets = nil
	}
	if len(out.AppParams) == 0 {
		out.AppParams = nil
	}
	if len(out.AppLocalStates) == 0 {
		out.AppLocalStates = nil
	}
	return out, rnd, without, nil
}

func (a *apiLedger) LookupKv(rnd basics.Round, key string) ([]byte, error) {
	return a.l.LookupKv(a.l.Latest(), key)
}

// allKeys merges the remote node's box keys with the fork's own changes.
func (a *apiLedger) allKeys(prefix string) ([]string, error) {
	set := map[string]bool{}
	if r := a.l.Remote(); r != nil && strings.HasPrefix(prefix, "bx:") && len(prefix) >= 11 {
		app, _, err := apps.SplitBoxKey(prefix[:11])
		if err != nil {
			return nil, err
		}
		names, err := r.BoxNames(basics.AppIndex(app))
		if err != nil {
			return nil, err
		}
		for _, nm := range names {
			k := apps.MakeBoxKey(app, nm)
			if strings.HasPrefix(k, prefix) {
				set[k] = true
			}
		}
	}
	for k, exists := range a.l.LocalBoxes(prefix) {
		set[k] = exists
	}
	keys := make([]string, 0, len(set))
	for k, exists := range set {
		if exists {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (a *apiLedger) LookupKeysByPrefix(rnd basics.Round, prefix string, max uint64) ([]string, error) {
	keys, err := a.allKeys(prefix)
	if err != nil {
		return nil, err
	}
	if max > 0 && uint64(len(keys)) > max {
		keys = keys[:max]
	}
	return keys, nil
}

func (a *apiLedger) LookupKvPairsByPrefix(rnd basics.Round, prefix, cursor string, limit, maxBytes uint64, includeValues bool) ([]ledgercore.KvPairResult, basics.Round, bool, error) {
	keys, err := a.allKeys(prefix)
	if err != nil {
		return nil, 0, false, err
	}
	var out []ledgercore.KvPairResult
	var size uint64
	for i, k := range keys {
		if cursor != "" && k <= cursor {
			continue
		}
		if limit > 0 && uint64(len(out)) >= limit {
			return out, a.l.Latest(), true, nil
		}
		kv := ledgercore.KvPairResult{Key: k}
		if includeValues {
			v, err := a.l.LookupKv(a.l.Latest(), k)
			if err != nil {
				return nil, 0, false, err
			}
			if v == nil {
				v = []byte{}
			}
			kv.Value = v
		}
		if maxBytes > 0 && size+kv.ByteSize() > maxBytes && len(out) > 0 {
			return out, a.l.Latest(), i < len(keys), nil
		}
		size += kv.ByteSize()
		out = append(out, kv)
	}
	return out, a.l.Latest(), false, nil
}

func (a *apiLedger) ConsensusParams(r basics.Round) (config.ConsensusParams, error) {
	hdr, err := a.l.BlockHdr(r)
	if err != nil {
		return config.ConsensusParams{}, err
	}
	p, ok := config.Consensus[hdr.CurrentProtocol]
	if !ok {
		return config.ConsensusParams{}, fmt.Errorf("unknown protocol %s", hdr.CurrentProtocol)
	}
	return p, nil
}

func (a *apiLedger) Latest() basics.Round { return a.l.Latest() }

func (a *apiLedger) LookupAsset(rnd basics.Round, addr basics.Address, aidx basics.AssetIndex) (ledgercore.AssetResource, error) {
	return a.l.LookupAsset(a.l.Latest(), addr, aidx)
}

func (a *apiLedger) LookupApplication(rnd basics.Round, addr basics.Address, aidx basics.AppIndex) (ledgercore.AppResource, error) {
	return a.l.LookupApplication(a.l.Latest(), addr, aidx)
}

func (a *apiLedger) LookupAssets(addr basics.Address, gt basics.AssetIndex, limit uint64) ([]ledgercore.AssetResourceWithIDs, basics.Round, error) {
	full, rnd, _, err := a.LookupLatest(addr)
	if err != nil {
		return nil, 0, err
	}
	ids := map[basics.AssetIndex]bool{}
	for id := range full.Assets {
		ids[id] = true
	}
	for id := range full.AssetParams {
		ids[id] = true
	}
	sorted := make([]basics.AssetIndex, 0, len(ids))
	for id := range ids {
		if id > gt {
			sorted = append(sorted, id)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var out []ledgercore.AssetResourceWithIDs
	for _, id := range sorted {
		if limit > 0 && uint64(len(out)) >= limit {
			break
		}
		r := ledgercore.AssetResourceWithIDs{AssetID: id}
		if h, ok := full.Assets[id]; ok {
			h := h
			r.AssetHolding = &h
		}
		if p, ok := full.AssetParams[id]; ok {
			p := p
			r.AssetParams = &p
			r.Creator = addr
		} else if c, ok, err := a.l.Creator(basics.CreatableIndex(id), basics.AssetCreatable); err == nil && ok {
			r.Creator = c
			if cr, err := a.l.LookupAsset(a.l.Latest(), c, id); err == nil {
				r.AssetParams = cr.AssetParams
			}
		}
		out = append(out, r)
	}
	return out, rnd, nil
}

func (a *apiLedger) LookupApplications(addr basics.Address, gt basics.AppIndex, limit uint64, includeParams bool) ([]ledgercore.AppResourceWithIDs, basics.Round, error) {
	full, rnd, _, err := a.LookupLatest(addr)
	if err != nil {
		return nil, 0, err
	}
	ids := map[basics.AppIndex]bool{}
	for id := range full.AppLocalStates {
		ids[id] = true
	}
	for id := range full.AppParams {
		ids[id] = true
	}
	sorted := make([]basics.AppIndex, 0, len(ids))
	for id := range ids {
		if id > gt {
			sorted = append(sorted, id)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var out []ledgercore.AppResourceWithIDs
	for _, id := range sorted {
		if limit > 0 && uint64(len(out)) >= limit {
			break
		}
		r := ledgercore.AppResourceWithIDs{AppID: id}
		if s, ok := full.AppLocalStates[id]; ok {
			s := s
			r.AppLocalState = &s
		}
		if p, ok := full.AppParams[id]; ok {
			r.Creator = addr
			if includeParams {
				p := p
				r.AppParams = &p
			}
		} else if c, ok, err := a.l.Creator(basics.CreatableIndex(id), basics.AppCreatable); err == nil && ok {
			r.Creator = c
			if includeParams {
				if cr, err := a.l.LookupApplication(a.l.Latest(), c, id); err == nil {
					r.AppParams = cr.AppParams
				}
			}
		}
		out = append(out, r)
	}
	return out, rnd, nil
}

func (a *apiLedger) Block(rnd basics.Round) (bookkeeping.Block, error) {
	if b, ok := a.c.Block(rnd); ok {
		return b, nil
	}
	if r := a.l.Remote(); r != nil && rnd <= a.l.ForkRound() {
		return r.Block(rnd)
	}
	return bookkeeping.Block{}, fmt.Errorf("round %d not available", rnd)
}

func (a *apiLedger) BlockCert(rnd basics.Round) (bookkeeping.Block, agreement.Certificate, error) {
	b, err := a.Block(rnd)
	return b, agreement.Certificate{Round: rnd}, err
}

func (a *apiLedger) EncodedBlockCert(rnd basics.Round) ([]byte, []byte, error) {
	b, cert, err := a.BlockCert(rnd)
	if err != nil {
		return nil, nil, err
	}
	return protocol.Encode(&b), protocol.Encode(&cert), nil
}

func (a *apiLedger) LatestTotals() (basics.Round, ledgercore.AccountTotals, error) {
	return a.l.LatestTotals()
}

func (a *apiLedger) OnlineCirculation(rnd, voteRnd basics.Round) (basics.MicroAlgos, error) {
	return a.l.OnlineCirculation(rnd, voteRnd)
}

func (a *apiLedger) BlockHdr(rnd basics.Round) (bookkeeping.BlockHeader, error) {
	return a.l.BlockHdr(rnd)
}

// Wait returns a channel that is closed once round r is committed.
func (a *apiLedger) Wait(r basics.Round) chan struct{} {
	ch, _ := a.WaitWithCancel(r)
	return ch
}

func (a *apiLedger) WaitWithCancel(r basics.Round) (chan struct{}, func()) {
	out := make(chan struct{})
	done := make(chan struct{})
	go func() {
		for {
			wake := a.c.Wait()
			if a.l.Latest() >= r {
				close(out)
				return
			}
			select {
			case <-wake:
			case <-done:
				return
			}
		}
	}()
	var once bool
	return out, func() {
		if !once {
			once = true
			close(done)
		}
	}
}

func (a *apiLedger) GetCreator(cidx basics.CreatableIndex, ctype basics.CreatableType) (basics.Address, bool, error) {
	return a.l.Creator(cidx, ctype)
}

func (a *apiLedger) TxnsFrom(basics.Address, basics.Round) ([]transactions.Transaction, error) {
	return nil, nil
}

func (a *apiLedger) GetStateDeltaForRound(rnd basics.Round) (ledgercore.StateDelta, error) {
	if d, ok := a.c.Delta(rnd); ok {
		return d, nil
	}
	if r := a.l.Remote(); r != nil && rnd <= a.l.ForkRound() {
		return r.Delta(rnd)
	}
	return ledgercore.StateDelta{}, fmt.Errorf("no delta for round %d", rnd)
}

func (a *apiLedger) GetTracer() logic.EvalTracer { return nil }
