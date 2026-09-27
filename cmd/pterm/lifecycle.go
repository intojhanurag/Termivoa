package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const devListenAddress = "127.0.0.1:8788"

var waitTimeout = 10 * time.Second // tests shorten it

// lifecycleEnv holds everything the lifecycle commands touch outside the
// process, so tests can substitute a temporary home and a fake runner.
type lifecycleEnv struct {
	host      lifecycle.Host
	paths     host.Paths
	runner    lifecycle.Runner
	gateway   string // gateway base URL without trailing slash
	tailscale string // tailscale CLI path, empty when not installed
}

var loadLifecycleEnv = func() (lifecycleEnv, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return lifecycleEnv{}, fmt.Errorf("startup registration supports macOS and Windows, not %s", runtime.GOOS)
	}
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		return lifecycleEnv{}, fmt.Errorf("locate pterm executable: %w", err)
	}
	paths, err := host.DefaultPaths()
	if err != nil {
		return lifecycleEnv{}, err
	}
	target := lifecycle.Host{OS: runtime.GOOS, Executable: executable, DataDir: paths.DataDir}
	if target.HomeDir, err = os.UserHomeDir(); err != nil {
		return lifecycleEnv{}, err
	}
	if runtime.GOOS == "darwin" {
		target.UID = os.Getuid()
	} else {
		current, err := user.Current()
		if err != nil {
			return lifecycleEnv{}, err
		}
		target.User = current.Username
	}
	tailscale, _ := exec.LookPath("tailscale")
	if tailscale == "" {
		tailscale = installedTailscale(runtime.GOOS, os.Getenv)
	}
	return lifecycleEnv{
		host: target, paths: paths, runner: lifecycle.ExecRunner{},
		gateway: strings.TrimSuffix(lifecycle.LocalURL, "/"), tailscale: tailscale,
	}, nil
}

// installedTailscale is the Tailscale CLI its installer puts off PATH, or "".
func installedTailscale(goos string, getenv func(string) string) string {
	var path string
	switch goos {
	case "darwin":
		path = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	case "windows":
		if programFiles := getenv("ProgramFiles"); filepath.IsAbs(programFiles) {
			path = filepath.Join(programFiles, "Tailscale", "tailscale.exe")
		}
	}
	if info, err := os.Stat(path); path == "" || err != nil || info.IsDir() {
		return ""
	}
	return path
}

type hostReport struct {
	statuses  []lifecycle.Status
	tailscale tailscaleReport
	broker    bool  // answered, or holds its data or runtime lock
	sessions  int   // active sessions of a broker that answered
	listErr   error // set when a running broker's sessions are unknown
	problems  int
}

// inspectHost performs only read-only checks and prints them.
func inspectHost(ctx context.Context, env lifecycleEnv, output io.Writer) (hostReport, error) {
	var report hostReport
	problem := func(format string, values ...any) {
		report.problems++
		fmt.Fprintf(output, "  ! "+format+"\n", values...)
	}
	fmt.Fprintln(output, "Checks (read-only):")
	fmt.Fprintf(output, "  pterm: %s (build %s)\n", env.host.Executable, buildID)
	for _, message := range lifecycle.ExecutableProblems(env.host.OS, env.host.Executable) {
		problem("%s", message)
	}
	for _, directory := range []struct{ name, path string }{{"data", env.paths.DataDir}, {"runtime", env.paths.RuntimeDir}} {
		state := "exists"
		if _, err := os.Stat(directory.path); errors.Is(err, os.ErrNotExist) {
			state = "not created yet"
		}
		fmt.Fprintf(output, "  %s directory: %s (%s)\n", directory.name, directory.path, state)
	}

	report.sessions, report.broker, report.listErr = activeSessions(ctx, env.paths)
	switch {
	case !report.broker:
		problem("broker: not running")
	case report.listErr != nil:
		problem("broker: %v", report.listErr)
	default:
		fmt.Fprintf(output, "  broker: running, %d active session(s)\n", report.sessions)
	}
	if err := gatewayHealthy(ctx, env.gateway); err != nil {
		problem("gateway: %s is not healthy: %v", env.gateway, err)
	} else {
		fmt.Fprintf(output, "  gateway: %s healthy\n", env.gateway)
	}

	statuses, err := env.host.Inspect(ctx, env.runner)
	if err != nil {
		return report, err
	}
	report.statuses = statuses
	for _, status := range report.statuses {
		switch {
		case status.Registered && status.FileMatches:
			fmt.Fprintf(output, "  startup %s: registered for this pterm\n", status.Service.Name)
		case status.Registered:
			problem("startup %s: registered, but not for this pterm (%s)", status.Service.Name, status.File)
		case status.Disabled:
			problem("startup %s: registered, but disabled", status.Service.Name)
		default:
			problem("startup %s: not registered", status.Service.Name)
		}
	}
	fmt.Fprintf(output, "  shells: %s\n", strings.Join(detectShells(), ", "))
	var problems int
	report.tailscale, problems = inspectTailscale(ctx, env.tailscale, output)
	report.problems += problems
	return report, nil
}

func detectShells() []string {
	candidates := []string{os.Getenv("SHELL"), "/bin/zsh"}
	if runtime.GOOS == "windows" {
		candidates = []string{"powershell.exe", "pwsh.exe"}
	}
	var found []string
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); candidate != "" && err == nil && !containsString(found, path) {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		return []string{"none found"}
	}
	return found
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

// tailscaleRun runs the tailscale CLI and returns its standard output. With
// stream set, output goes there instead: `tailscale serve` may print a link
// to enable Serve and wait. Tests replace it.
var tailscaleRun = func(ctx context.Context, cli string, stream io.Writer, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, cli, args...)
	if stream != nil {
		command.Stdout, command.Stderr = stream, stream
		return nil, command.Run()
	}
	return command.Output()
}

// tailscaleReport is what the read-only Tailscale checks found.
type tailscaleReport struct {
	installed bool
	status    lifecycle.TailscaleStatus
	statusErr error
	serveErr  error
	serve     lifecycle.ServeStatus // 443 (phone) and 8443 (preview) findings; never overwritten
}

// phoneOrigin is the private HTTPS origin Tailscale gives this computer. When
// there is none, problem says why and what the owner does next.
func (report tailscaleReport) phoneOrigin() (origin, problem string) {
	again := ", then run `pterm setup` again."
	switch {
	case !report.installed:
		return "", "Tailscale is not installed. Install it from https://tailscale.com/download and sign in" + again
	case report.statusErr != nil:
		return "", "Tailscale did not report its status. Open Tailscale and sign in" + again
	case report.status.BackendState != "Running":
		return "", "Tailscale is not connected (" + report.status.BackendState + "). Run `tailscale up` or sign in from the Tailscale app" + again
	case report.status.DNSName == "":
		return "", "Tailscale has no DNS name for this computer. Turn on MagicDNS and HTTPS certificates in the Tailscale admin console" + again
	}
	origin = "https://" + strings.ToLower(report.status.DNSName)
	if protocol.ValidatePairingOrigin(origin) != nil {
		return "", "Tailscale reported an unusable DNS name " + strconv.Quote(report.status.DNSName) + "; set the address yourself with `pterm config origin https://<host>`."
	}
	return origin, ""
}

// previewOrigin is the dev-server preview origin: the same tailnet host as
// origin, on the separate HTTPS 8443 Serve port.
func previewOrigin(origin string) string {
	return origin + ":8443"
}

// inspectTailscale runs only `status --json` and `serve status --json`.
func inspectTailscale(ctx context.Context, cli string, output io.Writer) (report tailscaleReport, problems int) {
	if cli == "" {
		fmt.Fprintln(output, "  ! tailscale: not installed (required for phone access)")
		return report, 1
	}
	report.installed = true
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tailscaleRun(ctx, cli, nil, args...)
	}
	data, err := run("status", "--json")
	status, parseErr := lifecycle.ParseTailscaleStatus(data)
	if report.statusErr = errors.Join(err, parseErr); report.statusErr != nil {
		fmt.Fprintf(output, "  ! tailscale: status unavailable: %v\n", report.statusErr)
		return report, 1
	}
	report.status = status
	fmt.Fprintf(output, "  tailscale: %s, host %s\n", status.BackendState, status.DNSName)
	if status.BackendState != "Running" {
		problems++
	}
	data, err = run("serve", "status", "--json")
	report.serve, parseErr = lifecycle.ServeFindings(data)
	switch {
	case err != nil || parseErr != nil:
		report.serveErr = errors.Join(err, parseErr)
		fmt.Fprintf(output, "  ! tailscale serve: status unavailable: %v\n", report.serveErr)
		return report, problems + 1
	case report.serve.Configured:
		fmt.Fprintln(output, "  tailscale serve: HTTPS 443 proxies the Phone Terminal gateway")
	default:
		fmt.Fprintln(output, "  ! tailscale serve: not configured for Phone Terminal")
		problems++
	}
	for _, finding := range report.serve.Problems {
		fmt.Fprintf(output, "  ! tailscale serve: %s\n", finding)
	}
	problems += len(report.serve.Problems)

	// Dev-server previews (HTTPS 8443) are optional: unconfigured or
	// conflicting never fails doctor, since only Phone Terminal itself (443)
	// is required.
	switch {
	case report.serve.PreviewConfigured:
		fmt.Fprintln(output, "  tailscale serve: HTTPS 8443 proxies dev-server previews")
	case len(report.serve.PreviewProblems) == 0:
		fmt.Fprintf(output, "  tailscale serve: dev-server previews not shared (optional): %s\n", lifecycle.PreviewServeCommand)
	}
	for _, finding := range report.serve.PreviewProblems {
		fmt.Fprintf(output, "  ! tailscale serve: %s\n", finding)
	}
	return report, problems
}

// activeSessions reports whether a broker is running and how many of its
// sessions are not yet exited or lost. It fails closed: a broker that holds
// its data or runtime lock but does not answer (hung, starting, or another
// protocol version) is running with err set. Only no answer and no held lock
// means no broker.
func activeSessions(ctx context.Context, paths host.Paths) (count int, running bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout+2*time.Second)
	defer cancel()
	client, dialErr := localclient.DialManagement(ctx, paths.RuntimeDir, buildID)
	if dialErr != nil {
		held, lockErr := ipc.BrokerLocksHeld(paths.DataDir, paths.RuntimeDir)
		switch {
		case lockErr != nil:
			return 0, true, fmt.Errorf("cannot tell whether a broker is running: %w", lockErr)
		case held:
			return 0, true, fmt.Errorf("a broker is running but did not answer (%v); %s first", dialErr, stopBrokerHint(paths.RuntimeDir))
		}
		return 0, false, nil
	}
	defer client.Close()
	var cursor *string
	for {
		page, err := client.ListPage(ctx, cursor, 100)
		if err != nil {
			return 0, true, fmt.Errorf("running, but its sessions could not be listed: %w", err)
		}
		for _, session := range page.Sessions {
			if session.State == protocol.Starting || session.State == protocol.Running || session.State == protocol.Exiting {
				count++
			}
		}
		if page.NextCursor == nil {
			return count, true, nil
		}
		cursor = page.NextCursor
	}
}

func (report hostReport) refuseActiveSessions(action string) error {
	if !report.broker {
		return nil
	}
	if report.listErr != nil {
		return fmt.Errorf("refusing to %s: cannot verify the broker's sessions: %w", action, report.listErr)
	}
	if report.sessions > 0 {
		return fmt.Errorf("refusing to %s: the broker has %d active session(s) that would end; finish them or run `pterm terminate <session-id>` first", action, report.sessions)
	}
	return nil
}

// stopBrokerHint says how to stop the broker, naming its recorded process ID
// as a last resort when it does not answer. The ID is diagnostic only.
func stopBrokerHint(runtimeDir string) string {
	descriptor, err := ipc.ReadEndpointDescriptor(runtimeDir)
	switch {
	case err != nil || descriptor.ProcessID <= 0:
		return "run `pterm shutdown`"
	case runtime.GOOS == "windows":
		return fmt.Sprintf("run `pterm shutdown` (if the broker does not answer, end pterm.exe process %d in Task Manager)", descriptor.ProcessID)
	default:
		return fmt.Sprintf("run `pterm shutdown` (if the broker does not answer, `kill %d`)", descriptor.ProcessID)
	}
}

// addressInUse reports whether the gateway's loopback address cannot be bound.
func addressInUse(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil {
		return true
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		return true
	}
	_ = listener.Close()
	return false
}

func gatewayHealthy(ctx context.Context, base string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return err
	}
	client := http.Client{Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return nil
}

func confirmLifecycle(ctx context.Context, input *os.File, output io.Writer, prompt string) error {
	if input == nil {
		return errors.New("confirmation requires terminal input; pass --yes to confirm non-interactively")
	}
	confirmed, err := fileConfirmer{input: input, output: output}.Confirm(ctx, prompt)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("cancelled; no changes made")
	}
	return nil
}

func parseLifecycleFlags(command string, args []string, bools ...string) (map[string]*bool, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := map[string]*bool{}
	for _, name := range bools {
		values[name] = flags.Bool(name, false, name)
	}
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%s options: %w", command, err)
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("%s does not accept positional arguments", command)
	}
	return values, nil
}

// runSetupCommand is the first-run wizard. It runs read-only checks, shows
// everything it will do, and asks once. Then it registers startup, waits for
// the broker and gateway, saves the Tailscale phone address, asks separately
// before it changes Tailscale Serve, adds a Home project if there is none, and
// pairs a phone. --apply --yes is the non-interactive installer path: it only
// registers startup and saves the phone address when no other one is set.
func runSetupCommand(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	options, err := parseLifecycleFlags("setup", args, "apply", "yes")
	if err != nil {
		return err
	}
	apply, yes := *options["apply"], *options["yes"]
	env, err := loadLifecycleEnv()
	if err != nil {
		return err
	}
	report, err := inspectHost(ctx, env, output)
	if err != nil {
		return err
	}
	plan, err := env.host.InstallPlan(report.statuses)
	if err != nil {
		return err
	}
	origin, originProblem := report.tailscale.phoneOrigin()
	describeSetup(env, plan, report.tailscale, origin, originProblem, yes, output)
	if !apply && input == nil {
		fmt.Fprintln(output, "\nNo changes made. Run `pterm setup` in a terminal, or `pterm setup --apply --yes` to register startup without questions.")
		return nil
	}
	if problems := lifecycle.ExecutableProblems(env.host.OS, env.host.Executable); len(problems) > 0 {
		return fmt.Errorf("refusing to register this pterm: %s", problems[0])
	}
	if len(plan.Steps) > 0 {
		// A registered copy of a process already running outside startup
		// registration would fail on the data lock or port and restart forever.
		if report.broker && !report.statuses[0].Registered {
			return fmt.Errorf("refusing to register startup: a broker is already running outside startup registration; finish its sessions, %s, then rerun `pterm setup`", stopBrokerHint(env.paths.RuntimeDir))
		}
		if !report.statuses[1].Registered && addressInUse(env.gateway) {
			return fmt.Errorf("refusing to register startup: %s is already in use (a gateway started outside startup registration, or another program); stop that process, then rerun `pterm setup`", env.gateway)
		}
		if plan.ChangesRunningBroker {
			if err := report.refuseActiveSessions("replace the broker registration"); err != nil {
				return err
			}
		}
	}
	if !yes {
		if err := confirmLifecycle(ctx, input, output, "\nContinue? [y/N] "); err != nil {
			return err
		}
	}
	if len(plan.Steps) > 0 {
		fmt.Fprintln(output, "\nRegistering startup:")
		if err := lifecycle.Apply(ctx, env.runner, plan, output); err != nil {
			return err
		}
	}

	fmt.Fprintln(output, "\nWaiting for the broker...")
	brokerUp := func() error {
		ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout)
		defer cancel()
		_, _, err := ipc.ValidateBrokerEndpoint(ctx, env.paths.RuntimeDir, protocol.CLI, buildID)
		return err
	}
	if err := waitWithin(ctx, 3*waitTimeout, brokerUp); err != nil {
		if yes {
			fmt.Fprintf(output, "The broker does not answer yet (%v). It starts at your next login; then run `pterm setup` to pair your phone.\n", err)
			return nil
		}
		return fmt.Errorf("the broker did not start (see %s): %w", env.host.LogDir(), err)
	}
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		return fmt.Errorf("cannot connect to the broker: %w", err)
	}
	defer admin.Close()
	if origin != "" {
		if err := savePhoneOrigin(ctx, admin, origin, output); err != nil {
			return err
		}
	}
	if yes {
		fmt.Fprintln(output, "\nStartup is registered. To pair your phone, run `pterm setup` (or `pterm pair`) in a terminal.")
		return nil
	}
	fmt.Fprintln(output, "Waiting for the gateway...")
	if err := waitWithin(ctx, 3*waitTimeout, func() error { return gatewayHealthy(ctx, env.gateway) }); err != nil {
		return fmt.Errorf("the gateway did not start (see %s): %w", env.host.LogDir(), err)
	}
	confirmer := fileConfirmer{input: input, output: output}
	if origin != "" {
		phoneServeConfigured, err := offerTailscaleServe(ctx, env, report.tailscale, origin, confirmer, output)
		if err != nil {
			return err
		}
		if report.tailscale.serveErr == nil && phoneServeConfigured {
			if err := offerTailscalePreviewServe(ctx, env, report.tailscale, origin, confirmer, output); err != nil {
				return err
			}
		}
	}
	projects, err := admin.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		name := "Home"
		if err := cli.ProjectAdd(ctx, admin, env.host.HomeDir, &name, output); err != nil {
			return err
		}
	}
	fmt.Fprintln(output, "\nPair your phone:")
	file, isFile := output.(*os.File)
	return cli.Pair(ctx, admin, cli.PairOptions{
		Fallback: env.gateway, ShowQR: isFile && term.IsTerminal(file.Fd()), Confirmer: confirmer,
	}, output)
}

func describeSetup(env lifecycleEnv, plan lifecycle.Plan, tailscale tailscaleReport, origin, originProblem string,
	yes bool, output io.Writer,
) {
	fmt.Fprintln(output, "\npterm setup will:")
	fmt.Fprintln(output, "1. Start Phone Terminal when you log in (current user only, no administrator rights):")
	if len(plan.Steps) == 0 {
		fmt.Fprintln(output, "  already registered for this pterm; nothing to change.")
	} else {
		_ = lifecycle.Describe(plan, output)
	}
	fmt.Fprintln(output, "2. Wait until the broker and the gateway answer.")
	if origin == "" {
		fmt.Fprintf(output, "3. Skip the phone address: %s\n   Until then, only a browser on this computer can pair.\n", originProblem)
	} else {
		fmt.Fprintf(output, "3. Save the phone address %s (unless you set another one).\n", origin)
	}
	if yes {
		fmt.Fprintf(output, "Phone access over your tailnet (run it yourself): %s\n", lifecycle.ServeCommand)
		fmt.Fprintf(output, "Dev-server previews over your tailnet (run it yourself): %s\n", lifecycle.PreviewServeCommand)
	} else {
		fmt.Fprintf(output, "4. Add your home folder %s as project \"Home\" if you have no project.\n", env.host.HomeDir)
		fmt.Fprintln(output, "5. Show a QR code for your phone and ask you to approve the code it shows.")
		asksPhoneServe := origin != "" && tailscale.serveErr == nil && !tailscale.serve.Configured && len(tailscale.serve.Problems) == 0
		if asksPhoneServe {
			fmt.Fprintf(output, "Before step 4 it asks separately whether to run: %s\n", lifecycle.ServeCommand)
		}
		// The second, separate preview question follows only once 443 is
		// settled successfully: already configured, or about to be asked (and,
		// if agreed to, applied) above.
		if origin != "" && tailscale.serveErr == nil && !tailscale.serve.PreviewConfigured && len(tailscale.serve.PreviewProblems) == 0 &&
			(tailscale.serve.Configured || asksPhoneServe) {
			fmt.Fprintf(output, "Before step 4 it also asks separately whether to run: %s\n", lifecycle.PreviewServeCommand)
		}
	}
	fmt.Fprintln(output, "Never use Tailscale Funnel: it would put your terminal on the public internet.")
}

// savePhoneOrigin saves origin unless the owner already chose another one.
func savePhoneOrigin(ctx context.Context, admin *localclient.AdminClient, origin string, output io.Writer) error {
	current, err := admin.PairingOrigin(ctx, nil)
	if err != nil {
		return err
	}
	switch {
	case current == nil:
		return cli.ConfigOrigin(ctx, admin, &origin, output)
	case *current == origin:
		fmt.Fprintf(output, "Phone address: %s\n", origin)
	default:
		fmt.Fprintf(output, "Phone address stays %s. Tailscale names this computer %s; to use that, run `pterm config origin %s`.\n",
			*current, origin, origin)
	}
	return nil
}

// offerTailscaleServe runs `tailscale serve` only after its own explicit yes,
// and only when HTTPS 443 serves nothing else and Funnel is off. configured
// reports whether HTTPS 443 proxies the gateway afterwards (already did, or
// this run made it so), which gates whether the separate preview question
// in offerTailscalePreviewServe is asked at all.
func offerTailscaleServe(ctx context.Context, env lifecycleEnv, tailscale tailscaleReport, origin string,
	confirmer fileConfirmer, output io.Writer,
) (configured bool, err error) {
	switch {
	case tailscale.serveErr != nil:
		fmt.Fprintf(output, "Could not read the Tailscale Serve status; run it yourself: %s\n", lifecycle.ServeCommand)
		return false, nil
	case tailscale.serve.Configured:
		fmt.Fprintf(output, "Tailscale Serve already shares the page at %s/\n", origin)
		return true, nil
	case len(tailscale.serve.Problems) > 0:
		fmt.Fprintln(output, "Tailscale Serve was not changed:")
		for _, finding := range tailscale.serve.Problems {
			fmt.Fprintf(output, "  ! %s\n", finding)
		}
		fmt.Fprintln(output, "Resolve this yourself, then run `pterm setup` again. Until then, only a browser on this computer can pair.")
		return false, nil
	}
	approved, err := confirmer.Confirm(ctx, fmt.Sprintf(
		"\nShare the page with your tailnet devices at %s/ ?\nThis runs: %s\nOnly devices in your tailnet can reach it; it never uses Funnel. [y/N] ",
		origin, lifecycle.ServeCommand))
	if err != nil {
		return false, err
	}
	if !approved {
		fmt.Fprintf(output, "Tailscale Serve not changed. To share the page later, run: %s\n", lifecycle.ServeCommand)
		return false, nil
	}
	serveCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := tailscaleRun(serveCtx, env.tailscale, output, strings.Fields(lifecycle.ServeCommand)[1:]...); err != nil {
		fmt.Fprintf(output, "tailscale serve failed (%v). Fix it, then run it yourself: %s\n", err, lifecycle.ServeCommand)
		return false, nil
	}
	fmt.Fprintf(output, "Tailscale Serve shares the page at %s/\n", origin)
	return true, nil
}

// offerTailscalePreviewServe asks a second, separate consent to run
// `tailscale serve` for the dev-server preview listener on HTTPS 8443, a
// separate origin from the phone terminal's 443. It never runs without its
// own explicit yes, and only when HTTPS 8443 serves nothing else.
func offerTailscalePreviewServe(ctx context.Context, env lifecycleEnv, tailscale tailscaleReport, origin string,
	confirmer fileConfirmer, output io.Writer,
) error {
	preview := previewOrigin(origin)
	switch {
	case tailscale.serve.PreviewConfigured:
		fmt.Fprintf(output, "Tailscale Serve already shares dev-server previews at %s/\n", preview)
		return nil
	case len(tailscale.serve.PreviewProblems) > 0:
		fmt.Fprintln(output, "Tailscale Serve was not changed for dev-server previews:")
		for _, finding := range tailscale.serve.PreviewProblems {
			fmt.Fprintf(output, "  ! %s\n", finding)
		}
		fmt.Fprintln(output, "Resolve this yourself, then run `pterm setup` again.")
		return nil
	}
	approved, err := confirmer.Confirm(ctx, fmt.Sprintf(
		"\nAlso share dev-server previews with your tailnet devices at %s/ ?\nThis runs: %s\nPreviews are a separate origin; only devices in your tailnet can reach them; it never uses Funnel. [y/N] ",
		preview, lifecycle.PreviewServeCommand))
	if err != nil {
		return err
	}
	if !approved {
		fmt.Fprintf(output, "Dev-server previews not shared. To share them later, run: %s\n", lifecycle.PreviewServeCommand)
		return nil
	}
	serveCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := tailscaleRun(serveCtx, env.tailscale, output, strings.Fields(lifecycle.PreviewServeCommand)[1:]...); err != nil {
		fmt.Fprintf(output, "tailscale serve failed (%v). Fix it, then run it yourself: %s\n", err, lifecycle.PreviewServeCommand)
		return nil
	}
	fmt.Fprintf(output, "Dev-server previews shared at %s/\n", preview)
	return nil
}

func runDoctorCommand(ctx context.Context, args []string, output io.Writer) error {
	if _, err := parseLifecycleFlags("doctor", args); err != nil {
		return err
	}
	env, err := loadLifecycleEnv()
	if err != nil {
		return err
	}
	report, err := inspectHost(ctx, env, output)
	if err != nil {
		return err
	}
	if report.problems > 0 {
		return fmt.Errorf("doctor found %d problem(s); `pterm setup` explains how to fix startup registration", report.problems)
	}
	fmt.Fprintln(output, "No problems found.")
	return nil
}

func runUninstallCommand(ctx context.Context, args []string, input *os.File, output io.Writer) error {
	options, err := parseLifecycleFlags("uninstall", args, "yes", "delete-data", "dry-run")
	if err != nil {
		return err
	}
	env, err := loadLifecycleEnv()
	if err != nil {
		return err
	}
	report, err := inspectHost(ctx, env, output)
	if err != nil {
		return err
	}
	var data []string
	if *options["delete-data"] {
		data = append(data, env.paths.DataDir)
		if cache := filepath.Dir(env.paths.RuntimeDir); cache != env.paths.DataDir {
			data = append(data, cache)
		}
	}
	plan, err := env.host.UninstallPlan(report.statuses, data)
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "\nUninstall removes this user's startup registration:")
	if len(plan.Steps) == 0 {
		fmt.Fprintln(output, "  nothing is registered.")
	} else if err := lifecycle.Describe(plan, output); err != nil {
		return err
	}
	if len(data) == 0 {
		fmt.Fprintln(output, "Your data (paired devices, projects, settings, logs) is kept; --delete-data removes it.")
	} else {
		fmt.Fprintln(output, "--delete-data permanently deletes paired devices, projects, settings and logs.")
	}
	if env.host.OS == "windows" && len(plan.Steps) > 0 {
		fmt.Fprintln(output, "Then it stops the gateway and, if it has no active sessions, the broker.")
	}
	fmt.Fprintln(output, "The application itself stays: move Phone Terminal.app to the Trash, or remove it in Windows Settings > Apps.")

	if plan.ChangesRunningBroker || len(data) > 0 {
		if err := report.refuseActiveSessions("uninstall"); err != nil {
			return err
		}
	}
	refuseDelete := fmt.Errorf("refusing to delete data: a broker is running that removing startup registration does not stop; finish its sessions, %s, then rerun `pterm uninstall --delete-data`", stopBrokerHint(env.paths.RuntimeDir))
	if len(data) > 0 && report.broker && !report.statuses[0].Registered && !report.statuses[0].Disabled {
		return refuseDelete
	}
	if *options["dry-run"] || len(plan.Steps) == 0 {
		return nil
	}
	if !*options["yes"] {
		if err := confirmLifecycle(ctx, input, output, "\nRemove these entries? [y/N] "); err != nil {
			return err
		}
	}
	fmt.Fprintln(output, "\nApplying:")
	// UninstallPlan puts data deletion last. Delete only once no broker
	// runs: registration does not prove the live broker was the one stopped.
	unregister := len(plan.Steps) - len(data)
	if err := lifecycle.Apply(ctx, env.runner, lifecycle.Plan{Steps: plan.Steps[:unregister]}, output); err != nil {
		return err
	}
	if env.host.OS == "windows" {
		if err := stopUnregistered(ctx, env, report, output); err != nil {
			return err
		}
	}
	if len(data) == 0 {
		return nil
	}
	stopped := func() error {
		if _, running, _ := activeSessions(ctx, env.paths); running {
			return refuseDelete
		}
		return nil
	}
	if err := waitFor(ctx, stopped); err != nil {
		return err
	}
	return lifecycle.Apply(ctx, env.runner, lifecycle.Plan{Steps: plan.Steps[unregister:]}, output)
}

// stopUnregistered stops what the removed Windows tasks started: schtasks
// /End ends the headless console host, which need not end pterm. The broker is
// only stopped when uninstall already checked it had no active sessions, and
// it is checked again because the gateway ran until now.
func stopUnregistered(ctx context.Context, env lifecycleEnv, report hostReport, output io.Writer) error {
	broker, gateway := report.statuses[0], report.statuses[1]
	if gateway.Registered || gateway.Disabled {
		fmt.Fprintln(output, "  stop the gateway")
		if _, err := stopGateway(ctx, env.paths.RuntimeDir); err != nil {
			return err
		}
	}
	if !broker.Registered && !broker.Disabled {
		return nil
	}
	count, running, err := activeSessions(ctx, env.paths)
	switch {
	case !running:
		return nil
	case err != nil:
		return fmt.Errorf("the broker keeps running: %w", err)
	case count > 0:
		return fmt.Errorf("the broker keeps running: it has %d active session(s) now; finish them, then run `pterm shutdown`", count)
	}
	fmt.Fprintln(output, "  shut down the broker")
	_, err = shutdownBroker(ctx, env.paths.RuntimeDir)
	return err
}

func runLaunchCommand(ctx context.Context, args []string, output io.Writer) error {
	options, err := parseLifecycleFlags("launch", args, "no-open")
	if err != nil {
		return err
	}
	env, err := loadLifecycleEnv()
	if err != nil {
		return err
	}
	statuses, err := env.host.Inspect(ctx, env.runner)
	if err != nil {
		return err
	}
	checks := []func() error{
		func() error {
			ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout)
			defer cancel()
			_, _, err := ipc.ValidateBrokerEndpoint(ctx, env.paths.RuntimeDir, protocol.CLI, buildID)
			return err
		},
		func() error { return gatewayHealthy(ctx, env.gateway) },
	}
	for index, status := range statuses {
		if checks[index]() == nil {
			continue
		}
		if err := startDetached(ctx, env, status); err != nil {
			return err
		}
		if err := waitFor(ctx, checks[index]); err != nil {
			return fmt.Errorf("%s did not start (see %s): %w", status.Service.Name, env.host.LogDir(), err)
		}
	}
	url := env.gateway + "/"
	fmt.Fprintf(output, "Phone Terminal is running: %s\n", url)
	if *options["no-open"] {
		return nil
	}
	command := []string{"open", url}
	if runtime.GOOS == "windows" {
		command = []string{"rundll32", "url.dll,FileProtocolHandler", url}
	}
	_, err = env.runner.Run(ctx, command[0], command[1:]...)
	return err
}

// startDetached asks the supervisor for a registered service, otherwise
// starts it in its own session with output appended to its log.
func startDetached(ctx context.Context, env lifecycleEnv, status lifecycle.Status) error {
	if status.Registered {
		command := env.host.StartCommand(status.Service)
		_, err := env.runner.Run(ctx, command[0], command[1:]...)
		return err
	}
	logDir, err := ipc.EnsurePrivateDirectory(env.host.LogDir())
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(logDir, status.Service.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	child := exec.Command(env.host.Executable, status.Service.Mode)
	child.Stdout, child.Stderr = log, log
	child.SysProcAttr = lifecycle.DetachedAttributes()
	if err := child.Start(); err != nil {
		return fmt.Errorf("start %s: %w", status.Service.Name, err)
	}
	return child.Process.Release()
}

func waitFor(ctx context.Context, check func() error) error {
	return waitWithin(ctx, waitTimeout, check)
}

func waitWithin(ctx context.Context, timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// runUpCommand is the development harness. Broker and gateway run as two
// children in separate process groups: a signal to the gateway's group can
// never reach the broker. Ctrl+C stops the gateway, then the broker.
func runUpCommand(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dev := flags.Bool("dev", false, "use the isolated development profile")
	dataDir := flags.String("data-dir", "", "development data directory")
	runtimeDir := flags.String("runtime-dir", "", "development runtime directory")
	listen := flags.String("listen", devListenAddress, "development gateway loopback address")
	previewListen := flags.String("preview-listen", gateway.DevelopmentPreviewListenAddress, `development dev-server preview address, or "off"`)
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("up options: %w", err)
	}
	if !*dev || flags.NArg() != 0 {
		return errors.New("usage: pterm up --dev [--data-dir <dir>] [--runtime-dir <dir>] [--listen <addr>] [--preview-listen <addr>|off]; installed use relies on `pterm setup`")
	}
	customRuntime := *runtimeDir != ""
	if !customRuntime {
		development, err := host.DevelopmentPaths()
		if err != nil {
			return err
		}
		*runtimeDir = development.RuntimeDir
	}
	validated, err := host.ValidateRuntimeDir(host.DevelopmentProfile, *runtimeDir)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Closing the terminal must stop both children too: they are in their
	// own process groups and would not receive the hangup themselves.
	ctx, stopHangup := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stopHangup()
	brokerArgs := []string{brokerMode, "--dev", "--runtime-dir", validated}
	if *dataDir != "" {
		brokerArgs = append(brokerArgs, "--data-dir", *dataDir)
	}
	gatewayArgs := []string{gatewayMode, "--dev", "--runtime-dir", validated, "--listen", *listen, "--preview-listen", *previewListen}

	type exited struct {
		name string
		err  error
	}
	done := make(chan exited, 2)
	processes := map[string]*os.Process{}
	var order []string // start order; stopped in reverse
	start := func(name string, args []string) error {
		child := exec.Command(executable, args...)
		child.Stdout, child.Stderr = os.Stderr, os.Stderr
		child.SysProcAttr = lifecycle.GroupAttributes()
		if err := child.Start(); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		processes[name] = child.Process
		order = append(order, name)
		go func() { done <- exited{name, child.Wait()} }()
		fmt.Fprintf(output, "%s started (pid %d)\n", name, child.Process.Pid)
		return nil
	}
	reap := func(result exited) {
		delete(processes, result.name)
		fmt.Fprintf(output, "%s exited: %s\n", result.name, exitText(result.err))
	}
	stopAll := func() {
		for index := len(order) - 1; index >= 0; index-- {
			process, running := processes[order[index]]
			if !running {
				continue
			}
			_ = lifecycle.Interrupt(process)
			timer := time.AfterFunc(5*time.Second, func() { _ = process.Kill() })
			for processes[order[index]] != nil {
				reap(<-done)
			}
			timer.Stop()
		}
	}

	if err := start("broker", brokerArgs); err != nil {
		return err
	}
	brokerReady := func() error {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, _, err := ipc.ValidateBrokerEndpoint(ctx, validated, protocol.CLI, buildID)
		return err
	}
	if err := waitFor(ctx, brokerReady); err != nil {
		stopAll()
		return fmt.Errorf("development broker did not become ready: %w", err)
	}
	if err := start("gateway", gatewayArgs); err != nil {
		stopAll()
		return err
	}
	base := "http://" + *listen
	if err := waitFor(ctx, func() error { return gatewayHealthy(ctx, base) }); err != nil {
		stopAll()
		return fmt.Errorf("development gateway did not become healthy: %w", err)
	}
	pair := "pterm pair --dev"
	if customRuntime {
		pair += " --runtime-dir " + lifecycle.DisplayCommand([]string{validated})
	}
	if *listen != devListenAddress {
		pair += " --origin " + base
	}
	fmt.Fprintf(output, "Development gateway: %s/\nPair a browser: %s\nPress Ctrl+C to stop both processes.\n", base, pair)

	var exitErr error
	for len(processes) > 0 {
		select {
		case <-ctx.Done():
			stopAll()
			return nil
		case result := <-done:
			reap(result)
			if result.err != nil {
				exitErr = errors.Join(exitErr, fmt.Errorf("%s: %w", result.name, result.err))
			}
		}
	}
	// Both exiting cleanly is the normal result of pterm shutdown.
	if exitErr != nil {
		return fmt.Errorf("development processes exited: %w", exitErr)
	}
	return nil
}

func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
