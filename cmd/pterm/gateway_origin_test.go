package main

import (
	"reflect"
	"testing"
)

func TestGatewayOriginFlag(t *testing.T) {
	config, err := parseGatewayConfig([]string{"--origin", "https://mac.tailnet.ts.net", "--origin", "https://win.tailnet.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://mac.tailnet.ts.net", "https://win.tailnet.ts.net"}; !reflect.DeepEqual(config.Origins, want) {
		t.Fatalf("origins = %v", config.Origins)
	}
	if config.Broker != nil {
		t.Fatal("broker client wired before internal/gatewayclient exists")
	}
	for _, args := range [][]string{
		{"--origin", "http://mac.tailnet.ts.net"},
		{"--origin", "https://mac.tailnet.ts.net/path"},
		{"--dev", "--origin", "https://mac.tailnet.ts.net"},
	} {
		if _, err := parseGatewayConfig(args); err == nil {
			t.Fatalf("parseGatewayConfig(%v) accepted an invalid origin", args)
		}
	}
	config, err = parseGatewayConfig([]string{"--dev", "--origin", "http://localhost:5173"})
	if err != nil || len(config.Origins) != 1 {
		t.Fatalf("development loopback origin = %v, %v", config.Origins, err)
	}
}
