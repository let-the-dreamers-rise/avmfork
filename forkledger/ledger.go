package forkledger

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/algorand/avm-abi/apps"
	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/ledger/ledgercore"
)

// ErrOldRound is returned when the evaluator asks for state at a round the
// fork no longer holds. The fork keeps only its latest state.
var ErrOldRound = errors.New("avmfork keeps only the latest round's state")

type creatableKey struct {
	idx   basics.CreatableIndex
	ctype basics.CreatableType
}

type creatorEntry struct {
	addr   basics.Address
	exists bool
}

type txidEntry struct {
	lastValid basics.Round
}

type leaseEntry struct {
	expires basics.Round
}

// FetchRecord notes one read the fork made from the remote node, and the
// round the node served it at.
type FetchRecord struct {
	Kind  string // "acct", "asset", "app", "box"
	Key   string
	Round basics.Round
}

// state is everything the fork has read or written, at the fork's latest
// round. A key that is absent from a map has not been read yet and will be
// fetched from the remote node.
type state struct {
	accts    map[basics.Address]ledgercore.AccountData
	assets   map[ledgercore.AccountAsset]ledgercore.AssetResource
	apps     map[ledgercore.AccountApp]ledgercore.AppResource
	kvs      map[string]*[]byte // nil pointer: box known not to exist
	creators map[creatableKey]creatorEntry
	txids    map[transactions.Txid]txidEntry
	leases   map[ledgercore.Txlease]leaseEntry
	hdrs     map[basics.Round]bookkeeping.BlockHeader
	latest   basics.Round
	totals   ledgercore.AccountTotals
}

func newState() *state {
	return &state{
		accts:    map[basics.Address]ledgercore.AccountData{},
		assets:   map[ledgercore.AccountAsset]ledgercore.AssetResource{},
		apps:     map[ledgercore.AccountApp]ledgercore.AppResource{},
		kvs:      map[string]*[]byte{},
		creators: map[creatableKey]creatorEntry{},
		txids:    map[transactions.Txid]txidEntry{},
		leases:   map[ledgercore.Txlease]leaseEntry{},
		hdrs:     map[basics.Round]bookkeeping.BlockHeader{},
	}
}

// clone copies the maps. Values are replaced, never mutated in place, when
// deltas are applied, so sharing the values between copies is safe.
func (s *state) clone() *state {
	c := newState()
	for k, v := range s.accts {
		c.accts[k] = v
	}
	for k, v := range s.assets {
		c.assets[k] = v
	}
	for k, v := range s.apps {
		c.apps[k] = v
	}
	for k, v := range s.kvs {
		c.kvs[k] = v
	}
	for k, v := range s.creators {
		c.creators[k] = v
	}
	for k, v := range s.txids {
		c.txids[k] = v
	}
	for k, v := range s.leases {
		c.leases[k] = v
	}
	for k, v := range s.hdrs {
		c.hdrs[k] = v
	}
	c.latest = s.latest
	c.totals = s.totals
	return c
}

// Ledger implements eval.LedgerForEvaluator over a remote node plus a local
// in-memory overlay. It is safe for concurrent use.
type Ledger struct {
	mu          sync.Mutex
	remote      *Remote // nil for a fork with no upstream (empty chain)
	forkRound   basics.Round
	genesisHash crypto.Digest
	genesisID   string
	st          *state // the fork's state: everything written locally
	base        *state // upstream values as first read; never written to

	// Transaction ids and leases from before the fork; see recent.go.
	recent     recent
	recentMu   sync.Mutex
	recentDone bool

	// OnFetch, if set, is called for every remote read. The differential
	// tester uses it to check that reads were not stale.
	OnFetch func(FetchRecord)
}

// Open forks the chain served by remote at its latest round.
func Open(remote *Remote) (*Ledger, error) {
	rnd, err := remote.LatestRound()
	if err != nil {
		return nil, fmt.Errorf("read node status: %w", err)
	}
	return OpenAt(remote, rnd)
}

// OpenAt forks at rnd. State reads still return the node's latest state;
// only the block header and totals come from rnd. Use a recent round.
func OpenAt(remote *Remote, rnd basics.Round) (*Ledger, error) {
	blk, err := remote.Block(rnd)
	if err != nil {
		return nil, fmt.Errorf("fetch block %d: %w", rnd, err)
	}
	delta, err := remote.Delta(rnd)
	if err != nil {
		return nil, fmt.Errorf("fetch state delta %d (the node must serve /v2/deltas): %w", rnd, err)
	}
	l := &Ledger{
		remote:      remote,
		forkRound:   rnd,
		genesisHash: blk.GenesisHash(),
		genesisID:   blk.GenesisID(),
		st:          newState(),
		base:        newState(),
	}
	l.st.latest = rnd
	l.st.totals = delta.Totals
	l.st.hdrs[rnd] = blk.BlockHeader
	return l, nil
}

// ForkRound is the round the fork was taken at.
func (l *Ledger) ForkRound() basics.Round { return l.forkRound }

// GenesisID of the forked network.
func (l *Ledger) GenesisID() string { return l.genesisID }

// Latest is the fork's latest round.
func (l *Ledger) Latest() basics.Round {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.latest
}

func (l *Ledger) note(kind, key string, rnd basics.Round) {
	if l.OnFetch != nil && l.remote != nil {
		l.OnFetch(FetchRecord{Kind: kind, Key: key, Round: rnd})
	}
}

func (l *Ledger) checkRound(rnd basics.Round) error {
	if rnd != l.st.latest {
		return fmt.Errorf("%w: asked for %d, latest is %d", ErrOldRound, rnd, l.st.latest)
	}
	return nil
}

// ---- eval.LedgerForEvaluator ----

// BlockHdr returns the header of round rnd, fetching rounds at or before the
// fork round from the remote node.
func (l *Ledger) BlockHdr(rnd basics.Round) (bookkeeping.BlockHeader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blockHdrLocked(rnd)
}

func (l *Ledger) blockHdrLocked(rnd basics.Round) (bookkeeping.BlockHeader, error) {
	if h, ok := l.st.hdrs[rnd]; ok {
		return h, nil
	}
	if h, ok := l.base.hdrs[rnd]; ok {
		return h, nil
	}
	if rnd > l.forkRound || l.remote == nil {
		return bookkeeping.BlockHeader{}, fmt.Errorf("no header for round %d", rnd)
	}
	blk, err := l.remote.Block(rnd)
	if err != nil {
		return bookkeeping.BlockHeader{}, err
	}
	l.base.hdrs[rnd] = blk.BlockHeader
	return blk.BlockHeader, nil
}

// GenesisHash of the forked network.
func (l *Ledger) GenesisHash() crypto.Digest { return l.genesisHash }

// GenesisProto returns the consensus parameters of the fork round's protocol.
func (l *Ledger) GenesisProto() config.ConsensusParams {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.st.hdrs[l.forkRound]
	return config.Consensus[h.CurrentProtocol]
}

// CheckDup rejects a transaction id or lease already used on the fork, or
// on the network in the blocks before the fork. A transaction with a lease
// loads those blocks (LoadRecent) if they are not loaded yet; a plain
// transaction id is checked against them only once they are, since loading
// takes a thousand block fetches. The avmfork command loads them in the
// background at startup.
func (l *Ledger) CheckDup(proto config.ConsensusParams, current, firstValid, lastValid basics.Round, txid transactions.Txid, txl ledgercore.Txlease) error {
	hasLease := proto.SupportTransactionLeases && txl.Lease != [32]byte{}
	if hasLease {
		if err := l.LoadRecent(); err != nil {
			return fmt.Errorf("load leases from before the fork: %w", err)
		}
	}
	if l.recentLoaded() {
		if hasLease {
			if exp, ok := l.recent.leases[txl]; ok && current <= exp {
				return ledgercore.MakeLeaseInLedgerError(txid, txl, false)
			}
		}
		if lv, ok := l.recent.txids[txid]; ok && lv >= current {
			return &ledgercore.TransactionInLedgerError{Txid: txid, InBlockEvaluator: false}
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if hasLease {
		if e, ok := l.st.leases[txl]; ok && current <= e.expires {
			return ledgercore.MakeLeaseInLedgerError(txid, txl, false)
		}
	}
	if e, ok := l.st.txids[txid]; ok && e.lastValid >= current {
		return &ledgercore.TransactionInLedgerError{Txid: txid, InBlockEvaluator: false}
	}
	return nil
}

// LookupWithoutRewards returns account data without pending rewards.
func (l *Ledger) LookupWithoutRewards(rnd basics.Round, addr basics.Address) (ledgercore.AccountData, basics.Round, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRound(rnd); err != nil {
		return ledgercore.AccountData{}, 0, err
	}
	ad, err := l.accountLocked(addr)
	return ad, rnd, err
}

func (l *Ledger) accountLocked(addr basics.Address) (ledgercore.AccountData, error) {
	if ad, ok := l.st.accts[addr]; ok {
		return ad, nil
	}
	if ad, ok := l.base.accts[addr]; ok {
		return ad, nil
	}
	if l.remote == nil {
		return ledgercore.AccountData{}, nil
	}
	ad, r, err := l.remote.Account(addr)
	if err != nil {
		return ledgercore.AccountData{}, fmt.Errorf("fetch account %s: %w", addr, err)
	}
	l.note("acct", addr.String(), r)
	l.base.accts[addr] = ad
	return ad, nil
}

// LookupAgreement returns the online data the fork holds for addr.
func (l *Ledger) LookupAgreement(rnd basics.Round, addr basics.Address) (basics.OnlineAccountData, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ad, err := l.accountLocked(addr)
	if err != nil {
		return basics.OnlineAccountData{}, err
	}
	h, err := l.blockHdrLocked(l.st.latest)
	if err != nil {
		return basics.OnlineAccountData{}, err
	}
	proto := config.Consensus[h.CurrentProtocol]
	return ad.OnlineAccountData(proto.RewardUnit, h.RewardsLevel), nil
}

// GetKnockOfflineCandidates returns no candidates. The fork has no
// consensus, so nobody is suspended for being absent.
func (l *Ledger) GetKnockOfflineCandidates(basics.Round, config.ConsensusParams) (map[basics.Address]basics.OnlineAccountData, error) {
	return nil, nil
}

// LookupAsset returns addr's holding of, and params for, asset aidx.
func (l *Ledger) LookupAsset(rnd basics.Round, addr basics.Address, aidx basics.AssetIndex) (ledgercore.AssetResource, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRound(rnd); err != nil {
		return ledgercore.AssetResource{}, err
	}
	return l.assetLocked(addr, aidx)
}

func (l *Ledger) assetLocked(addr basics.Address, aidx basics.AssetIndex) (ledgercore.AssetResource, error) {
	key := ledgercore.AccountAsset{Address: addr, Asset: aidx}
	if r, ok := l.st.assets[key]; ok {
		return r, nil
	}
	if r, ok := l.base.assets[key]; ok {
		return r, nil
	}
	if l.remote == nil {
		return ledgercore.AssetResource{}, nil
	}
	r, fr, err := l.remote.AccountAsset(addr, aidx)
	if err != nil {
		return ledgercore.AssetResource{}, fmt.Errorf("fetch asset %d of %s: %w", aidx, addr, err)
	}
	l.note("asset", fmt.Sprintf("%s/%d", addr, aidx), fr)
	l.base.assets[key] = r
	return r, nil
}

// LookupApplication returns addr's local state in, and params of, app aidx.
func (l *Ledger) LookupApplication(rnd basics.Round, addr basics.Address, aidx basics.AppIndex) (ledgercore.AppResource, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRound(rnd); err != nil {
		return ledgercore.AppResource{}, err
	}
	return l.appLocked(addr, aidx)
}

func (l *Ledger) appLocked(addr basics.Address, aidx basics.AppIndex) (ledgercore.AppResource, error) {
	key := ledgercore.AccountApp{Address: addr, App: aidx}
	if r, ok := l.st.apps[key]; ok {
		return r, nil
	}
	if r, ok := l.base.apps[key]; ok {
		return r, nil
	}
	if l.remote == nil {
		return ledgercore.AppResource{}, nil
	}
	r, fr, err := l.remote.AccountApp(addr, aidx)
	if err != nil {
		return ledgercore.AppResource{}, fmt.Errorf("fetch app %d of %s: %w", aidx, addr, err)
	}
	l.note("app", fmt.Sprintf("%s/%d", addr, aidx), fr)
	l.base.apps[key] = r
	return r, nil
}

// LookupKv returns a box value, or nil if the box does not exist.
func (l *Ledger) LookupKv(rnd basics.Round, key string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRound(rnd); err != nil {
		return nil, err
	}
	for _, m := range []map[string]*[]byte{l.st.kvs, l.base.kvs} {
		if v, ok := m[key]; ok {
			if v == nil {
				return nil, nil
			}
			return *v, nil
		}
	}
	if l.remote == nil {
		return nil, nil
	}
	app, name, err := apps.SplitBoxKey(key)
	if err != nil {
		return nil, err
	}
	v, fr, err := l.remote.Box(basics.AppIndex(app), name)
	if err != nil {
		return nil, fmt.Errorf("fetch box of app %d: %w", app, err)
	}
	l.note("box", key, fr)
	if v == nil {
		l.base.kvs[key] = nil
	} else {
		l.base.kvs[key] = &v
	}
	return v, nil
}

// GetCreatorForRound returns the creator of an asset or app.
func (l *Ledger) GetCreatorForRound(rnd basics.Round, cidx basics.CreatableIndex, ctype basics.CreatableType) (basics.Address, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRound(rnd); err != nil {
		return basics.Address{}, false, err
	}
	return l.creatorLocked(cidx, ctype)
}

func (l *Ledger) creatorLocked(cidx basics.CreatableIndex, ctype basics.CreatableType) (basics.Address, bool, error) {
	key := creatableKey{cidx, ctype}
	if c, ok := l.st.creators[key]; ok {
		return c.addr, c.exists, nil
	}
	if c, ok := l.base.creators[key]; ok {
		return c.addr, c.exists, nil
	}
	if l.remote == nil {
		return basics.Address{}, false, nil
	}
	var addr basics.Address
	var ok bool
	var err error
	switch ctype {
	case basics.AssetCreatable:
		addr, ok, err = l.remote.AssetCreator(basics.AssetIndex(cidx))
	case basics.AppCreatable:
		addr, ok, err = l.remote.AppCreator(basics.AppIndex(cidx))
	default:
		return basics.Address{}, false, fmt.Errorf("unknown creatable type %v", ctype)
	}
	if err != nil {
		return basics.Address{}, false, err
	}
	l.base.creators[key] = creatorEntry{addr: addr, exists: ok}
	return addr, ok, nil
}

// GetStateProofVerificationContext is not supported: the fork does not
// track state proofs.
func (l *Ledger) GetStateProofVerificationContext(basics.Round) (*ledgercore.StateProofVerificationContext, error) {
	return nil, errors.New("avmfork does not support state proof transactions")
}

// OnlineCirculation returns the remote node's current online stake.
func (l *Ledger) OnlineCirculation(basics.Round, basics.Round) (basics.MicroAlgos, error) {
	if l.remote == nil {
		return basics.MicroAlgos{}, nil
	}
	return l.remote.OnlineMoney()
}

// LatestTotals returns the totals after the latest round.
func (l *Ledger) LatestTotals() (basics.Round, ledgercore.AccountTotals, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.latest, l.st.totals, nil
}

// VotersForStateProof returns no voters; the fork has no state proofs.
func (l *Ledger) VotersForStateProof(basics.Round) (*ledgercore.VotersForRound, error) {
	return nil, nil
}

// FlushCaches is a no-op.
func (l *Ledger) FlushCaches() {}

// ---- commit, cheats, snapshots ----

// Commit applies the delta of the block evaluated on top of the latest
// round, and makes that block the latest.
func (l *Ledger) Commit(blk bookkeeping.Block, delta ledgercore.StateDelta) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if blk.Round() != l.st.latest+1 {
		return fmt.Errorf("commit round %d on top of %d", blk.Round(), l.st.latest)
	}
	l.applyLocked(delta)
	l.st.hdrs[blk.Round()] = blk.BlockHeader
	l.st.latest = blk.Round()
	l.st.totals = delta.Totals
	return nil
}

func (l *Ledger) applyLocked(d ledgercore.StateDelta) {
	for _, br := range d.Accts.Accts {
		l.st.accts[br.Addr] = br.AccountData
	}
	for _, rec := range d.Accts.AssetResources {
		key := ledgercore.AccountAsset{Address: rec.Addr, Asset: rec.Aidx}
		r := l.st.assets[key]
		if rec.Params.Deleted {
			r.AssetParams = nil
		} else if rec.Params.Params != nil {
			p := *rec.Params.Params
			r.AssetParams = &p
		}
		if rec.Holding.Deleted {
			r.AssetHolding = nil
		} else if rec.Holding.Holding != nil {
			h := *rec.Holding.Holding
			r.AssetHolding = &h
		}
		l.st.assets[key] = r
	}
	for _, rec := range d.Accts.AppResources {
		key := ledgercore.AccountApp{Address: rec.Addr, App: rec.Aidx}
		r := l.st.apps[key]
		if rec.Params.Deleted {
			r.AppParams = nil
		} else if rec.Params.Params != nil {
			p := *rec.Params.Params
			r.AppParams = &p
		}
		if rec.State.Deleted {
			r.AppLocalState = nil
		} else if rec.State.LocalState != nil {
			s := *rec.State.LocalState
			r.AppLocalState = &s
		}
		l.st.apps[key] = r
	}
	for k, v := range d.KvMods {
		if v.Data == nil {
			l.st.kvs[k] = nil
		} else {
			b := append([]byte{}, v.Data...)
			l.st.kvs[k] = &b
		}
	}
	for idx, mc := range d.Creatables {
		l.st.creators[creatableKey{idx, mc.Ctype}] = creatorEntry{addr: mc.Creator, exists: mc.Created}
	}
	for txid, inc := range d.Txids {
		l.st.txids[txid] = txidEntry{lastValid: inc.LastValid}
	}
	for lease, exp := range d.Txleases {
		l.st.leases[lease] = leaseEntry{expires: exp}
	}
}

// Snapshot is a saved copy of the fork's state.
type Snapshot struct{ st *state }

// Snapshot saves the current state.
func (l *Ledger) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Snapshot{st: l.st.clone()}
}

// Revert restores a saved state. Values already read from the remote node
// are kept, so a test rerun after a revert sees the same upstream state.
func (l *Ledger) Revert(s Snapshot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.st = s.st.clone()
}

// Account returns account data at the latest round, fetching it if needed.
func (l *Ledger) Account(addr basics.Address) (ledgercore.AccountData, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accountLocked(addr)
}

// Creator returns the creator of an asset or app.
func (l *Ledger) Creator(cidx basics.CreatableIndex, ctype basics.CreatableType) (basics.Address, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.creatorLocked(cidx, ctype)
}

// Box returns a box value, or nil if it does not exist.
func (l *Ledger) Box(app basics.AppIndex, name string) ([]byte, error) {
	return l.LookupKv(l.Latest(), apps.MakeBoxKey(uint64(app), name))
}

// Remote returns the upstream node, or nil.
func (l *Ledger) Remote() *Remote { return l.remote }

// LocalResources returns the asset and app resources of addr that the fork
// has read or changed, as the evaluator sees them. They override what the
// remote node reports now.
func (l *Ledger) LocalResources(addr basics.Address) (map[basics.AssetIndex]ledgercore.AssetResource, map[basics.AppIndex]ledgercore.AppResource) {
	l.mu.Lock()
	defer l.mu.Unlock()
	as := map[basics.AssetIndex]ledgercore.AssetResource{}
	ap := map[basics.AppIndex]ledgercore.AppResource{}
	for _, st := range []*state{l.base, l.st} {
		for k, v := range st.assets {
			if k.Address == addr {
				as[k.Asset] = v
			}
		}
		for k, v := range st.apps {
			if k.Address == addr {
				ap[k.App] = v
			}
		}
	}
	return as, ap
}

// LocalBoxes returns the box keys with the given key prefix that the fork
// has read or changed, mapped to whether the box exists.
func (l *Ledger) LocalBoxes(prefix string) map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]bool{}
	for _, st := range []*state{l.base, l.st} {
		for k, v := range st.kvs {
			if strings.HasPrefix(k, prefix) {
				out[k] = v != nil
			}
		}
	}
	return out
}
