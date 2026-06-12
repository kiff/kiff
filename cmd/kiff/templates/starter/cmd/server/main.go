// Command server runs the starter KIFF domain over HTTP.
//
// It is intentionally small: parse flags, build a runtime, hand it to the
// optional httpapi handler, and serve. Production deployments swap the stores
// for a real backend and add their own middleware (auth, logging, metrics)
// around the handler.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/kiff/kiff/cmd/kiff/templates/starter/domain"

	"github.com/kiff/kiff/pkg/kiff/httpapi"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/store/file"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataDir := flag.String("data-dir", "", "Directory for file-backed JSONL stores; empty uses in-memory stores")
	flag.Parse()

	rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "starter server failed to start: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF starter: tasks domain")
	fmt.Printf("- listening on %s\n", url)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores (state lost on restart; pass -data-dir for persistence)")
	}
	fmt.Println("- routes:")
	fmt.Println("  POST /events/raw")
	fmt.Println("  GET  /entities/{entityID}/allowed-actions")
	fmt.Println("  POST /entities/{entityID}/actions/{actionName}/validate")
	fmt.Println("  POST /entities/{entityID}/actions/{actionName}/execute")
	fmt.Println("  POST /entities/{entityID}/actions/{actionName}/approvals")
	fmt.Println("  GET  /entities/{entityID}/approvals")
	fmt.Println("  POST /approvals/{approvalID}/grant")
	fmt.Println("  POST /approvals/{approvalID}/deny")
	fmt.Println("  GET  /entities/{entityID}/timeline")

	server := &http.Server{Handler: httpapi.NewHandler(rt)}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
}

// buildRuntime wires the domain runtime, choosing in-memory or file-backed
// stores based on dataDir. The closer must be invoked on shutdown when stores
// are file-backed.
func buildRuntime(dataDir string) (*runtime.Runtime, func(), error) {
	if dataDir == "" {
		rt, err := domain.NewRuntime()
		return rt, nil, err
	}
	bundle, err := file.NewBundle(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open file bundle: %w", err)
	}
	storeBundle := bundle.AsStoreBundle()
	rt, err := domain.NewRuntimeWithStores(&storeBundle)
	if err != nil {
		_ = bundle.Close()
		return nil, nil, err
	}
	return rt, func() { _ = bundle.Close() }, nil
}

func localURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "::", "[::]", "0.0.0.0":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
