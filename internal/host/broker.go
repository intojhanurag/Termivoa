package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const databaseFilename = "phone-terminal.db"

type BrokerConfig struct {
	DataDir    string
	RuntimeDir string
	HostBuild  string
	Profile    Profile

	// Ready is an internal integration-test observation point. It receives no
	// ownership: the broker remains solely responsible for every value and the
	// callback must return promptly.
	Ready func(BrokerReady)

	// beforeRegistryClose is a package-test synchronization point. Production
	// callers cannot set it.
	beforeRegistryClose func()
}

type BrokerReady struct {
	Generation string
	Descriptor ipc.EndpointDescriptor
	Registry   *broker.Registry
	Store      *store.Store
	Recovered  []store.SessionMetadata
}

// RunBroker owns the complete persistent broker lifetime. The restricted IPC
// data-directory and runtime locks are acquired before SQLite is opened or a
// generation is begun. The data lock remains held through Registry and Store
// cleanup, so a differently configured runtime cannot overlap ownership or
// mark live metadata lost.
func RunBroker(ctx context.Context, config BrokerConfig) (returnErr error) {
	if ctx == nil {
		return errors.New("host: broker context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.DataDir == "" || config.RuntimeDir == "" {
		return errors.New("host: broker data and runtime directories are required")
	}
	if len(config.HostBuild) == 0 || len(config.HostBuild) > 128 {
		return errors.New("host: broker build must be 1..128 bytes")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return fmt.Errorf("host: broker platform %q is not a supported v0 host", runtime.GOOS)
	}

	validated, err := ValidatePaths(config.Profile, Paths{
		DataDir: config.DataDir, RuntimeDir: config.RuntimeDir,
	})
	if err != nil {
		return err
	}
	config.DataDir = validated.DataDir
	config.RuntimeDir = validated.RuntimeDir

	generation, err := newUUID()
	if err != nil {
		return err
	}
	dataDir, dataLock, err := ipc.LockBrokerDataDirectory(config.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, dataLock.Close())
	}()
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{
		RuntimeDir: config.RuntimeDir, BrokerGeneration: generation,
	})
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, listener.Close())
	}()

	databasePath := filepath.Join(dataDir, databaseFilename)
	persistence, err := store.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	storeOwned := true
	defer func() {
		if storeOwned {
			returnErr = errors.Join(returnErr, persistence.Close())
		}
	}()
	if err := ipc.EnsurePrivateFile(databasePath); err != nil {
		return err
	}
	recovered, err := persistence.BeginBrokerGeneration(ctx, generation, time.Now().UTC())
	if err != nil {
		return err
	}

	registry, err := broker.NewRegistry(ctx, broker.RegistryConfig{
		Store: persistence, BrokerGeneration: generation,
	})
	if err != nil {
		return err
	}
	registryOwned := true
	defer func() {
		if registryOwned {
			returnErr = errors.Join(returnErr, registry.Close())
		}
	}()
	server, err := broker.NewManagementServer(broker.ManagementServerConfig{
		Registry: registry, Store: persistence,
		BrokerGeneration: generation, HostBuild: config.HostBuild,
	})
	if err != nil {
		return err
	}
	if config.Ready != nil {
		config.Ready(BrokerReady{
			Generation: generation, Descriptor: listener.Descriptor(), Registry: registry,
			Store: persistence, Recovered: append([]store.SessionMetadata(nil), recovered...),
		})
	}

	serveErr := server.Serve(ctx, listener)
	if config.beforeRegistryClose != nil {
		config.beforeRegistryClose()
	}
	registryOwned = false
	registryErr := registry.Close()
	storeOwned = false
	storeErr := persistence.Close()
	return errors.Join(serveErr, registryErr, storeErr)
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("host: create broker generation: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
}
