package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/platform"
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
	if brokerConfig.DataDir != dataDir || brokerConfig.RuntimeDir != runtimeDir || brokerConfig.HostBuild != buildID ||
		brokerConfig.Profile != platform.DevelopmentProfile {
		t.Fatalf("broker config = %#v", brokerConfig)
	}
	gatewayConfig, err := parseGatewayConfig([]string{
		"--dev", "--runtime-dir", runtimeDir, "--listen", "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gatewayConfig.RuntimeDir != runtimeDir || gatewayConfig.ListenAddress != "127.0.0.1:0" ||
		gatewayConfig.HostBuild != buildID || gatewayConfig.Profile != platform.DevelopmentProfile {
		t.Fatalf("gateway config = %#v", gatewayConfig)
	}
}

func TestBareAndPartialDevelopmentModesNeverReuseProductionDefaults(t *testing.T) {
	production, err := platform.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	development, err := platform.DevelopmentPaths()
	if err != nil {
		t.Fatal(err)
	}
	brokerConfig, err := parseBrokerConfig([]string{"--dev"})
	if err != nil {
		t.Fatal(err)
	}
	if brokerConfig.DataDir != development.DataDir || brokerConfig.RuntimeDir != development.RuntimeDir ||
		brokerConfig.DataDir == production.DataDir || brokerConfig.RuntimeDir == production.RuntimeDir {
		t.Fatalf("bare development broker config = %#v", brokerConfig)
	}
	gatewayConfig, err := parseGatewayConfig([]string{"--dev"})
	if err != nil {
		t.Fatal(err)
	}
	if gatewayConfig.RuntimeDir != development.RuntimeDir ||
		gatewayConfig.ListenAddress != gateway.DevelopmentListenAddress {
		t.Fatalf("bare development gateway config = %#v", gatewayConfig)
	}

	customData := filepath.Join(t.TempDir(), "data")
	brokerConfig, err = parseBrokerConfig([]string{"--dev", "--data-dir", customData})
	if err != nil {
		t.Fatal(err)
	}
	if brokerConfig.DataDir != customData || brokerConfig.RuntimeDir != development.RuntimeDir {
		t.Fatalf("partial development broker config = %#v", brokerConfig)
	}
	customRuntime := filepath.Join(t.TempDir(), "runtime")
	brokerConfig, err = parseBrokerConfig([]string{"--dev", "--runtime-dir", customRuntime})
	if err != nil {
		t.Fatal(err)
	}
	if brokerConfig.DataDir != development.DataDir || brokerConfig.RuntimeDir != customRuntime {
		t.Fatalf("partial development broker runtime config = %#v", brokerConfig)
	}
	gatewayConfig, err = parseGatewayConfig([]string{"--dev", "--listen", "127.0.0.1:9080"})
	if err != nil {
		t.Fatal(err)
	}
	if gatewayConfig.RuntimeDir != development.RuntimeDir || gatewayConfig.ListenAddress != "127.0.0.1:9080" {
		t.Fatalf("partial development gateway config = %#v", gatewayConfig)
	}
}

func TestDevelopmentModesRejectProductionPathsAndCanonicalAliases(t *testing.T) {
	production, err := platform.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseBrokerConfig([]string{"--dev", "--data-dir", production.DataDir}); err == nil {
		t.Fatal("development broker accepted production data path")
	}
	if _, err := parseGatewayConfig([]string{"--dev", "--runtime-dir", production.RuntimeDir}); err == nil {
		t.Fatal("development gateway accepted production runtime path")
	}

	aliasRoot := filepath.Join(t.TempDir(), "production-parent")
	if err := os.Symlink(filepath.Dir(production.DataDir), aliasRoot); err != nil {
		t.Skipf("canonical alias test requires symlink support: %v", err)
	}
	alias := filepath.Join(aliasRoot, filepath.Base(production.DataDir))
	if _, err := parseBrokerConfig([]string{"--dev", "--data-dir", alias}); err == nil {
		t.Fatal("development broker accepted canonical alias of production data")
	}
	runtimeAliasRoot := filepath.Join(t.TempDir(), "production-cache")
	productionCache := filepath.Dir(filepath.Dir(production.RuntimeDir))
	if err := os.Symlink(productionCache, runtimeAliasRoot); err != nil {
		t.Skipf("canonical runtime alias test requires symlink support: %v", err)
	}
	runtimeAlias := filepath.Join(runtimeAliasRoot, filepath.Base(filepath.Dir(production.RuntimeDir)), filepath.Base(production.RuntimeDir))
	if _, err := parseGatewayConfig([]string{"--dev", "--runtime-dir", runtimeAlias}); err == nil {
		t.Fatal("development gateway accepted canonical alias of production runtime")
	}
	if _, err := parseBrokerConfig([]string{"--dev", "--data-dir", filepath.Join(production.DataDir, "child")}); err == nil {
		t.Fatal("development broker accepted a child of production data")
	}
}

func TestProductionModesUseFixedDefaults(t *testing.T) {
	want, err := platform.DefaultPaths()
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
	if output.String() != "Termivoa private development build\n" {
		t.Fatalf("output = %q", output.String())
	}
}
