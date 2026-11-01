package forkledger

import (
	"fmt"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/protocol"
)

// EmptyFeeSink and EmptyRewardsPool are the special accounts of a chain made
// by NewEmpty.
var (
	EmptyFeeSink     = basics.Address(crypto.Hash([]byte("avmfork empty chain fee sink")))
	EmptyRewardsPool = basics.Address(crypto.Hash([]byte("avmfork empty chain rewards pool")))
)

// NewEmpty starts a new chain with no upstream node, at the current
// consensus version. Its only accounts are the fee sink and the rewards
// pool, each holding the minimum balance; fund other accounts with
// SetBalance.
func NewEmpty(genesisID string, timestamp int64) (*Ledger, error) {
	proto := protocol.ConsensusCurrentVersion
	params, ok := config.Consensus[proto]
	if !ok {
		return nil, fmt.Errorf("unknown consensus version %s", proto)
	}
	min := basics.MicroAlgos{Raw: params.MinBalance}
	balances := map[basics.Address]basics.AccountData{
		EmptyFeeSink:     {Status: basics.NotParticipating, MicroAlgos: min},
		EmptyRewardsPool: {Status: basics.NotParticipating, MicroAlgos: min},
	}
	genesisHash := crypto.Hash([]byte("avmfork empty chain " + genesisID))
	gb := bookkeeping.MakeTimestampedGenesisBalances(balances, EmptyFeeSink, EmptyRewardsPool, timestamp)
	blk, err := bookkeeping.MakeGenesisBlock(proto, gb, genesisID, genesisHash)
	if err != nil {
		return nil, err
	}
	l := &Ledger{
		genesisHash: genesisHash,
		genesisID:   genesisID,
		st:          newState(),
		base:        newState(),
	}
	var ot basics.OverflowTracker
	for addr, ad := range balances {
		lad := ledgercore.ToAccountData(ad)
		l.st.accts[addr] = lad
		l.st.totals.AddAccount(params.RewardUnit, lad, &ot)
	}
	if ot.Overflowed {
		return nil, fmt.Errorf("genesis totals overflow")
	}
	l.st.hdrs[0] = blk.BlockHeader
	return l, nil
}
