// Package forkledger is a ledger for go-algorand's block evaluator that reads
// state it has not seen yet from a live algod node and keeps every change
// it makes in memory.
package forkledger

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/algorand/go-algorand/daemon/algod/api/client"
	v2 "github.com/algorand/go-algorand/daemon/algod/api/server/v2"
	"github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/model"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/rpcs"
)

// Remote reads state from an algod REST endpoint.
//
// algod only serves account state at its latest round. Every read reports
// the round it was served at, so callers can tell when the node has moved
// past the round they forked at.
type Remote struct {
	rc client.RestClient
}

// NewRemote returns a Remote for the algod at rawURL.
func NewRemote(rawURL, token string) (*Remote, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("bad algod url %q: %w", rawURL, err)
	}
	return &Remote{rc: client.MakeRestClient(*u, token)}, nil
}

func isNotFound(err error) bool {
	var he client.HTTPError
	if errors.As(err, &he) {
		return he.StatusCode == http.StatusNotFound
	}
	return false
}

// LatestRound is the node's last committed round.
func (r *Remote) LatestRound() (basics.Round, error) {
	st, err := r.rc.Status()
	if err != nil {
		return 0, err
	}
	return st.LastRound, nil
}

// Block returns the full block for rnd.
func (r *Remote) Block(rnd basics.Round) (bookkeeping.Block, error) {
	raw, err := r.rc.RawBlock(rnd)
	if err != nil {
		return bookkeeping.Block{}, err
	}
	var bc rpcs.EncodedBlockCert
	if err := protocol.Decode(raw, &bc); err != nil {
		return bookkeeping.Block{}, fmt.Errorf("decode block %d: %w", rnd, err)
	}
	return bc.Block, nil
}

// Delta returns the state delta the node recorded for rnd.
func (r *Remote) Delta(rnd basics.Round) (ledgercore.StateDelta, error) {
	return r.rc.GetLedgerStateDelta(rnd)
}

// OnlineMoney returns the node's current online stake.
func (r *Remote) OnlineMoney() (basics.MicroAlgos, error) {
	s, err := r.rc.LedgerSupply()
	if err != nil {
		return basics.MicroAlgos{}, err
	}
	return basics.MicroAlgos{Raw: s.OnlineMoney}, nil
}

// Account returns the base account data (balances, totals, keys) for addr,
// without its resources.
func (r *Remote) Account(addr basics.Address) (ledgercore.AccountData, basics.Round, error) {
	m, err := r.rc.AccountInformation(addr.String(), false)
	if err != nil {
		return ledgercore.AccountData{}, 0, err
	}
	ad, err := accountFromModel(&m)
	return ad, m.Round, err
}

func accountFromModel(m *model.Account) (ledgercore.AccountData, error) {
	// Only the base data is wanted. Strip any resource lists so the
	// conversion does not count them; the totals come from the node.
	base := *m
	base.Assets, base.CreatedAssets, base.AppsLocalState, base.CreatedApps = nil, nil, nil, nil
	// The ledger stores the balance without pending rewards.
	base.Amount = m.AmountWithoutPendingRewards
	bad, err := v2.AccountToAccountData(&base)
	if err != nil {
		return ledgercore.AccountData{}, err
	}
	ad := ledgercore.ToAccountData(bad)
	ad.TotalAppParams = m.TotalCreatedApps
	ad.TotalAppLocalStates = m.TotalAppsOptedIn
	ad.TotalAssetParams = m.TotalCreatedAssets
	ad.TotalAssets = m.TotalAssetsOptedIn
	return ad, nil
}

// AccountAsset returns addr's holding of, and params for, asset aidx.
func (r *Remote) AccountAsset(addr basics.Address, aidx basics.AssetIndex) (ledgercore.AssetResource, basics.Round, error) {
	resp, err := r.rc.AccountAssetInformation(addr.String(), aidx)
	if err != nil {
		if isNotFound(err) {
			return ledgercore.AssetResource{}, 0, nil
		}
		return ledgercore.AssetResource{}, 0, err
	}
	var res ledgercore.AssetResource
	if resp.AssetHolding != nil {
		h := basics.AssetHolding{Amount: resp.AssetHolding.Amount, Frozen: resp.AssetHolding.IsFrozen}
		res.AssetHolding = &h
	}
	if resp.CreatedAsset != nil {
		p, err := assetParamsFromModel(aidx, resp.CreatedAsset)
		if err != nil {
			return ledgercore.AssetResource{}, 0, err
		}
		res.AssetParams = &p
	}
	return res, resp.Round, nil
}

func assetParamsFromModel(aidx basics.AssetIndex, mp *model.AssetParams) (basics.AssetParams, error) {
	// Reuse the node's own conversion by wrapping the params in an account.
	acct := model.Account{Status: "Offline", CreatedAssets: &[]model.Asset{{Index: aidx, Params: mp}}}
	bad, err := v2.AccountToAccountData(&acct)
	if err != nil {
		return basics.AssetParams{}, err
	}
	p := bad.AssetParams[aidx]
	// The plain-string fields lose non-UTF-8 bytes in JSON; the b64 forms don't.
	if mp.NameB64 != nil {
		p.AssetName = string(*mp.NameB64)
	}
	if mp.UnitNameB64 != nil {
		p.UnitName = string(*mp.UnitNameB64)
	}
	if mp.UrlB64 != nil {
		p.URL = string(*mp.UrlB64)
	}
	return p, nil
}

// AccountApp returns addr's local state in, and params of, app aidx.
func (r *Remote) AccountApp(addr basics.Address, aidx basics.AppIndex) (ledgercore.AppResource, basics.Round, error) {
	resp, err := r.rc.AccountApplicationInformation(addr.String(), aidx)
	if err != nil {
		if isNotFound(err) {
			return ledgercore.AppResource{}, 0, nil
		}
		return ledgercore.AppResource{}, 0, err
	}
	var res ledgercore.AppResource
	if resp.AppLocalState != nil {
		acct := model.Account{Status: "Offline", AppsLocalState: &[]model.ApplicationLocalState{*resp.AppLocalState}}
		bad, err := v2.AccountToAccountData(&acct)
		if err != nil {
			return ledgercore.AppResource{}, 0, err
		}
		ls := bad.AppLocalStates[aidx]
		res.AppLocalState = &ls
	}
	if resp.CreatedApp != nil {
		p, err := v2.ApplicationParamsToAppParams(resp.CreatedApp)
		if err != nil {
			return ledgercore.AppResource{}, 0, err
		}
		res.AppParams = &p
	}
	return res, resp.Round, nil
}

// Box returns the value of box name in app, or nil if it does not exist.
func (r *Remote) Box(app basics.AppIndex, name string) ([]byte, basics.Round, error) {
	resp, err := r.rc.GetApplicationBoxByName(app, "b64:"+base64.StdEncoding.EncodeToString([]byte(name)))
	if err != nil {
		if isNotFound(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	v := resp.Value
	if v == nil {
		v = []byte{}
	}
	return v, resp.Round, nil
}

// AssetCreator returns the creator of asset aidx, if it exists.
func (r *Remote) AssetCreator(aidx basics.AssetIndex) (basics.Address, bool, error) {
	a, err := r.rc.AssetInformation(aidx)
	if err != nil {
		if isNotFound(err) {
			return basics.Address{}, false, nil
		}
		return basics.Address{}, false, err
	}
	if a.Params == nil {
		return basics.Address{}, false, nil
	}
	addr, err := basics.UnmarshalChecksumAddress(a.Params.Creator)
	return addr, err == nil, err
}

// AppCreator returns the creator of app aidx, if it exists.
func (r *Remote) AppCreator(aidx basics.AppIndex) (basics.Address, bool, error) {
	a, err := r.rc.ApplicationInformation(aidx)
	if err != nil {
		if isNotFound(err) {
			return basics.Address{}, false, nil
		}
		return basics.Address{}, false, err
	}
	if a.Params == nil {
		return basics.Address{}, false, nil
	}
	addr, err := basics.UnmarshalChecksumAddress(a.Params.Creator)
	return addr, err == nil, err
}

// Simulate posts a msgpack-encoded simulate request and returns the raw
// msgpack response.
func (r *Remote) Simulate(req []byte) ([]byte, error) {
	return r.rc.RawSimulateRawTransaction(req)
}

// FullAccount returns addr's account with all of its resources, as algod's
// /v2/accounts endpoint serves it.
func (r *Remote) FullAccount(addr basics.Address) (basics.AccountData, basics.Round, error) {
	m, err := r.rc.AccountInformation(addr.String(), true)
	if err != nil {
		return basics.AccountData{}, 0, err
	}
	m.Amount = m.AmountWithoutPendingRewards
	ad, err := v2.AccountToAccountData(&m)
	if err != nil {
		return basics.AccountData{}, 0, err
	}
	if m.CreatedAssets != nil {
		for _, ca := range *m.CreatedAssets {
			if ca.Params == nil {
				continue
			}
			p, err := assetParamsFromModel(ca.Index, ca.Params)
			if err != nil {
				return basics.AccountData{}, 0, err
			}
			ad.AssetParams[ca.Index] = p
		}
	}
	return ad, m.Round, nil
}

// BoxNames lists every box name of app.
func (r *Remote) BoxNames(app basics.AppIndex) ([]string, error) {
	var names []string
	next := ""
	for {
		resp, err := r.rc.ApplicationBoxesPage(app, 10000, next, "", false, 0)
		if err != nil {
			if isNotFound(err) {
				return names, nil
			}
			return nil, err
		}
		for _, b := range resp.Boxes {
			names = append(names, string(b.Name))
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			return names, nil
		}
		next = *resp.NextToken
	}
}
