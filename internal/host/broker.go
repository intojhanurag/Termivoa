package host

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

const databaseFilename = "phone-terminal.db"

type BrokerConfig struct {
	DataDir    string
	RuntimeDir string
	HostBuild  string
	Profile    platform.Profile

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
	Auth       *auth.Service
	Push       *webpush.Notifier
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
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return fmt.Errorf("host: broker platform %q is not a supported v0 host", runtime.GOOS)
	}

	validated, err := platform.ValidatePaths(config.Profile, platform.Paths{
		DataDir: config.DataDir, RuntimeDir: config.RuntimeDir,
	})
	if err != nil {
		return err
	}
	config.DataDir = validated.DataDir
	config.RuntimeDir = validated.RuntimeDir

	generation := uuid.New().String()
	// admin.shutdown cancels this context: the same orderly exit as a signal.
	ctx, shutdown := context.WithCancel(ctx)
	defer shutdown()
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

	authority, err := auth.NewService(auth.ServiceConfig{Store: persistence})
	if err != nil {
		return err
	}
	// The broker sends push notifications: it owns devices, revocation and
	// session state. It creates its VAPID key on first start. Sessions send
	// agent waiting/done events through the registry.
	notifier, err := webpush.NewNotifier(ctx, webpush.NotifierConfig{
		Store: persistence, Revoked: authority.DeviceRevoked, CredentialIdle: auth.BrowserInactivityLifetime,
	})
	if err != nil {
		return err
	}
	registry, err := broker.NewRegistry(ctx, broker.RegistryConfig{
		Store: persistence, BrokerGeneration: generation, Notify: notifier.Notify,
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
	// Shell discovery only saves absolute executables that exist; it never
	// runs them. A database failure here prevents startup honestly.
	if err := broker.DetectShellProfiles(ctx, persistence, store.Platform(runtime.GOOS), time.Now()); err != nil {
		return err
	}
	server, err := broker.NewManagementServer(broker.ManagementServerConfig{
		Registry: registry, Store: persistence,
		BrokerGeneration: generation, HostBuild: config.HostBuild, Auth: authority, Push: notifier,
		Shutdown: shutdown, Development: config.Profile == platform.DevelopmentProfile,
	})
	if err != nil {
		return err
	}
	if config.Ready != nil {
		config.Ready(BrokerReady{
			Generation: generation, Descriptor: listener.Descriptor(), Registry: registry,
			Store: persistence, Auth: authority, Push: notifier, Recovered: append([]store.SessionMetadata(nil), recovered...),
		})
	}

	serveErr := server.Serve(ctx, listener)
	if ctx.Err() != nil && errors.Is(serveErr, ctx.Err()) {
		// Cancellation between Ready and Serve is the same orderly shutdown
		// that Serve reports as nil once it is running.
		serveErr = nil
	}
	if config.beforeRegistryClose != nil {
		config.beforeRegistryClose()
	}
	registryOwned = false
	registryErr := registry.Close()
	storeOwned = false
	storeErr := persistence.Close()
	return errors.Join(serveErr, registryErr, storeErr)
}
