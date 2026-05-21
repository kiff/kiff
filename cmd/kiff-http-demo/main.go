package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/kiff-framework/kiff-framework/examples/mission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/httpapi"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	rt, err := mission.NewRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff http demo failed: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff http demo listen failed: %v\n", err)
		os.Exit(1)
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF HTTP demo: mission runtime")
	fmt.Printf("- listening on %s\n", url)
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
	fmt.Println("- see docs/brick-14.md for curl examples")

	server := &http.Server{
		Handler: httpapi.NewHandler(rt),
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "kiff http demo failed: %v\n", err)
		os.Exit(1)
	}
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
