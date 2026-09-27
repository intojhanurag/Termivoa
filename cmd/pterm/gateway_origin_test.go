package main

import (
	"reflect"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
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

func TestGatewayPreviewListenFlag(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
		ok   bool
	}{
		{nil, gateway.ProductionPreviewListenAddress, true},
		{[]string{"--preview-listen", "127.0.0.1:9000"}, "", false}, // production is fixed
		{[]string{"--preview-listen", "off"}, "", true},
		{[]string{"--dev"}, gateway.DevelopmentPreviewListenAddress, true},
		{[]string{"--dev", "--preview-listen", "127.0.0.1:0"}, "127.0.0.1:0", true},
		{[]string{"--dev", "--preview-listen", "off"}, "", true},
		{[]string{"--dev", "--preview-listen", "0.0.0.0:8790"}, "", false},
		{[]string{"--dev", "--preview-listen", gateway.ProductionPreviewListenAddress}, "", false},
	} {
		config, err := parseGatewayConfig(test.args)
		if (err == nil) != test.ok || test.ok && config.PreviewListenAddress != test.want {
			t.Errorf("%v = %q, %v", test.args, config.PreviewListenAddress, err)
		}
	}
}
