// Command avmfork runs a local Algorand node that forks MainNet, TestNet or
// any network served by an algod REST endpoint.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/algorand/go-algorand/data/basics"

	"github.com/let-the-dreamers-rise/avmfork/chain"
	"github.com/let-the-dreamers-rise/avmfork/forkledger"
	"github.com/let-the-dreamers-rise/avmfork/server"
)

var networks = map[string]string{
	"mainnet": "https://mainnet-api.algonode.cloud",
	"testnet": "https://testnet-api.algonode.cloud",
}

func main() {
	fork := flag.String("fork", "mainnet", "network to fork: mainnet, testnet, an algod URL, or none for an empty chain")
	forkToken := flag.String("fork-token", "", "API token for the upstream algod, if it needs one")
	round := flag.Uint64("fork-round", 0, "round to fork at (default: the upstream's latest)")
	listen := flag.String("listen", "127.0.0.1:4002", "address to serve the algod API on")
	token := flag.String("token", "", "require this X-Algo-API-Token on requests")
	flag.Parse()

	url := *fork
	if u, ok := networks[strings.ToLower(url)]; ok {
		url = u
	}
	var l *forkledger.Ledger
	var err error
	if strings.ToLower(url) == "none" {
		l, err = forkledger.NewEmpty("avmfork-v1", time.Now().Unix())
	} else {
		var remote *forkledger.Remote
		remote, err = forkledger.NewRemote(url, *forkToken)
		if err != nil {
			fatal(err)
		}
		if *round == 0 {
			l, err = forkledger.Open(remote)
		} else {
			l, err = forkledger.OpenAt(remote, basics.Round(*round))
		}
	}
	if err != nil {
		fatal(err)
	}
	if l.Remote() != nil {
		// Transaction ids and leases from the last 1000 rounds, so the
		// duplicate checks match the network's.
		go func() {
			if err := l.LoadRecent(); err != nil {
				fmt.Fprintln(os.Stderr, "avmfork: loading recent transactions:", err)
			}
		}()
	}
	c := chain.New(l)
	e := server.New(server.NewNode(c), server.Options{Token: *token})

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fatal(err)
	}
	if l.Remote() == nil {
		fmt.Printf("avmfork: empty chain %s\n", l.GenesisID())
	} else {
		fmt.Printf("avmfork: forked %s (%s) at round %d\n", l.GenesisID(), url, l.ForkRound())
	}
	fmt.Printf("avmfork: algod API on http://%s\n", ln.Addr())
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		ln.Close()
	}()
	if err := http.Serve(ln, e); err != nil && !strings.Contains(err.Error(), "use of closed") {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "avmfork:", err)
	os.Exit(1)
}
