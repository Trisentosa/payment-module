// Command apikey issues an API key for a calling service.
//
//	go run ./cmd/apikey order-service
//
// It prints the key, which goes to the calling service, and the CALLER_API_KEYS entry,
// which goes into PayGate's configuration. PayGate stores only the hash, so the key can't
// be recovered later. Issue a new one instead.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

func main() {
	if len(os.Args) != 2 || strings.TrimSpace(os.Args[1]) == "" || strings.ContainsAny(os.Args[1], ":,") {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/apikey <service-name>   (no ':' or ',' in the name)")
		os.Exit(2)
	}
	service := strings.TrimSpace(os.Args[1])

	key, err := apikey.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate key:", err)
		os.Exit(1)
	}

	fmt.Printf("API key for %s (give it to that service as a secret; it is not shown again):\n  %s\n\n", service, key)
	fmt.Printf("Add this entry to PayGate's CALLER_API_KEYS (comma-separated):\n  %s:%s\n", service, apikey.Hash(key))
}
