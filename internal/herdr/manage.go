package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// Manage errors (docs/herdr-manage.md, section 3.2). The broker gives each a
// fixed public text.
var (
	ErrRenamed      = errors.New("herdr: the item has another name now")
	ErrGroupOpen    = errors.New("herdr: a linked worktree of this main checkout is open")
	ErrNotLinked    = errors.New("herdr: the item is not a linked worktree")
	ErrFull         = errors.New("herdr: the terminal or group limit is reached")
	ErrNotStartable = errors.New("herdr: the server cannot be started now")
	ErrNoServer     = errors.New("herdr: the server did not come up")
	ErrManage       = errors.New("herdr: manage failed")
)

// manageTimeouts are the step limits of Manage. A test seam.
var manageTimeouts = struct{ call, refresh, ready, gone time.Duration }{
	call: 15 * time.Second, refresh: 3 * time.Second, ready: 5 * time.Second, gone: 500 * time.Millisecond,
}

// Project is the Termivoa project of newWorkspace: its resolved folder, which
// the broker checked right before the call, and its display name.
type Project struct{ Folder, Name string }

var errorCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// Manage closes or removes an item, makes a tab or a workspace, or starts the
// server (docs/herdr-manage.md, section 4). One manage or create runs at a
// time. ctx must be broker-owned, never a request's. Item actions read a
// fresh snapshot first: IDs start again after a herdr restart, so the item
// must be there under the name the user confirmed. Nothing is ever forced.
func (provider *Provider) Manage(ctx context.Context, request protocol.HTTPProviderManageRequest, project Project) (protocol.HTTPProviderManageResponse, error) {
	var response protocol.HTTPProviderManageResponse
	if !provider.create.TryLock() {
		return response, ErrBusy
	}
	defer provider.create.Unlock()
	provider.mu.RLock()
	current := provider.view
	provider.mu.RUnlock()
	if request.Action == protocol.StartServer {
		return response, provider.start(current)
	}
	if current.state != protocol.HTTPProviderStateReady {
		return response, ErrUnavailable
	}
	fresh, ok := provider.readMapped(ctx)
	if !ok {
		return response, ErrUnavailable
	}
	var group protocol.HTTPProviderGroup
	var item protocol.HTTPProviderItem
	if request.Action != protocol.NewWorkspace {
		found := false
		for _, group = range fresh.response.Groups {
			for _, item = range group.Items {
				if found = request.ItemID != nil && item.ID == *request.ItemID; found {
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return response, ErrTargetMissing
		}
		response.ItemID = &item.ID
	}
	var made struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			PaneID     string `json:"pane_id"`
			TerminalID string `json:"terminal_id"`
		} `json:"root_pane"`
	}
	method, params := "", map[string]any{"workspace_id": item.ID}
	switch request.Action {
	case protocol.CloseItem, protocol.RemoveItem:
		if request.Name == nil || item.Name != *request.Name {
			return response, ErrRenamed
		}
		method = "workspace.close"
		if request.Action == protocol.RemoveItem {
			if item.Linked == nil || !*item.Linked {
				return response, ErrNotLinked
			}
			method, params["force"] = "worktree.remove", false
		} else if item.Linked == nil && (len(group.Items) == maxItems ||
			slices.ContainsFunc(group.Items, func(other protocol.HTTPProviderItem) bool { return other.Linked != nil })) {
			// A main checkout with an open linked worktree (or with more items than the list shows):
			// herdr 0.8.2 would close the worktrees too; 0.9.3 refuses.
			return response, ErrGroupOpen
		}
	case protocol.NewTab:
		if len(item.Targets) >= maxTargets {
			return response, ErrFull
		}
		method, params["focus"] = "tab.create", false
	case protocol.NewWorkspace:
		if len(fresh.response.Groups) >= maxGroups {
			return response, ErrFull
		}
		if !filepath.IsAbs(project.Folder) { // herdr would pick a folder of its own
			return response, ErrManage
		}
		method = "workspace.create"
		params = map[string]any{"cwd": project.Folder, "label": Clean(project.Name, maxNameRunes, "workspace"), "focus": false}
	default:
		return response, ErrManage
	}
	err := provider.call(ctx, manageTimeouts.call, method, params, &made)
	var failure *callError
	if err != nil {
		code := "no_answer"
		if errors.As(err, &failure) {
			if code = failure.code; !errorCodePattern.MatchString(code) {
				code = "other"
			}
		}
		// The action and herdr's code only: its text can name local paths.
		provider.logf("herdr: %s failed: %s", request.Action, code)
		provider.Poll()
		switch code {
		case "dirty_worktree_requires_force":
			problem := protocol.Uncommitted
			response.Problem = &problem
			return response, nil
		case "workspace_not_found":
			return response, ErrTargetMissing
		case "workspace_group_close_required":
			return response, ErrGroupOpen
		case "not_linked_worktree":
			return response, ErrNotLinked
		}
		return response, ErrManage
	}
	if request.Action == protocol.NewWorkspace && handlePattern.MatchString(made.Workspace.ID) {
		response.ItemID = &made.Workspace.ID
	}
	target := made.RootPane.PaneID + "." + made.RootPane.TerminalID
	opens := request.Action == protocol.NewTab || request.Action == protocol.NewWorkspace
	// Without `terminal attach` this herdr cannot open a terminal that has no agent: no targetId.
	if opens && current.caps.attachAny && handlePattern.MatchString(made.RootPane.PaneID) &&
		handlePattern.MatchString(made.RootPane.TerminalID) && handlePattern.MatchString(target) {
		response.TargetID = &target
		// Answer once a snapshot read after the call has the new terminal, so it opens at once.
		provider.settle(ctx, manageTimeouts.refresh, target)
	}
	provider.Poll()
	return response, nil
}

// systemdRun is where a Linux host looks for systemd-run. A test seam.
var systemdRun = func() string {
	for _, path := range []string{"/usr/bin/systemd-run", "/bin/systemd-run"} {
		if runtime.GOOS == "linux" && tailnet.CheckBinary(path) == nil {
			return path
		}
	}
	return ""
}

// start is design A6: `herdr server` as a child of the broker that no request
// or serve context can end, in its own session, with no terminal and no
// output. On Linux it runs in its own systemd scope when it can, so stopping
// the Termivoa service does not end herdr and its agents; when that run ends
// without a server, herdr starts plainly once. It waits up to 5 s for ready.
// The caller holds create.
func (provider *Provider) start(current view) error {
	if !current.startable() {
		return ErrNotStartable
	}
	if provider.child != nil {
		select {
		case <-provider.child:
		default:
			return ErrNoServer // the server it started before is still coming up, or hangs
		}
	}
	if tailnet.CheckBinary(current.binary) != nil {
		return ErrUnavailable
	}
	argv := append(provider.command(current.binary), "server")
	if scope := systemdRun(); scope != "" {
		if provider.spawn(append([]string{scope, "--user", "--scope", "--quiet", "--collect", "--"}, argv...)) {
			return nil
		}
		select {
		case <-provider.child:
		default:
			return ErrNoServer
		}
	}
	if !provider.spawn(argv) {
		return ErrNoServer
	}
	return nil
}

// spawn starts argv, reaps it in a goroutine, and reports whether the
// provider turned ready within the limit. After the child ended it looks for
// 500 ms more (a server that was already coming up), not longer.
func (provider *Provider) spawn(argv []string) bool {
	command := exec.Command(argv[0], argv[1:]...)
	// The environment of every Termivoa session, then the provider's own values (the last one wins).
	command.Env = append(ptyhost.HostEnvironment(os.Environ()), provider.environment(false)...)
	command.Env = append(command.Env, "TERM=xterm-256color")
	command.Dir = provider.config.Home
	noTerminal(command) // its own session: no controlling terminal, not in the broker's process group
	ended := make(chan struct{})
	provider.child = ended
	if err := command.Start(); err != nil {
		close(ended)
		provider.logf("herdr: %s failed: not_started", protocol.StartServer)
		return false
	}
	go func() {
		_ = command.Wait()
		close(ended)
	}()
	deadline, gone := time.Now().Add(manageTimeouts.ready), false
	for {
		provider.LookAgain()
		provider.mu.RLock()
		ready := provider.view.state == protocol.HTTPProviderStateReady
		provider.mu.RUnlock()
		if ready {
			return true
		}
		if !gone {
			select {
			case <-ended:
				if last := time.Now().Add(manageTimeouts.gone); last.Before(deadline) {
					deadline = last
				}
				gone = true
			default:
			}
		}
		if !time.Now().Before(deadline) {
			provider.logf("herdr: %s failed: not_ready", protocol.StartServer)
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
