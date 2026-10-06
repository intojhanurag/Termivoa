package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	gib            = 1 << 30
	minFreeDuring  = 512 << 20
	sizeEvery      = 5 * time.Second
	resultFor      = 10 * time.Minute
	gitConfigLimit = 10 * time.Second
	tempPrefix     = ".termivoa-clone-"
	maxPieceBytes  = 4 << 10
)

// setupRun is the one set-up of this computer: running, or its result.
// view, stopped, noSpace and ended are guarded by Service.mu.
type setupRun struct {
	view    protocol.HTTPGitHubSetup
	running bool
	ended   time.Time
	cancel  context.CancelFunc
	stopped bool
	noSpace bool
}

// Setup starts the set-up of repo (owner/name) for device. Every outcome is
// in the answer's setup field: cloning, finished (also "already here"), or
// failed with a problem. Errors: ErrBusy when a set-up runs,
// ErrNotConnected without a login, ErrNoGit without a usable git. The
// checks before the clone call GitHub once (at most 10 s, 20 s with a token
// refresh); the clone itself runs in the background.
func (service *Service) Setup(ctx context.Context, device, repo string) (protocol.HTTPGitHubResponse, error) {
	if service == nil {
		return protocol.HTTPGitHubResponse{}, ErrNotConnected
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return service.State(device), ErrGitHubDown
	}
	if service.setup != nil && service.setup.running {
		service.mu.Unlock()
		return service.State(device), ErrBusy
	}
	owner, name, ok := splitRepo(repo)
	shown := repo
	if len(shown) > 140 {
		shown = ""
	}
	startedAt := service.now().UnixMilli()
	run := &setupRun{view: protocol.HTTPGitHubSetup{Repo: shown, Phase: protocol.Cloning, StartedAt: &startedAt}, running: true}
	if !ok {
		service.setup = run
		service.endSetupLocked(run, protocol.BadName)
		service.mu.Unlock()
		return service.State(device), nil
	}
	acc := service.account
	if acc == nil {
		service.mu.Unlock()
		return service.State(device), ErrNotConnected
	}
	runCtx, cancel := context.WithCancel(service.ctx)
	run.cancel = cancel
	service.setup = run
	service.mu.Unlock()

	fail := func(problem protocol.HTTPGitHubSetupProblem) (protocol.HTTPGitHubResponse, error) {
		cancel()
		service.endSetup(run, problem, "", false)
		return service.State(device), nil
	}
	binary := service.gitBinary()
	if binary == "" {
		cancel()
		service.mu.Lock()
		service.setup = nil
		service.mu.Unlock()
		return service.State(device), ErrNoGit
	}
	var remote struct {
		FullName string `json:"full_name"`
		Size     int64  `json:"size"`
	}
	status, err := service.apiGet(runCtx, "/repos/"+owner+"/"+name, &remote)
	switch {
	case errors.Is(err, ErrNotConnected):
		return fail(protocol.HTTPGitHubSetupProblemNotConnected)
	case err != nil && runCtx.Err() != nil:
		return fail(protocol.Stopped)
	case err != nil:
		return fail(protocol.HTTPGitHubSetupProblemGitHubDown)
	case status != http.StatusOK || !strings.EqualFold(remote.FullName, owner+"/"+name):
		return fail(protocol.NotFound)
	}
	// GitHub's spelling names the folder.
	if owner, name, ok = splitRepo(remote.FullName); !ok {
		return fail(protocol.BadName)
	}
	sizeKB := max(remote.Size, 0)

	if id, here := service.savedOrigins(runCtx)[strings.ToLower(owner+"/"+name)]; here {
		cancel()
		service.endSetup(run, "", id, true)
		return service.State(device), nil
	}
	switch service.place(runCtx, service.homeEntries(), owner, name) {
	case placeSame:
		cancel()
		id, err := service.deps.AddProject(service.ctx, filepath.Join(service.home, name))
		switch {
		case errors.Is(err, ErrTooManyProjects):
			return fail(protocol.TooManyProjects)
		case err != nil:
			return fail(protocol.CloneFailed)
		}
		service.endSetup(run, "", id, true)
		return service.State(device), nil
	case placeOther:
		return fail(protocol.NameTaken)
	}
	if service.deps.Projects != nil {
		if projects, err := service.deps.Projects(runCtx); err == nil && len(projects) >= protocol.MaxAdminProjects {
			return fail(protocol.TooManyProjects)
		}
	}
	limit := uint64(sizeKB)*1024*3 + gib
	if free, err := service.freeSpace(service.home); err != nil || free < limit {
		return fail(protocol.NoSpace)
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return fail(protocol.Stopped)
	}
	service.wg.Add(1)
	service.mu.Unlock()
	go func() {
		defer service.wg.Done()
		defer cancel()
		service.clone(runCtx, run, acc, binary, owner, name, limit)
	}()
	return service.State(device), nil
}

// Stop stops the running set-up; its result becomes failed/stopped.
func (service *Service) Stop(device string) protocol.HTTPGitHubResponse {
	if service == nil {
		return protocol.HTTPGitHubResponse{}
	}
	service.mu.Lock()
	if run := service.setup; run != nil && run.running {
		run.stopped = true
		if run.cancel != nil {
			run.cancel()
		}
	}
	service.mu.Unlock()
	return service.State(device)
}

// endSetup records the result and drops the list cache: here and taken
// may have changed.
func (service *Service) endSetup(run *setupRun, problem protocol.HTTPGitHubSetupProblem, projectID string, already bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.dropCache()
	if problem == "" {
		run.view.Phase = protocol.Finished
		run.view.ProjectID = &projectID
		if already {
			run.view.Already = &already
		}
		run.running, run.ended = false, service.now()
		return
	}
	service.endSetupLocked(run, problem)
}

func (service *Service) endSetupLocked(run *setupRun, problem protocol.HTTPGitHubSetupProblem) {
	service.dropCache()
	run.view.Phase = protocol.HTTPGitHubSetupPhaseFailed
	run.view.Problem = &problem
	run.running, run.ended = false, service.now()
}

// clone runs the clone into a temp folder in the home folder, prepares it,
// renames it to ~/<name> with a rename that cannot replace, and adds the
// project. On any failure it removes only the temp folder.
func (service *Service) clone(ctx context.Context, run *setupRun, acc *account, binary, owner, name string, limit uint64) {
	ctx, cancel := context.WithTimeout(ctx, service.opts.cloneLimit)
	defer cancel()
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	base := tempPrefix + service.config.Profile + "-" + hex.EncodeToString(random)
	temp := filepath.Join(service.home, base)
	if err := os.Mkdir(temp, 0o700); err != nil {
		service.endSetup(run, protocol.CloneFailed, "", false)
		return
	}
	problem, lfs := service.cloneInto(ctx, run, acc, binary, owner, name, temp, limit)
	if problem == "" {
		err := renameExclusive(temp, filepath.Join(service.home, name))
		switch {
		case errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY):
			problem = protocol.NameTaken
		case err != nil:
			problem = protocol.CloneFailed
		}
	}
	if problem != "" {
		service.removeTemp(base)
		service.endSetup(run, problem, "", false)
		return
	}
	id, err := service.deps.AddProject(service.ctx, filepath.Join(service.home, name))
	switch {
	case errors.Is(err, ErrTooManyProjects):
		service.endSetup(run, protocol.TooManyProjects, "", false)
		return
	case err != nil:
		service.endSetup(run, protocol.CloneFailed, "", false)
		return
	}
	service.mu.Lock()
	if lfs {
		run.view.LFS = &lfs
	}
	service.mu.Unlock()
	service.endSetup(run, "", id, false)
}

// cloneInto runs git clone into temp and prepares the clone (6.5, 6.6).
func (service *Service) cloneInto(
	ctx context.Context, run *setupRun, acc *account, binary, owner, name, temp string, limit uint64,
) (protocol.HTTPGitHubSetupProblem, bool) {
	gitHome, err := os.MkdirTemp("", "termivoa-git-home-")
	if err != nil {
		return protocol.CloneFailed, false
	}
	defer os.RemoveAll(gitHome)
	command := service.cloneCommand(ctx, binary, gitHome, owner, name, temp)
	command.Stderr = &progressWriter{report: func(percent int64) {
		service.mu.Lock()
		run.view.Percent = &percent
		service.mu.Unlock()
	}}
	if err := command.Start(); err != nil {
		return protocol.CloneFailed, false
	}
	watchDone := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		service.watchDisk(ctx, run, temp, limit, watchDone)
	}()
	err = command.Wait()
	killGroup(command)
	close(watchDone)
	<-watched

	service.mu.Lock()
	stopped, noSpace := run.stopped, run.noSpace
	service.mu.Unlock()
	switch {
	case noSpace:
		return protocol.NoSpace, false
	case stopped || service.ctx.Err() != nil:
		return protocol.Stopped, false
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return protocol.Timeout, false
	case err != nil:
		return protocol.CloneFailed, false
	}
	return service.prepare(ctx, binary, acc, owner, name, temp)
}

// watchDisk reads the temp folder's size and the free space every 5 s and
// stops the clone above the limit or below 512 MiB free.
func (service *Service) watchDisk(ctx context.Context, run *setupRun, temp string, limit uint64, done <-chan struct{}) {
	ticker := time.NewTicker(sizeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		size := dirSize(temp)
		free, err := service.freeSpace(service.home)
		service.mu.Lock()
		bytes := int64(size)
		run.view.Bytes = &bytes
		if size > limit || err == nil && free < minFreeDuring {
			run.noSpace = true
			if run.cancel != nil {
				run.cancel()
			}
		}
		service.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}

func dirSize(root string) uint64 {
	var total uint64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := entry.Info(); err == nil { // Lstat: a link counts as itself.
			total += uint64(max(info.Size(), 0))
		}
		return nil
	})
	return total
}

// prepare writes the helper lines and the commit identity into the clone's
// own config, and reads whether it uses Git LFS.
func (service *Service) prepare(
	ctx context.Context, binary string, acc *account, owner, name, temp string,
) (protocol.HTTPGitHubSetupProblem, bool) {
	configFile := filepath.Join(temp, ".git", "config")
	key := "credential." + service.opts.git + "/" + owner + "/" + name + ".git.helper"
	env := service.fixedEnv(binary, service.home)
	for _, value := range []string{"", service.helper()} {
		if _, err := service.runGitLimited(ctx, binary, env, "config", "--file", configFile, "--add", key, value); err != nil {
			return protocol.CloneFailed, false
		}
	}
	if !service.hasUserName(ctx, binary, configFile) {
		email := strconv.FormatInt(acc.id, 10) + "+" + acc.login + "@users.noreply.github.com"
		for _, pair := range [][2]string{{"user.name", acc.login}, {"user.email", email}} {
			if _, err := service.runGitLimited(ctx, binary, env, "config", "--file", configFile, pair[0], pair[1]); err != nil {
				return protocol.CloneFailed, false
			}
		}
	}
	return "", usesLFS(temp)
}

// hasUserName: the clone or the user's global config has user.name. The
// global read runs with the real HOME and reads only that one key.
func (service *Service) hasUserName(ctx context.Context, binary, configFile string) bool {
	local, _ := service.runGitLimited(ctx, binary, service.fixedEnv(binary, service.home), "config", "--file", configFile, "--get", "user.name")
	if len(bytes.TrimSpace(local)) > 0 {
		return true
	}
	globalEnv := []string{
		"HOME=" + service.home, "PATH=" + filepath.Dir(binary) + ":" + gitSafePathEnd,
		"LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
	}
	global, _ := service.runGitLimited(ctx, binary, globalEnv, "config", "--global", "--get", "user.name")
	return len(bytes.TrimSpace(global)) > 0
}

func (service *Service) runGitLimited(ctx context.Context, binary string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitConfigLimit)
	defer cancel()
	return service.runGit(ctx, binary, env, args...)
}

// usesLFS: the top-level .gitattributes names the lfs filter.
func usesLFS(temp string) bool {
	path := filepath.Join(temp, ".gitattributes")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBodyBytes {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && bytes.Contains(data, []byte("filter=lfs"))
}

// removeTemp removes the temp folder base in the home folder, through an
// os.Root so that nothing outside the home folder can be reached.
func (service *Service) removeTemp(base string) {
	root, err := os.OpenRoot(service.home)
	if err != nil {
		return
	}
	defer root.Close()
	_ = root.RemoveAll(base)
}

// sweep removes the temp folders of this profile that a crash left.
func (service *Service) sweep() {
	entries, err := os.ReadDir(service.home)
	if err != nil {
		return
	}
	prefix := tempPrefix + service.config.Profile + "-"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := os.Lstat(filepath.Join(service.home, entry.Name()))
		if err == nil && info.IsDir() && ownedByMe(info) {
			service.removeTemp(entry.Name())
		}
	}
}

var receivingPattern = regexp.MustCompile(`Receiving objects:\s+(\d{1,3})%`)

// progressWriter reads git's stderr in pieces split on "\r" and "\n", each
// cut at 4 KiB, and reports the percent of "Receiving objects". Nothing
// else is kept.
type progressWriter struct {
	piece  []byte
	report func(int64)
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	for _, char := range data {
		if char == '\r' || char == '\n' {
			if match := receivingPattern.FindSubmatch(writer.piece); match != nil {
				if percent, err := strconv.ParseInt(string(match[1]), 10, 64); err == nil && percent <= 100 {
					writer.report(percent)
				}
			}
			writer.piece = writer.piece[:0]
			continue
		}
		if len(writer.piece) < maxPieceBytes {
			writer.piece = append(writer.piece, char)
		}
	}
	return len(data), nil
}
