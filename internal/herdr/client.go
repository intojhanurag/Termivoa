package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

const (
	maxLineBytes   = 4 << 20 // one socket reply
	maxOutputBytes = 64 << 10
)

// commandTimeout is the time limit of one herdr CLI run. A test seam.
var commandTimeout = 3 * time.Second

var (
	errDeadline    = os.ErrDeadlineExceeded
	errUnsupported = errors.New("herdr: snapshot cannot be read")
	requestCounter atomic.Uint64
	kindPattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)
)

// callError is herdr's error reply. Its text may name local paths (git
// output): it is used to classify, never shown or logged.
type callError struct{ code, message string }

func (err *callError) Error() string { return "herdr: " + err.code }

// socketRefused: the socket exists but fails design S3. reason is for the
// owner (pterm doctor, the broker log): it names local paths and the fix.
type socketRefused struct{ reason string }

func (err *socketRefused) Error() string { return "herdr: will not use the socket: " + err.reason }

// call sends one request on its own connection (herdr closes the socket
// after each reply) and decodes result. Every call checks the socket first.
func (provider *Provider) call(ctx context.Context, timeout time.Duration, method string, params, result any) error {
	path := provider.config.socketPath()
	if err := checkSocket(path); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := ipc.VerifyPeer(conn); err != nil {
		return &socketRefused{"its server is not this user's: " + err.Error()}
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	request, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{ID: "termivoa-" + strconv.FormatUint(requestCounter.Add(1), 10), Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return err
	}
	line, err := readLine(conn)
	if err != nil {
		return err
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		if !bytes.HasSuffix(line, []byte("\n")) {
			return io.ErrUnexpectedEOF // herdr went away in the middle of a reply: not running, not "too old"
		}
		return fmt.Errorf("herdr: malformed reply: %w", err)
	}
	if reply.Error != nil {
		return &callError{code: reply.Error.Code, message: reply.Error.Message}
	}
	if len(reply.Result) == 0 {
		return errors.New("herdr: reply has no result")
	}
	return json.Unmarshal(reply.Result, result)
}

// readLine reads one newline-terminated reply of at most maxLineBytes.
func readLine(conn net.Conn) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(conn, maxLineBytes+1))
	line, err := reader.ReadBytes('\n')
	if len(line) > maxLineBytes {
		return nil, errors.New("herdr: reply is larger than 4 MiB")
	}
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return nil, err
	}
	return line, nil
}

type pong struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

func (provider *Provider) ping(ctx context.Context) (pong, error) {
	var result pong
	err := provider.call(ctx, callTimeout, "ping", struct{}{}, &result)
	return result, err
}

func (provider *Provider) readSnapshot(ctx context.Context) (wireSnapshot, error) {
	var result struct {
		Snapshot *wireSnapshot `json:"snapshot"`
	}
	if err := provider.call(ctx, callTimeout, "session.snapshot", struct{}{}, &result); err != nil {
		var syntax *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &typeErr) {
			return wireSnapshot{}, errUnsupported
		}
		return wireSnapshot{}, err
	}
	if result.Snapshot == nil || !result.Snapshot.complete() {
		return wireSnapshot{}, errUnsupported
	}
	return *result.Snapshot, nil
}

// findBinary returns the first BinaryPaths entry, then the first herdr on
// PathEnv, that passes tailnet.CheckBinary; "" when none does. It also
// remembers whether a herdr was refused.
func (provider *Provider) findBinary() string {
	path, refused := FindBinary(provider.config)
	provider.refusedAny = len(refused) > 0
	return path
}

func (provider *Provider) findBinaryAgain() bool {
	return provider.binary != "" && tailnet.CheckBinary(provider.binary) == nil
}

// FindBinary also returns why candidates were refused (for pterm doctor).
func FindBinary(config Config) (path string, refused []string) {
	var candidates []string
	for _, candidate := range config.BinaryPaths {
		if filepath.IsAbs(candidate) && !slices.Contains(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	for _, folder := range filepath.SplitList(config.PathEnv) {
		if candidate := filepath.Join(folder, "herdr"); filepath.IsAbs(folder) && !slices.Contains(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range candidates {
		err := tailnet.CheckBinary(candidate)
		switch {
		case err == nil:
			return candidate, refused
		case !errors.Is(err, os.ErrNotExist):
			refused = append(refused, fmt.Sprintf("herdr at %s: %v; Termivoa will not run it", candidate, err))
		}
	}
	return "", refused
}

// run runs the herdr CLI with the fixed environment, a time limit, an
// output cap, standard input from the null device and no controlling
// terminal, and returns its standard output. Never a shell. A run that the
// time limit (or ctx) stopped returns the context's error.
func (provider *Provider) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := tailnet.CheckBinary(provider.binary); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	argv := append(provider.command(provider.binary), args...)
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Env = provider.environment(false)
	command.Dir = provider.config.Home
	command.WaitDelay = time.Second
	noTerminal(command)
	output := &cappedBuffer{}
	command.Stdout, command.Stderr = output, io.Discard
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return output.Bytes(), err
}

type cappedBuffer struct{ bytes.Buffer }

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	if room := maxOutputBytes - buffer.Len(); room > 0 {
		buffer.Buffer.Write(data[:min(len(data), room)])
	}
	return len(data), nil
}

// probe reads what this herdr build can do: `terminal attach` (tabs without
// an agent can open) and the kinds of `agent start` (the M2 agent picker).
// Only the help text counts, never the exit status: herdr 0.8.2 answers
// `--help` after an unknown command with its general help and exit 0, and its
// default command starts the herdr UI, which run's missing terminal and time
// limit stop. A run that did not end by itself (the time limit on a loaded
// computer, or it did not start) says nothing about this herdr: its part of
// last stays, and ended is false so that the caller asks again.
func (provider *Provider) probe(ctx context.Context, last capabilities) (caps capabilities, ended bool) {
	caps = last
	attach, attachErr := provider.run(ctx, "terminal", "attach", "--help")
	if ranToItsEnd(attachErr) {
		caps.attachAny = bytes.Contains(attach, []byte("Usage: herdr terminal attach"))
	}
	help, helpErr := provider.run(ctx, "agent", "start", "--help")
	if ranToItsEnd(helpErr) {
		caps.kinds = parseKinds(help)
	}
	return caps, ranToItsEnd(attachErr) && ranToItsEnd(helpErr)
}

// ranToItsEnd: the command ran and exited by itself, with any status. A run
// that a signal ended (exit code -1) did not.
func ranToItsEnd(err error) bool {
	var exit *exec.ExitError
	return err == nil || errors.As(err, &exit) && exit.ExitCode() >= 0
}

// parseKinds reads the "[possible values: a, b]" list of `agent start --help`.
// A missing list means no kinds: M2 then offers no agent.
func parseKinds(help []byte) []string {
	_, rest, ok := strings.Cut(string(help), "[possible values:")
	if !ok {
		return nil
	}
	list, _, ok := strings.Cut(rest, "]")
	if !ok {
		return nil
	}
	var kinds []string
	for _, kind := range strings.Split(list, ",") {
		if kind = strings.TrimSpace(kind); kindPattern.MatchString(kind) && !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func validVersion(version string) bool { return versionPattern.MatchString(version) }
