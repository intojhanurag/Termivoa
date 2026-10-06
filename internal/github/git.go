package github

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

const (
	gitShort       = 2 * time.Second
	gitRelook      = 10 * time.Second
	maxGitOutput   = 64 << 10
	gitMinMajor    = 2
	gitMinMinor    = 31 // the first git that reads GIT_CONFIG_COUNT
	gitSafePathEnd = "/usr/bin:/bin"
)

var gitVersionPattern = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// findGit returns the first git that passes tailnet.CheckBinary and is at
// least 2.31: GitPaths first, then PathEnv. "" when there is none.
func (service *Service) findGit() string {
	var candidates []string
	for _, candidate := range service.config.GitPaths {
		if filepath.IsAbs(candidate) && !slices.Contains(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	for _, folder := range filepath.SplitList(service.config.PathEnv) {
		if candidate := filepath.Join(folder, "git"); filepath.IsAbs(folder) && !slices.Contains(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range candidates {
		if tailnet.CheckBinary(candidate) != nil || !developerToolsFor(candidate) {
			continue
		}
		if service.gitVersionOK(candidate) {
			return candidate
		}
	}
	return ""
}

// developerToolsFor: on macOS /usr/bin/git is a shim that opens an
// "install the developer tools" window when they are missing; it is used
// only when they are there.
// ponytail: the two default places only; an Xcode elsewhere uses its own git
// through PathEnv.
func developerToolsFor(candidate string) bool {
	if runtime.GOOS != "darwin" || candidate != "/usr/bin/git" {
		return true
	}
	for _, real := range []string{"/Library/Developer/CommandLineTools/usr/bin/git", "/Applications/Xcode.app/Contents/Developer/usr/bin/git"} {
		if _, err := os.Stat(real); err == nil {
			return true
		}
	}
	return false
}

func (service *Service) gitVersionOK(binary string) bool {
	ctx, cancel := context.WithTimeout(service.ctx, gitShort)
	defer cancel()
	output, err := service.runGit(ctx, binary, service.fixedEnv(binary, service.home), "--version")
	if err != nil {
		return false
	}
	match := gitVersionPattern.FindSubmatch(output)
	if match == nil {
		return false
	}
	major, _ := strconv.Atoi(string(match[1]))
	minor, _ := strconv.Atoi(string(match[2]))
	return major > gitMinMajor || major == gitMinMajor && minor >= gitMinMinor
}

// gitBinary is the usable git, or "". When there is none it looks again at
// most every 10 s.
func (service *Service) gitBinary() string {
	service.gitMu.Lock()
	defer service.gitMu.Unlock()
	if service.gitPath != "" && tailnet.CheckBinary(service.gitPath) == nil {
		return service.gitPath
	}
	if !service.gitLooked.IsZero() && time.Since(service.gitLooked) < gitRelook {
		return ""
	}
	service.gitLooked = time.Now()
	service.gitPath = service.findGit()
	return service.gitPath
}

// fixedEnv is the whole environment of a short git run: nothing from the
// broker's own environment, no global or system config, no prompt.
func (service *Service) fixedEnv(binary, home string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + filepath.Dir(binary) + ":" + gitSafePathEnd,
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
}

// git runs the usable git with args, the fixed environment (or env), a
// time limit, an output cap, no stdin and no terminal, in "/" so that git
// discovers no repository. It returns standard output.
func (service *Service) git(ctx context.Context, timeout time.Duration, env []string, args ...string) ([]byte, error) {
	binary := service.gitBinary()
	if binary == "" {
		return nil, ErrNoGit
	}
	if env == nil {
		env = service.fixedEnv(binary, service.home)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return service.runGit(ctx, binary, env, args...)
}

func (service *Service) runGit(ctx context.Context, binary string, env []string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = env
	command.Dir = "/"
	command.WaitDelay = time.Second
	ownGroup(command)
	output := &cappedBuffer{limit: maxGitOutput}
	command.Stdout, command.Stderr = output, nil
	err := command.Run()
	killGroup(command)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return output.Bytes(), err
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	if room := buffer.limit - buffer.Len(); room > 0 {
		buffer.Buffer.Write(data[:min(len(data), room)])
	}
	return len(data), nil
}

// helperCommand is git's credential helper value. git runs a "!" helper
// through the shell and appends the operation, so both paths are single
// quoted (New refuses paths with a single quote): production
// "!'<pterm>' github-credential", development
// "!'<pterm>' github-credential --dev --runtime-dir '<runtimeDir>'" (a later
// git pull in a terminal has no PTERM_RUNTIME_DIR, and a development
// broker has its own runtime folder).
func helperCommand(pterm, profile, runtimeDir string) string {
	value := "!'" + pterm + "' github-credential"
	if profile == "development" {
		value += " --dev --runtime-dir '" + runtimeDir + "'"
	}
	return value
}

func (service *Service) helper() string {
	return helperCommand(service.config.Pterm, service.config.Profile, service.config.RuntimeDir)
}

// xdgNames are the folders pterm reads to find its runtime folder on Linux.
var xdgNames = []string{"XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"}

// cloneHelper is the helper for the clone itself. git runs with HOME set to
// an empty temp folder there, and pterm finds its runtime folder from HOME
// (macOS) or XDG_RUNTIME_DIR (Linux): so the helper gets the broker's own
// values back in front of it. The helper saved in the clone has no such
// prefix: a later git pull runs in the user's own environment.
func (service *Service) cloneHelper() string {
	prefix := "HOME='" + service.config.Home + "' "
	for _, name := range xdgNames {
		if value := os.Getenv(name); value != "" {
			prefix += name + "='" + value + "' "
		}
	}
	return "!" + prefix + strings.TrimPrefix(service.helper(), "!")
}

// cloneConfig is the config of the clone, given through GIT_CONFIG_COUNT
// (design 6.5), in this order.
func (service *Service) cloneConfig(owner, name string) [][2]string {
	config := [][2]string{
		{"credential.helper", ""},
		{"credential." + service.opts.git + "/" + owner + "/" + name + ".git.helper", service.cloneHelper()},
		{"protocol.allow", "never"},
		{"protocol.https.allow", "always"},
	}
	if service.opts.open {
		config = append(config, [2]string{"protocol.http.allow", "always"})
	}
	return append(config,
		[2]string{"http.followRedirects", "false"},
		[2]string{"http.proxy", ""},
		[2]string{"core.hooksPath", "/dev/null"},
		[2]string{"core.fsmonitor", "false"},
		[2]string{"submodule.recurse", "false"},
		[2]string{"transfer.fsckObjects", "true"},
	)
}

// cloneCommand builds the clone of owner/name into target, with gitHome as
// HOME. It holds no token: git asks `pterm github-credential`, which asks
// the broker.
func (service *Service) cloneCommand(ctx context.Context, binary, gitHome, owner, name, target string) *exec.Cmd {
	env := service.fixedEnv(binary, gitHome)
	env = append(env, "GIT_LFS_SKIP_SMUDGE=1")
	config := service.cloneConfig(owner, name)
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
	for index, pair := range config {
		env = append(env, "GIT_CONFIG_KEY_"+strconv.Itoa(index)+"="+pair[0], "GIT_CONFIG_VALUE_"+strconv.Itoa(index)+"="+pair[1])
	}
	command := exec.CommandContext(ctx, binary, "clone", "--no-recurse-submodules", "--template=", "--progress",
		service.opts.git+"/"+owner+"/"+name+".git", target)
	command.Env = env
	command.Dir = "/"
	command.WaitDelay = 5 * time.Second
	ownGroup(command)
	return command
}
