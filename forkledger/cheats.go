package forkledger

import (
	"errors"
	"fmt"

	"github.com/algorand/avm-abi/apps"
	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/ledger/ledgercore"
)

// These methods change state outside of any transaction. Each keeps the
// counters that the evaluator checks (money totals, holding and box counts)
// consistent, so later blocks evaluate normally.

func (l *Ledger) rewardUnitLocked() uint64 {
	h := l.st.hdrs[l.st.latest]
	return config.Consensus[h.CurrentProtocol].RewardUnit
}

// SetBalance sets addr's balance and updates the money totals.
func (l *Ledger) SetBalance(addr basics.Address, amt basics.MicroAlgos) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	old, err := l.accountLocked(addr)
	if err != nil {
		return err
	}
	nu := old
	nu.MicroAlgos = amt
	var ot basics.OverflowTracker
	unit := l.rewardUnitLocked()
	t := l.st.totals
	t.DelAccount(unit, old, &ot)
	t.AddAccount(unit, nu, &ot)
	if ot.Overflowed {
		return errors.New("balance change overflows the money totals")
	}
	l.st.totals = t
	l.st.accts[addr] = nu
	return nil
}

// SetAssetHolding gives addr a holding of asset aidx, opting it in if
// needed.
func (l *Ledger) SetAssetHolding(addr basics.Address, aidx basics.AssetIndex, h basics.AssetHolding) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok, err := l.creatorLocked(basics.CreatableIndex(aidx), basics.AssetCreatable); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("asset %d does not exist", aidx)
	}
	r, err := l.assetLocked(addr, aidx)
	if err != nil {
		return err
	}
	if r.AssetHolding == nil {
		ad, err := l.accountLocked(addr)
		if err != nil {
			return err
		}
		ad.TotalAssets++
		l.st.accts[addr] = ad
	}
	hc := h
	r.AssetHolding = &hc
	l.st.assets[ledgercore.AccountAsset{Address: addr, Asset: aidx}] = r
	return nil
}

// SetBoxValue writes (or, if !exists, deletes) a box and updates the app
// account's box counters.
func (l *Ledger) SetBoxValue(app basics.AppIndex, name string, value []byte, exists bool) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("box names are 1 to 64 bytes")
	}
	if exists && len(value) > 32768 {
		return errors.New("box values are at most 32768 bytes")
	}
	old, err := l.Box(app, name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	appAddr := app.Address()
	ad, err := l.accountLocked(appAddr)
	if err != nil {
		return err
	}
	if old != nil {
		ad.TotalBoxes--
		ad.TotalBoxBytes -= uint64(len(name) + len(old))
	}
	key := apps.MakeBoxKey(uint64(app), name)
	if exists {
		ad.TotalBoxes++
		ad.TotalBoxBytes += uint64(len(name) + len(value))
		v := append([]byte{}, value...)
		l.st.kvs[key] = &v
	} else {
		l.st.kvs[key] = nil
	}
	l.st.accts[appAddr] = ad
	return nil
}

// SetAppState writes or deletes (tv == nil) one key of an app's global
// state, or of holder's local state if holder is set. The key must fit the
// app's declared schema.
func (l *Ledger) SetAppState(app basics.AppIndex, holder *basics.Address, key string, tv *basics.TealValue) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	creator, ok, err := l.creatorLocked(basics.CreatableIndex(app), basics.AppCreatable)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %d does not exist", app)
	}
	cr, err := l.appLocked(creator, app)
	if err != nil {
		return err
	}
	if cr.AppParams == nil {
		return fmt.Errorf("app %d has no params at its creator", app)
	}
	if holder == nil {
		p := *cr.AppParams
		kv, err := editKV(p.GlobalState, p.GlobalStateSchema, key, tv)
		if err != nil {
			return err
		}
		p.GlobalState = kv
		cr.AppParams = &p
		l.st.apps[ledgercore.AccountApp{Address: creator, App: app}] = cr
		return nil
	}
	hr, err := l.appLocked(*holder, app)
	if err != nil {
		return err
	}
	if hr.AppLocalState == nil {
		return fmt.Errorf("%s is not opted in to app %d", holder, app)
	}
	s := *hr.AppLocalState
	kv, err := editKV(s.KeyValue, s.Schema, key, tv)
	if err != nil {
		return err
	}
	s.KeyValue = kv
	hr.AppLocalState = &s
	l.st.apps[ledgercore.AccountApp{Address: *holder, App: app}] = hr
	return nil
}

func editKV(in basics.TealKeyValue, schema basics.StateSchema, key string, tv *basics.TealValue) (basics.TealKeyValue, error) {
	out := basics.TealKeyValue{}
	for k, v := range in {
		out[k] = v
	}
	if tv == nil {
		delete(out, key)
	} else {
		out[key] = *tv
	}
	var used basics.StateSchema
	for _, v := range out {
		if v.Type == basics.TealUintType {
			used.NumUint++
		} else {
			used.NumByteSlice++
		}
	}
	if used.NumUint > schema.NumUint || used.NumByteSlice > schema.NumByteSlice {
		return nil, fmt.Errorf("state would need %d ints and %d byte slices; schema allows %d and %d",
			used.NumUint, used.NumByteSlice, schema.NumUint, schema.NumByteSlice)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
