package forkledger

import (
	"fmt"
	"sync"
	"time"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/ledger/ledgercore"
)

// recent holds the transaction ids and leases of the blocks just before the
// fork round, so that the duplicate and lease checks see what the network
// would. A transaction confirmed at round r is valid until at most
// r + MaxTxnLife, so only the last MaxTxnLife blocks matter.
type recent struct {
	once   sync.Once
	err    error
	txids  map[transactions.Txid]basics.Round  // last valid round
	leases map[ledgercore.Txlease]basics.Round // expires
}

// recentParallel is how many blocks are fetched at once.
const recentParallel = 16

// LoadRecent fetches the blocks before the fork round and indexes their
// transaction ids and leases. It runs once; later calls return the first
// result. A fork with no upstream has nothing to load.
func (l *Ledger) LoadRecent() error {
	l.recent.once.Do(func() { l.recent.err = l.loadRecent() })
	return l.recent.err
}

func (l *Ledger) recentLoaded() bool {
	l.recentMu.Lock()
	defer l.recentMu.Unlock()
	return l.recentDone
}

func (l *Ledger) loadRecent() error {
	defer func() {
		l.recentMu.Lock()
		l.recentDone = true
		l.recentMu.Unlock()
	}()
	l.recent.txids = map[transactions.Txid]basics.Round{}
	l.recent.leases = map[ledgercore.Txlease]basics.Round{}
	if l.remote == nil {
		return nil
	}
	l.mu.Lock()
	hdr := l.st.hdrs[l.forkRound]
	l.mu.Unlock()
	proto, ok := config.Consensus[hdr.CurrentProtocol]
	if !ok {
		return fmt.Errorf("unknown consensus version %s", hdr.CurrentProtocol)
	}
	first := basics.Round(1)
	if l.forkRound > basics.Round(proto.MaxTxnLife) {
		first = l.forkRound - basics.Round(proto.MaxTxnLife)
	}

	type result struct {
		txids  map[transactions.Txid]basics.Round
		leases map[ledgercore.Txlease]basics.Round
		err    error
	}
	rounds := make(chan basics.Round)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < recentParallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rnd := range rounds {
				res := result{txids: map[transactions.Txid]basics.Round{}, leases: map[ledgercore.Txlease]basics.Round{}}
				var blk bookkeeping.Block
				var err error
				for try := 0; try < 3; try++ {
					if blk, err = l.remote.Block(rnd); err == nil {
						break
					}
					time.Sleep(time.Duration(try+1) * 500 * time.Millisecond)
				}
				if err == nil {
					var payset []transactions.SignedTxnWithAD
					payset, err = blk.DecodePaysetFlat()
					for _, st := range payset {
						txn := st.Txn
						res.txids[txn.ID()] = txn.LastValid
						if txn.Lease != [32]byte{} {
							res.leases[ledgercore.Txlease{Sender: txn.Sender, Lease: txn.Lease}] = txn.LastValid
						}
					}
				}
				if err != nil {
					res.err = fmt.Errorf("block %d: %w", rnd, err)
				}
				results <- res
			}
		}()
	}
	go func() {
		for r := first; r <= l.forkRound; r++ {
			rounds <- r
		}
		close(rounds)
		wg.Wait()
		close(results)
	}()
	var firstErr error
	for res := range results {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		for k, v := range res.txids {
			l.recent.txids[k] = v
		}
		for k, v := range res.leases {
			if v > l.recent.leases[k] {
				l.recent.leases[k] = v
			}
		}
	}
	return firstErr
}
