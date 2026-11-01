// Package difftest checks avmfork against a real algod node.
//
// It takes transaction groups from recent blocks of the upstream network,
// changes each group's note so it is new (and not a duplicate), drops the
// signatures, and simulates the same request on the upstream node and on a
// fresh avmfork fork of that node. The two simulate responses must be
// identical.
//
// Both sides run go-algorand's evaluator, so what this tests is avmfork's
// own code: the ledger that reads remote state, the block header it builds,
// and the API layer. A difference means avmfork read some state wrongly.
//
// algod only serves its latest state, and the network moves on every few
// seconds. A comparison is only meaningful if neither side read state that
// changed between the two runs. The tester records every remote read and
// checks it against the node's state deltas for the rounds in between; if
// a read key was touched and the responses differ, the case is reported as
// "stale" instead of being counted as a mismatch.
package difftest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/algorand/go-algorand/crypto"
	v2 "github.com/algorand/go-algorand/daemon/algod/api/server/v2"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	upstreamsim "github.com/algorand/go-algorand/ledger/simulation"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/chain"
	"github.com/let-the-dreamers-rise/avmfork/forkledger"
	"github.com/let-the-dreamers-rise/avmfork/server"
)

// Outcome of one comparison.
type Outcome string

// Outcomes.
const (
	Match    Outcome = "match"
	Mismatch Outcome = "mismatch"
	Stale    Outcome = "stale"   // state moved between the runs; not counted
	Skipped  Outcome = "skipped" // could not be prepared (e.g. state proof txn)
	Errored  Outcome = "error"   // a request failed outside evaluation

	// KnownLimit is a difference avmfork documents: the replayed
	// transaction carries a lease that its original copy still holds on
	// the network, and avmfork does not know leases from before the fork.
	KnownLimit Outcome = "known-limit"
)

// Case is the result for one transaction group.
type Case struct {
	SourceRound basics.Round `json:"source-round"`
	FirstTxID   string       `json:"first-txid"`
	Size        int          `json:"group-size"`
	Types       []string     `json:"types"`
	ForkRound   basics.Round `json:"fork-round"`
	Outcome     Outcome      `json:"outcome"`
	Detail      string       `json:"detail,omitempty"`
	Failed      bool         `json:"failed-on-both,omitempty"`
	AppCalls    int          `json:"app-calls"`
	Fetches     int          `json:"remote-reads"`

	// Commit is the result of also sending the group to the fork as real
	// transactions (impersonating the senders) and comparing what the
	// committed block records with upstream's simulate result. Empty if
	// the commit check was not run or the simulate check did not match.
	Commit       Outcome `json:"commit,omitempty"`
	CommitDetail string  `json:"commit-detail,omitempty"`

	// Upstream and Fork are kept only for mismatches.
	Upstream json.RawMessage `json:"upstream,omitempty"`
	Fork     json.RawMessage `json:"fork,omitempty"`
}

// Tester runs comparisons against one upstream node.
type Tester struct {
	URL    string
	Token  string
	Remote *forkledger.Remote
	Client *http.Client

	// CheckCommit also runs each matching group through the fork's normal
	// submit path; see Case.Commit.
	CheckCommit bool

	deltaMu sync.Mutex
	deltas  map[basics.Round]touched
}

// New returns a tester for the algod at url.
func New(url, token string) (*Tester, error) {
	r, err := forkledger.NewRemote(url, token)
	if err != nil {
		return nil, err
	}
	return &Tester{URL: strings.TrimRight(url, "/"), Token: token, Remote: r, Client: http.DefaultClient, deltas: map[basics.Round]touched{}}, nil
}

// touched is the set of state keys a round changed, in the same key format
// the fork ledger reports reads in.
type touched map[string]bool

func touchedKeys(d ledgercore.StateDelta) touched {
	t := touched{}
	for _, br := range d.Accts.Accts {
		t["acct:"+br.Addr.String()] = true
	}
	for _, r := range d.Accts.AssetResources {
		t[fmt.Sprintf("asset:%s/%d", r.Addr, r.Aidx)] = true
	}
	for _, r := range d.Accts.AppResources {
		t[fmt.Sprintf("app:%s/%d", r.Addr, r.Aidx)] = true
	}
	for k := range d.KvMods {
		t["box:"+k] = true
	}
	return t
}

func (t *Tester) roundTouched(r basics.Round) (touched, error) {
	t.deltaMu.Lock()
	if tk, ok := t.deltas[r]; ok {
		t.deltaMu.Unlock()
		return tk, nil
	}
	t.deltaMu.Unlock()
	d, err := t.Remote.Delta(r)
	if err != nil {
		return nil, err
	}
	tk := touchedKeys(d)
	t.deltaMu.Lock()
	t.deltas[r] = tk
	t.deltaMu.Unlock()
	return tk, nil
}

// Groups splits a block's payset into transaction groups.
func Groups(blk bookkeeping.Block) ([][]transactions.SignedTxn, error) {
	paysetGroups, err := blk.DecodePaysetGroups()
	if err != nil {
		return nil, err
	}
	out := make([][]transactions.SignedTxn, len(paysetGroups))
	for i, g := range paysetGroups {
		for _, s := range g {
			out[i] = append(out[i], s.SignedTxn)
		}
	}
	return out, nil
}

// Prepare makes a fresh copy of a confirmed group: each note gets a marker
// so the transaction ids are new, single-signature authorizations are
// removed (simulate fills them in), logic signatures are kept, and the group
// id is recomputed.
func Prepare(group []transactions.SignedTxn, marker []byte) ([]transactions.SignedTxn, error) {
	out := make([]transactions.SignedTxn, len(group))
	for i, s := range group {
		if s.Txn.Type == protocol.StateProofTx || s.Txn.Type == protocol.HeartbeatTx {
			return nil, fmt.Errorf("%s transactions are not replayable", s.Txn.Type)
		}
		if len(s.Txn.Note)+len(marker) > 1024 {
			return nil, fmt.Errorf("note too long to mark")
		}
		c := s
		c.Txn.Note = append(append([]byte{}, s.Txn.Note...), marker...)
		c.Sig = crypto.Signature{}
		c.Msig = crypto.MultisigSig{}
		c.PQsig = transactions.PQSig{}
		c.Txn.Group = crypto.Digest{}
		out[i] = c
	}
	if len(out) > 1 {
		var tg transactions.TxGroup
		for _, s := range out {
			tg.TxGroupHashes = append(tg.TxGroupHashes, crypto.Digest(s.Txn.ID()))
		}
		gid := crypto.HashObj(tg)
		for i := range out {
			out[i].Txn.Group = gid
		}
	}
	return out, nil
}

func (t *Tester) post(url, token string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, url+"/v2/transactions/simulate?format=json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/msgpack")
	if token != "" {
		req.Header.Set("X-Algo-API-Token", token)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// Compare simulates group on the upstream node and on a fresh fork, and
// classifies the result.
func (t *Tester) Compare(group []transactions.SignedTxn, src basics.Round) Case {
	c := Case{SourceRound: src, Size: len(group), FirstTxID: group[0].ID().String()}
	for _, s := range group {
		c.Types = append(c.Types, string(s.Txn.Type))
		if s.Txn.Type == protocol.ApplicationCallTx {
			c.AppCalls++
		}
	}
	req := v2.PreEncodedSimulateRequest{
		TxnGroups:             []v2.PreEncodedSimulateRequestTransactionGroup{{Txns: group}},
		AllowEmptySignatures:  true,
		AllowUnnamedResources: true,
		FixSigners:            true,
		ExecTraceConfig:       execTraceAll(),
	}
	body := protocol.EncodeReflect(&req)

	// Upstream first; then fork at the round it ran on.
	upBody, upCode, err := t.post(t.URL, t.Token, body)
	if err != nil {
		c.Outcome, c.Detail = Errored, "upstream: "+err.Error()
		return c
	}
	var upLast struct {
		LastRound basics.Round `json:"last-round"`
	}
	if upCode == http.StatusOK {
		if err := json.Unmarshal(upBody, &upLast); err != nil {
			c.Outcome, c.Detail = Errored, "upstream decode: "+err.Error()
			return c
		}
	} else {
		r, err := t.Remote.LatestRound()
		if err != nil {
			c.Outcome, c.Detail = Errored, err.Error()
			return c
		}
		upLast.LastRound = r
	}

	l, err := forkledger.OpenAt(t.Remote, upLast.LastRound)
	if err != nil {
		c.Outcome, c.Detail = Errored, "fork: "+err.Error()
		return c
	}
	var mu sync.Mutex
	var reads []forkledger.FetchRecord
	l.OnFetch = func(f forkledger.FetchRecord) {
		mu.Lock()
		reads = append(reads, f)
		mu.Unlock()
	}
	c.ForkRound = l.ForkRound()
	srv := httptest.NewServer(server.New(server.NewNode(chain.New(l)), server.Options{}))
	defer srv.Close()
	forkBody, forkCode, err := t.post(srv.URL, "", body)
	if err != nil {
		c.Outcome, c.Detail = Errored, "fork: "+err.Error()
		return c
	}
	c.Fetches = len(reads)

	t.judge(&c, upCode, forkCode, upBody, forkBody)
	if c.Outcome == Match && t.CheckCommit && upCode == http.StatusOK {
		t.commit(&c, srv.URL, group, upBody)
	}
	if c.Outcome != Mismatch {
		return c
	}
	// A difference only counts if neither side could have seen different
	// state: any read served after the upstream's round must not have
	// changed since.
	for _, f := range reads {
		for r := upLast.LastRound + 1; r <= f.Round; r++ {
			tk, err := t.roundTouched(r)
			if err != nil {
				c.Outcome, c.Detail = Errored, "delta: "+err.Error()
				return c
			}
			if tk[f.Kind+":"+f.Key] {
				c.Outcome, c.Detail = Stale, fmt.Sprintf("%s %s changed in round %d", f.Kind, f.Key, r)
				return c
			}
		}
	}
	return c
}

// judge compares the two responses and sets c's outcome.
func (t *Tester) judge(c *Case, upCode, forkCode int, upBody, forkBody []byte) {
	if upCode != forkCode {
		c.Outcome = Mismatch
		c.Detail = fmt.Sprintf("HTTP %d upstream vs %d fork", upCode, forkCode)
		c.Upstream, c.Fork = upBody, forkBody
		return
	}
	if upCode != http.StatusOK {
		// Both rejected the request: compare the messages.
		if normalizeErr(upBody) == normalizeErr(forkBody) {
			c.Outcome, c.Failed = Match, true
		} else {
			c.Outcome, c.Detail = Mismatch, "different request errors"
			c.Upstream, c.Fork = upBody, forkBody
		}
		return
	}
	var a, b map[string]any
	if err := json.Unmarshal(upBody, &a); err != nil {
		c.Outcome, c.Detail = Errored, err.Error()
		return
	}
	if err := json.Unmarshal(forkBody, &b); err != nil {
		c.Outcome, c.Detail = Errored, err.Error()
		return
	}
	delete(a, "last-round")
	delete(b, "last-round")
	sortUnordered(a)
	sortUnordered(b)
	if groups, ok := a["txn-groups"].([]any); ok && len(groups) > 0 {
		if g, ok := groups[0].(map[string]any); ok && g["failure-message"] != nil {
			c.Failed = true
		}
	}
	if reflect.DeepEqual(a, b) {
		c.Outcome = Match
		return
	}
	if upCode == http.StatusOK && isLeaseFromBeforeFork(a, b) {
		c.Outcome, c.Detail = KnownLimit, "lease taken on the network before the fork round"
		return
	}
	c.Outcome = Mismatch
	c.Detail = firstDiff("", a, b)
	c.Upstream, c.Fork = upBody, forkBody
}

// commit sends group to the fork at forkURL through POST /v2/transactions,
// with every sender impersonated, and compares each committed
// transaction's pending-transaction record with the txn-result that the
// upstream's simulate returned for it. This covers the code a user's
// transactions go through: block building, evaluation with real
// validation, commit, and the confirmed-transaction API.
func (t *Tester) commit(c *Case, forkURL string, group []transactions.SignedTxn, upBody []byte) {
	var up map[string]any
	if err := json.Unmarshal(upBody, &up); err != nil {
		c.Commit, c.CommitDetail = Errored, err.Error()
		return
	}
	g, _ := firstGroup(up)
	upFailed := g["failure-message"] != nil
	results, _ := g["txn-results"].([]any)

	for _, s := range group {
		body, _ := json.Marshal(map[string]any{"address": s.Txn.Sender.String()})
		if _, code, err := t.post(forkURL+"/avmfork/impersonate", "", body); err != nil || code != http.StatusOK {
			c.Commit, c.CommitDetail = Errored, fmt.Sprintf("impersonate: %v (HTTP %d)", err, code)
			return
		}
	}
	var raw []byte
	for _, s := range group {
		raw = append(raw, protocol.Encode(&s)...)
	}
	_, code, err := t.post(forkURL+"/v2/transactions", "", raw)
	if err != nil {
		c.Commit, c.CommitDetail = Errored, err.Error()
		return
	}
	if upFailed || code != http.StatusOK {
		if upFailed && code != http.StatusOK {
			c.Commit = Match // rejected on both
		} else {
			c.Commit, c.CommitDetail = Mismatch, fmt.Sprintf("simulate failed: %v, submit HTTP %d", upFailed, code)
		}
		return
	}
	if len(results) != len(group) {
		c.Commit, c.CommitDetail = Mismatch, fmt.Sprintf("%d simulate results for %d transactions", len(results), len(group))
		return
	}
	for i, s := range group {
		resp, err := t.Client.Get(fmt.Sprintf("%s/v2/transactions/pending/%s?format=json", forkURL, s.ID()))
		if err != nil {
			c.Commit, c.CommitDetail = Errored, err.Error()
			return
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			c.Commit, c.CommitDetail = Errored, fmt.Sprintf("pending %s: HTTP %d %v", s.ID(), resp.StatusCode, err)
			return
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			c.Commit, c.CommitDetail = Errored, err.Error()
			return
		}
		r, _ := results[i].(map[string]any)
		want, _ := r["txn-result"].(map[string]any)
		normalizePending(got)
		normalizePending(want)
		if !reflect.DeepEqual(want, got) {
			c.Commit, c.CommitDetail = Mismatch, fmt.Sprintf("txn %d: %s", i, firstDiff("", want, got))
			return
		}
	}
	c.Commit = Match
}

// normalizePending drops the fields of a pending-transaction record that
// differ between a simulation and a commit by design: the confirmed round,
// and the signer the fork records for an impersonated, rekeyed sender
// (simulate leaves it out).
func normalizePending(m map[string]any) {
	delete(m, "confirmed-round")
	delete(m, "pool-error")
	if st, ok := m["txn"].(map[string]any); ok {
		delete(st, "sgnr")
	}
	sortUnordered(m)
	if inner, ok := m["inner-txns"].([]any); ok {
		for _, x := range inner {
			if im, ok := x.(map[string]any); ok {
				normalizePending(im)
			}
		}
	}
}

// unorderedLists are the response arrays that algod (v5.0.x) fills by
// iterating Go maps, so their order differs from run to run.
var unorderedLists = map[string]bool{
	"app-initial-states": true,
	"app-locals":         true,
	"kvs":                true,
	"global-state-delta": true,
	"local-state-delta":  true,
	"delta":              true,
}

// sortUnordered sorts those arrays, everywhere in v, by their JSON text.
func sortUnordered(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			sortUnordered(e)
			if l, ok := e.([]any); ok && unorderedLists[k] {
				sort.SliceStable(l, func(i, j int) bool {
					a, _ := json.Marshal(l[i])
					b, _ := json.Marshal(l[j])
					return string(a) < string(b)
				})
			}
		}
	case []any:
		for _, e := range x {
			sortUnordered(e)
		}
	}
}

// isLeaseFromBeforeFork reports whether the upstream rejected the group for
// a lease it has on record and avmfork, which cannot know that lease, did
// not. (avmfork then runs the group further and may fail somewhere else.)
func isLeaseFromBeforeFork(up, fork map[string]any) bool {
	g, ok := firstGroup(up)
	if !ok {
		return false
	}
	msg, _ := g["failure-message"].(string)
	if !strings.Contains(msg, "using an overlapping lease") {
		return false
	}
	fg, ok := firstGroup(fork)
	if !ok {
		return false
	}
	fmsg, _ := fg["failure-message"].(string)
	return !strings.Contains(fmsg, "using an overlapping lease")
}

func firstGroup(resp map[string]any) (map[string]any, bool) {
	groups, ok := resp["txn-groups"].([]any)
	if !ok || len(groups) == 0 {
		return nil, false
	}
	g, ok := groups[0].(map[string]any)
	return g, ok
}

func normalizeErr(b []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) != nil {
		return string(b)
	}
	return e.Message
}

// firstDiff describes the first differing path between two JSON values.
func firstDiff(path string, a, b any) string {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return path + ": type differs"
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			if !reflect.DeepEqual(av[k], bv[k]) {
				return firstDiff(path+"."+k, av[k], bv[k])
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return fmt.Sprintf("%s: length %d vs %d", path, len(av), lenOf(b))
		}
		for i := range av {
			if !reflect.DeepEqual(av[i], bv[i]) {
				return firstDiff(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i])
			}
		}
	}
	return fmt.Sprintf("%s: %v vs %v", path, trunc(a), trunc(b))
}

func lenOf(v any) int {
	if s, ok := v.([]any); ok {
		return len(s)
	}
	return -1
}

func trunc(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func execTraceAll() upstreamsim.ExecTraceConfig {
	return upstreamsim.ExecTraceConfig{Enable: true, Stack: true, Scratch: true, State: true}
}
