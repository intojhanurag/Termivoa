package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// runStopCommand stops only the gateway. Broker sessions keep running.
func runStopCommand(ctx context.Context, args []string, output io.Writer) error {
	config, err := parseLocalCommand("stop", args)
	if errors.Is(err, flag.ErrHelp) {
		_, err = io.WriteString(output, commandHelp)
		return err
	}
	if err != nil {
		return err
	}
	stopped, err := stopGateway(ctx, config.runtimeDir)
	if err != nil {
		return err
	}
	var text strings.Builder
	switch {
	case stopped && config.dev:
		text.WriteString("Gateway stopped. Phones cannot connect until it starts again.\n")
	case stopped:
		text.WriteString("Gateway stopped. Phones cannot connect until it starts again (pterm launch).\n")
	case !config.dev && gatewayHealthy(ctx, strings.TrimSuffix(lifecycle.LocalURL, "/")) == nil:
		return fmt.Errorf("a gateway answers at %s but has no stop endpoint; it may be an older build—stop it through its supervisor", lifecycle.LocalURL)
	default:
		text.WriteString("The gateway is not running.\n")
	}
	count, reachable, err := activeSessions(ctx, platform.Paths{RuntimeDir: config.runtimeDir})
	switch {
	case !reachable:
		text.WriteString("The broker is not running.\n")
	case err != nil:
		text.WriteString("The broker keeps running; its sessions could not be listed.\n")
	default:
		fmt.Fprintf(&text, "The broker keeps running with %d active session(s); nothing was ended. "+
			"pterm list shows them; pterm shutdown ends them and stops the broker.\n", count)
	}
	if !config.dev {
		supervisorNote(ctx, &text, lifecycle.Services[1:])
	}
	_, err = io.WriteString(output, text.String())
	return err
}

// runShutdownCommand confirms, asks the broker to end every session and
// exit, waits until it has gone, then stops the gateway.
func runShutdownCommand(ctx context.Context, args []string, input *os.File, output io.Writer) (returnErr error) {
	config, parseErr := parseLocalCommand("shutdown", args)
	if parseErr != nil && !errors.Is(parseErr, flag.ErrHelp) {
		return parseErr
	}
	managedOutput, err := newManagementOutput(ctx, output)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, managedOutput.Close()) }()
	output = managedOutput
	if errors.Is(parseErr, flag.ErrHelp) {
		_, err = io.WriteString(output, commandHelp)
		return err
	}
	count, reachable, err := activeSessions(ctx, platform.Paths{RuntimeDir: config.runtimeDir})
	if reachable && err != nil {
		return fmt.Errorf("cannot list the broker's sessions: %w", err)
	}
	var text strings.Builder
	if reachable {
		prompt := fmt.Sprintf("Shut down Termivoa? This ends %d active session(s) and their process trees, "+
			"then stops the broker and the gateway. [y/N] ", count)
		confirmed, err := fileConfirmer{input: input, output: output}.Confirm(ctx, prompt)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err = io.WriteString(output, "Shutdown cancelled.\n")
			return err
		}
		ended, err := shutdownBroker(ctx, config.runtimeDir)
		if err != nil {
			return err
		}
		fmt.Fprintf(&text, "Ended %d session(s). Broker stopped.\n", ended)
	} else {
		text.WriteString("The broker is not running.\n")
	}
	stopped, err := stopGateway(ctx, config.runtimeDir)
	if err != nil {
		return errors.Join(writeString(output, text.String()), err)
	}
	if stopped {
		text.WriteString("Gateway stopped.\n")
	} else {
		text.WriteString("The gateway is not running.\n")
	}
	if !config.dev {
		supervisorNote(ctx, &text, lifecycle.Services)
	}
	return writeString(output, text.String())
}

func shutdownBroker(ctx context.Context, runtimeDir string) (int64, error) {
	client, err := localclient.DialAdmin(ctx, runtimeDir, buildID)
	if err != nil {
		return 0, fmt.Errorf("cannot connect to the managed-session broker: %w", err)
	}
	result, err := client.Shutdown(ctx)
	_ = client.Close()
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return 0, fmt.Errorf("shutdown outcome is uncertain—run pterm list to check the broker before trying again: %w", err)
		}
		return 0, err
	}
	// The broker removes its endpoint when its serve loop stops.
	err = waitFor(ctx, func() error {
		if _, _, err := ipc.ValidateBrokerEndpoint(ctx, runtimeDir, protocol.CLI, buildID); err == nil {
			return errors.New("the broker accepted shutdown but is still running")
		}
		return nil
	})
	return result.TerminatedSessions, err
}

func stopGateway(ctx context.Context, runtimeDir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	stopped, err := localclient.StopGateway(ctx, runtimeDir, buildID)
	if err != nil {
		return stopped, fmt.Errorf("stop the gateway: %w", err)
	}
	return stopped, nil
}

// supervisorNote explains startup registration for the given services. A
// clean exit is not restarted immediately, but the supervisor starts the
// service again at the next login. pterm prints, never runs, the unload.
func supervisorNote(ctx context.Context, text *strings.Builder, services []lifecycle.Service) {
	env, err := loadLifecycleEnv()
	if err != nil {
		return
	}
	statuses, err := env.host.Inspect(ctx, env.runner)
	if err != nil {
		return
	}
	for _, status := range statuses {
		for _, service := range services {
			if status.Registered && status.Service == service {
				fmt.Fprintf(text, "The %s is registered for startup, so its supervisor may start it again (at your next login, "+
					"or through pterm launch). To keep it stopped, unload it yourself; pterm setup --apply registers it again:\n  %s\n",
					service.Name, lifecycle.DisplayCommand(env.host.UnloadCommand(service)))
			}
		}
	}
}

func writeString(output io.Writer, text string) error {
	_, err := io.WriteString(output, text)
	return err
}
