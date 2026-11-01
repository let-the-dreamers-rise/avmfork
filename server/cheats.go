package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/algorand/go-algorand/data/basics"

	"github.com/let-the-dreamers-rise/avmfork/chain"
)

// The /avmfork endpoints change the fork directly, the way Anvil's cheat
// RPCs do for EVM forks. Bodies and responses are JSON. Byte strings
// (box names, state keys, byte values) are base64.

type cheatError struct {
	Message string `json:"message"`
}

func fail(c echo.Context, code int, err error) error {
	return c.JSON(code, cheatError{Message: err.Error()})
}

type snapshots struct {
	mu   sync.Mutex
	next int
	byID map[int]chain.Snapshot
}

func registerCheats(e *echo.Echo, n *Node, mw ...echo.MiddlewareFunc) {
	g := e.Group("/avmfork", mw...)
	snaps := &snapshots{byID: map[int]chain.Snapshot{}}
	l := n.C.L

	g.GET("/info", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"fork-round":   l.ForkRound(),
			"latest-round": l.Latest(),
			"genesis-id":   l.GenesisID(),
			"upstream":     l.Remote() != nil,
		})
	})

	g.POST("/impersonate", func(c echo.Context) error {
		var req struct {
			Address string `json:"address"`
			Enabled *bool  `json:"enabled"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		addr, err := basics.UnmarshalChecksumAddress(req.Address)
		if err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		on := req.Enabled == nil || *req.Enabled
		n.C.Impersonate(addr, on)
		return c.JSON(http.StatusOK, map[string]any{"address": req.Address, "impersonated": on})
	})

	g.POST("/set-balance", func(c echo.Context) error {
		var req struct {
			Address    string `json:"address"`
			MicroAlgos uint64 `json:"microalgos"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		addr, err := basics.UnmarshalChecksumAddress(req.Address)
		if err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		if err := l.SetBalance(addr, basics.MicroAlgos{Raw: req.MicroAlgos}); err != nil {
			return fail(c, http.StatusInternalServerError, err)
		}
		return c.JSON(http.StatusOK, map[string]any{"address": req.Address, "microalgos": req.MicroAlgos})
	})

	g.POST("/set-asset-holding", func(c echo.Context) error {
		var req struct {
			Address string `json:"address"`
			Asset   uint64 `json:"asset"`
			Amount  uint64 `json:"amount"`
			Frozen  bool   `json:"frozen"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		addr, err := basics.UnmarshalChecksumAddress(req.Address)
		if err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		if err := l.SetAssetHolding(addr, basics.AssetIndex(req.Asset), basics.AssetHolding{Amount: req.Amount, Frozen: req.Frozen}); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		return c.JSON(http.StatusOK, req)
	})

	g.POST("/set-box", func(c echo.Context) error {
		var req struct {
			App   uint64  `json:"app"`
			Name  string  `json:"name"`
			Value *string `json:"value"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		name, err := base64.StdEncoding.DecodeString(req.Name)
		if err != nil {
			return fail(c, http.StatusBadRequest, fmt.Errorf("name: %w", err))
		}
		var value []byte
		if req.Value != nil {
			value, err = base64.StdEncoding.DecodeString(*req.Value)
			if err != nil {
				return fail(c, http.StatusBadRequest, fmt.Errorf("value: %w", err))
			}
		}
		if err := l.SetBoxValue(basics.AppIndex(req.App), string(name), value, req.Value != nil); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		return c.JSON(http.StatusOK, req)
	})

	type stateReq struct {
		App     uint64  `json:"app"`
		Address string  `json:"address"` // empty for global state
		Key     string  `json:"key"`
		Uint    *uint64 `json:"uint"`
		Bytes   *string `json:"bytes"`
		Delete  bool    `json:"delete"`
	}
	g.POST("/set-state", func(c echo.Context) error {
		var req stateReq
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		key, err := base64.StdEncoding.DecodeString(req.Key)
		if err != nil {
			return fail(c, http.StatusBadRequest, fmt.Errorf("key: %w", err))
		}
		var tv *basics.TealValue
		switch {
		case req.Delete:
		case req.Uint != nil && req.Bytes == nil:
			tv = &basics.TealValue{Type: basics.TealUintType, Uint: *req.Uint}
		case req.Bytes != nil && req.Uint == nil:
			b, err := base64.StdEncoding.DecodeString(*req.Bytes)
			if err != nil {
				return fail(c, http.StatusBadRequest, fmt.Errorf("bytes: %w", err))
			}
			tv = &basics.TealValue{Type: basics.TealBytesType, Bytes: string(b)}
		default:
			return fail(c, http.StatusBadRequest, fmt.Errorf("give exactly one of uint, bytes or delete"))
		}
		var holder *basics.Address
		if req.Address != "" {
			a, err := basics.UnmarshalChecksumAddress(req.Address)
			if err != nil {
				return fail(c, http.StatusBadRequest, err)
			}
			holder = &a
		}
		if err := l.SetAppState(basics.AppIndex(req.App), holder, string(key), tv); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		return c.JSON(http.StatusOK, req)
	})

	g.POST("/warp", func(c echo.Context) error {
		var req struct {
			Seconds   int64 `json:"seconds"`
			Timestamp int64 `json:"timestamp"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		if req.Timestamp != 0 {
			n.C.SetNextTimestamp(req.Timestamp)
		} else {
			n.C.WarpTime(time.Duration(req.Seconds) * time.Second)
		}
		return c.JSON(http.StatusOK, map[string]any{"time-offset": n.C.TimeOffset()})
	})

	g.POST("/mine", func(c echo.Context) error {
		var req struct {
			Blocks int `json:"blocks"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		if req.Blocks <= 0 {
			req.Blocks = 1
		}
		if req.Blocks > 100000 {
			return fail(c, http.StatusBadRequest, fmt.Errorf("at most 100000 blocks per call"))
		}
		rnd, err := n.C.AdvanceBlocks(req.Blocks)
		if err != nil {
			return fail(c, http.StatusInternalServerError, err)
		}
		return c.JSON(http.StatusOK, map[string]any{"latest-round": rnd})
	})

	g.POST("/snapshot", func(c echo.Context) error {
		snaps.mu.Lock()
		defer snaps.mu.Unlock()
		snaps.next++
		snaps.byID[snaps.next] = n.C.Snapshot()
		return c.JSON(http.StatusOK, map[string]any{"id": snaps.next, "round": l.Latest()})
	})

	g.POST("/revert", func(c echo.Context) error {
		var req struct {
			ID int `json:"id"`
		}
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, err)
		}
		snaps.mu.Lock()
		s, ok := snaps.byID[req.ID]
		snaps.mu.Unlock()
		if !ok {
			return fail(c, http.StatusNotFound, fmt.Errorf("no snapshot %d", req.ID))
		}
		n.C.Revert(s)
		return c.JSON(http.StatusOK, map[string]any{"id": req.ID, "round": l.Latest()})
	})
}
