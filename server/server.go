package server

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/sync/semaphore"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/daemon/algod/api/server/common"
	"github.com/algorand/go-algorand/daemon/algod/api/server/lib"
	v2 "github.com/algorand/go-algorand/daemon/algod/api/server/v2"
	"github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/data"
	"github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/experimental"
	"github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/model"
	npprivate "github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/nonparticipating/private"
	nppublic "github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/nonparticipating/public"
	pprivate "github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/participating/private"
	ppublic "github.com/algorand/go-algorand/daemon/algod/api/server/v2/generated/participating/public"
	"github.com/algorand/go-algorand/logging"
	"github.com/algorand/go-algorand/protocol"

	"github.com/let-the-dreamers-rise/avmfork/internal/simulation"
)

// handlers is algod's own v2 handler set, with simulate routed to avmfork's
// copy of the simulator.
type handlers struct {
	*v2.Handlers
	node *Node
}

// maxSimulateBytes matches algod's limit.
const maxSimulateBytes = v2.MaxSimulateBytes

// SimulateTransaction mirrors algod's handler (daemon/algod/api/server/v2
// handlers.go) line for line, calling the fork's simulator.
func (h *handlers) SimulateTransaction(ctx echo.Context, params model.SimulateTransactionParams) error {
	stat, err := h.node.Status()
	if err != nil {
		return internalError(ctx, err, "failed retrieving node status", h.Log)
	}
	proto := config.Consensus[stat.LastVersion]

	requestBuffer := new(bytes.Buffer)
	requestBodyReader := http.MaxBytesReader(nil, ctx.Request().Body, maxSimulateBytes)
	if _, err = requestBuffer.ReadFrom(requestBodyReader); err != nil {
		return badRequest(ctx, err, err.Error(), h.Log)
	}
	requestData := requestBuffer.Bytes()

	var simulateRequest v2.PreEncodedSimulateRequest
	err = decode(protocol.CodecHandle, requestData, &simulateRequest)
	if err != nil {
		err = decode(protocol.JSONStrictHandle, requestData, &simulateRequest)
		if err != nil {
			return badRequest(ctx, err, err.Error(), h.Log)
		}
	}
	for _, txgroup := range simulateRequest.TxnGroups {
		if len(txgroup.Txns) == 0 {
			err = errors.New("empty txgroup")
			return badRequest(ctx, err, err.Error(), h.Log)
		}
		if len(txgroup.Txns) > proto.MaxTxGroupSize {
			err = fmt.Errorf("transaction group size %d exceeds protocol max %d", len(txgroup.Txns), proto.MaxTxGroupSize)
			return badRequest(ctx, err, err.Error(), h.Log)
		}
	}
	req := convertSimulationRequest(simulateRequest)
	if req.Round != 0 && req.Round != h.node.C.L.Latest() {
		err = fmt.Errorf("avmfork simulates only on top of its latest round %d", h.node.C.L.Latest())
		return badRequest(ctx, err, err.Error(), h.Log)
	}
	req.Round = 0
	simulationResult, err := h.node.simulate(req)
	if err != nil {
		var invalidTxErr simulation.InvalidRequestError
		if errors.As(err, &invalidTxErr) {
			return badRequest(ctx, invalidTxErr, invalidTxErr.Error(), h.Log)
		}
		return internalError(ctx, err, err.Error(), h.Log)
	}
	response := convertSimulationResult(simulationResult)
	handle, contentType, err := getCodecHandle((*string)(params.Format))
	if err != nil {
		return badRequest(ctx, err, "failed to parse the format option", h.Log)
	}
	responseData, err := encode(handle, &response)
	if err != nil {
		return internalError(ctx, err, "failed to encode response", h.Log)
	}
	return ctx.Blob(http.StatusOK, contentType, responseData)
}

// Options for New.
type Options struct {
	// Token, if set, is required in the X-Algo-API-Token header.
	Token string
	Log   logging.Logger
}

// New returns an HTTP handler serving algod's REST API and avmfork's
// /avmfork control endpoints for n.
func New(n *Node, opts Options) *echo.Echo {
	log := opts.Log
	if log == nil {
		log = logging.NewLogger()
		log.SetLevel(logging.Warn)
	}
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Pre(middleware.RemoveTrailingSlash())
	// Browser tools such as Lora need CORS to talk to a local node. The
	// /avmfork control endpoints get no CORS headers, so a web page cannot
	// call them from the browser.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		Skipper: func(c echo.Context) bool {
			return strings.HasPrefix(c.Request().URL.Path, "/avmfork")
		},
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{"*"},
	}))
	var mw []echo.MiddlewareFunc
	mw = append(mw, middleware.BodyLimit("10MB"))
	if opts.Token != "" {
		mw = append(mw, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if c.Request().Header.Get("X-Algo-API-Token") != opts.Token {
					return c.JSON(http.StatusUnauthorized, model.ErrorResponse{Message: "Invalid API Token"})
				}
				return next(c)
			}
		})
	}

	ctx := lib.ReqContext{Node: n, Log: log, Shutdown: make(chan struct{})}
	for _, route := range common.Routes {
		r := route
		e.Add(r.Method, r.Path, func(c echo.Context) error {
			r.HandlerFunc(ctx, c)
			return nil
		})
	}

	h := &handlers{
		Handlers: &v2.Handlers{Node: n, Log: log, Shutdown: ctx.Shutdown, KeygenLimiter: semaphore.NewWeighted(1)},
		node:     n,
	}
	nppublic.RegisterHandlers(e, h, mw...)
	npprivate.RegisterHandlers(e, h, mw...)
	ppublic.RegisterHandlers(e, h, mw...)
	pprivate.RegisterHandlers(e, h, mw...)
	data.RegisterHandlers(e, h, mw...)
	experimental.RegisterHandlers(e, h, mw...)

	registerCheats(e, n, mw...)
	return e
}
