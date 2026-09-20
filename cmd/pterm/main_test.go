package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/host"
)

func TestInternalModeOverridesRequireExplicitDevelopmentProfile(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name string
		run  func([]string) error
		args []string
	}{
		{
			name: "broker",
			run: func(args []string) error {
				_, err := parseBrokerConfig(args)
				return err
			},
			args: []string{"--data-dir", filepath.Join(root, "data")},
		},
		{
			name: "gateway runtime",
			run: func(args []string) error {
				_, err := parseGatewayConfig(args)
				return err
			},
			args: []string{"--runtime-dir", filepath.Join(root, "runtime")},
		},
		{
			name: "gateway listener",
			run: func(args []string) error {
				_, err := parseGatewayConfig(args)
				return err
			},
			args: []string{"--listen", "127.0.0.1:0"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(test.args); err == nil {
				t.Fatal("override succeeded without --dev")
			}
		})
	}
}

func TestDevelopmentModeUsesOnlyExplicitIsolatedPaths(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	runtimeDir := filepath.Join(root, "runtime")
	brokerConfig, err := parseBrokerConfig([]string{
		"--dev", "--data-dir", dataDir, "--runtime-dir", runtimeDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if brokerConfig.DataDir != dataDir || brokerConfig.RuntimeDir != runtimeDir || brokerConfig.HostBuild != buildID {
		t.Fatalf("broker config = %#v", brokerConfig)
	}
	gatewayConfig, err := parseGatewayConfig([]string{
		"--dev", "--runtime-dir", runtimeDir, "--listen", "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gatewayConfig.RuntimeDir != runtimeDir || gatewayConfig.ListenAddress != "127.0.0.1:0" ||
		gatewayConfig.HostBuild != buildID {
		t.Fatalf("gateway config = %#v", gatewayConfig)
	}
}

func TestProductionModesUseFixedDefaults(t *testing.T) {
	want, err := host.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	brokerConfig, err := parseBrokerConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if brokerConfig.DataDir != want.DataDir || brokerConfig.RuntimeDir != want.RuntimeDir {
		t.Fatalf("broker defaults = %#v, want %#v", brokerConfig, want)
	}
	gatewayConfig, err := parseGatewayConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if gatewayConfig.RuntimeDir != want.RuntimeDir || gatewayConfig.ListenAddress != gateway.ProductionListenAddress {
		t.Fatalf("gateway defaults = %#v", gatewayConfig)
	}
}

func TestRunWithoutCommandIsNonOperational(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), nil, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Phone Terminal private development build\n" {
		t.Fatalf("output = %q", output.String())
	}
}
