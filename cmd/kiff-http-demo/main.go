package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/kiff-framework/kiff-framework/examples/mission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/httpapi"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store/file"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataDir := flag.String("data-dir", "", "Directory for file-backed JSONL stores; empty uses in-memory stores")
	flag.Parse()

	rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff http demo failed: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff http demo listen failed: %v\n", err)
		os.Exit(1)
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF HTTP demo: mission runtime")
	fmt.Printf("- listening on %s\n", url)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s (events.jsonl, decisions.jsonl, approvals.jsonl, audit.jsonl)\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores (state lost on restart; use -data-dir for persistence)")
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
	fmt.Println("- see docs/changelog/brick-14.md for curl examples")

	server := &http.Server{
		Handler: httpapi.NewHandler(rt),
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "kiff http demo failed: %v\n", err)
		os.Exit(1)
	}
}

// buildRuntime returns a mission runtime configured with either in-memory
// stores (when dataDir is empty) or file-backed JSONL stores. The returned
// closer must be invoked at shutdown when stores are file-backed.
func buildRuntime(dataDir string) (*runtime.Runtime, func(), error) {
	if dataDir == "" {
		rt, err := mission.NewRuntime()
		return rt, nil, err
	}
	bundle, err := file.NewBundle(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open file bundle: %w", err)
	}
	storeBundle := bundle.AsStoreBundle()
	rt, err := mission.NewRuntimeWithStores(&storeBundle)
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
	switch {
	case host == "", host == "::", host == "[::]", host == "0.0.0.0":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
