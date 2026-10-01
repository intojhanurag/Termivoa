package broker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gitchanges"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Worktrees (WorktreeCreate in protocol/v1/broker-rpc.schema.json). git runs
// off the session actor, as for the Changes view.
var (
	listBranches   = gitchanges.Branches
	createWorktree = gitchanges.CreateWorktree
	rollBackCreate = gitchanges.RollBack
	removeWorktree = gitchanges.RemoveWorktree
)

const branchesTimeout = 5 * time.Second

// defaultWorktreeRoot is <home>/termivoa/worktrees, or "" without a home
// folder (worktree creates then fail).
func defaultWorktreeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "termivoa", "worktrees")
}

// sessionWorktree is the worktree record of a live session that started in
// a Termivoa worktree, or nil.
func (server *ManagementServer) sessionWorktree(session ManagedSession) *protocol.SessionWorktree {
	name, repo, ok := gitchanges.WorktreeOf(server.config.WorktreeRoot, session.ProjectFolder())
	if !ok {
		return nil
	}
	return &protocol.SessionWorktree{Name: name, Repo: repo}
}

func (server *ManagementServer) withWorktree(record protocol.BrokerRPCSessionRecord) protocol.BrokerRPCSessionRecord {
	if record.State == protocol.Exited || record.State == protocol.Lost {
		return record
	}
	if session, err := server.config.Registry.Get(record.ID); err == nil {
		record.Worktree = server.sessionWorktree(session)
	}
	return record
}

// prepareWorktree creates the worktree for a session create and returns it.
// The caller holds server.worktreeCreates.
func (server *ManagementServer) prepareWorktree(
	ctx context.Context, worktree protocol.WorktreeCreate,
) (gitchanges.Created, error) {
	if err := protocol.ValidateWorktreeCreate(&worktree); err != nil {
		return gitchanges.Created{}, gitchanges.ErrWorktreeName
	}
	source, err := server.config.Registry.Get(worktree.SourceSessionID)
	if err != nil {
		return gitchanges.Created{}, err
	}
	folder, err := source.ChangesFolder(ctx)
	if err != nil {
		return gitchanges.Created{}, err
	}
	if server.config.WorktreeRoot == "" {
		return gitchanges.Created{}, errors.New("broker: no home folder for worktrees")
	}
	return createWorktree(ctx, folder, server.config.WorktreeRoot, worktree.Name, worktree.Base)
}

func (stream *sessionConnection) listBranches(ctx context.Context, request protocol.Envelope) error {
	if !stream.branchesBusy.CompareAndSwap(false, true) {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a branches request is already running")
	}
	folder, err := stream.session.ChangesFolder(ctx)
	if err != nil {
		stream.branchesBusy.Store(false)
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	viewer := stream.viewer
	go func() {
		work, cancel := context.WithTimeout(viewer.ctx, branchesTimeout)
		payload := listBranches(work, folder)
		cancel()
		stream.branchesBusy.Store(false)
		if viewer.ctx.Err() != nil || stream.revoked() {
			return // The viewer is gone; the dispatch loop ends the stream.
		}
		if err := stream.send(viewer.ctx, request, protocol.BranchesListed, payload); err != nil {
			_ = stream.writer.conn.Close()
		}
	}()
	return nil
}

// removeWorktreeOfSession runs worktree.remove for the controller. It shares
// the one-write-per-session slot with commit and push. A started remove
// finishes even if the viewer leaves. After a removal the reply goes out
// first, then the session is terminated as the management operation does.
func (stream *sessionConnection) removeWorktreeOfSession(
	ctx context.Context, request protocol.Envelope, identity func(string) MutationIdentity,
) error {
	payload, _ := protocol.DecodePayload[protocol.WorktreeRemovePayload](request)
	folder := stream.session.ProjectFolder()
	if stream.server.sessionWorktree(stream.session) == nil {
		return stream.failure(ctx, request, protocol.InvalidRequest, "this session did not start in a Termivoa worktree")
	}
	if err := stream.session.AuthorizeControl(ctx, identity(payload.Lease)); err != nil {
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	writes, sessionID, generation := &stream.server.changesWrites, stream.sessionID, stream.generation
	if _, busy := writes.LoadOrStore(sessionID, struct{}{}); busy {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a commit, push or remove is already running for this session")
	}
	viewer, registry := stream.viewer, stream.server.config.Registry
	go func() {
		work := context.WithoutCancel(viewer.ctx)
		result := removeWorktree(work, folder, payload.Force)
		writes.Delete(sessionID)
		if viewer.ctx.Err() == nil && !stream.revoked() {
			if err := stream.send(viewer.ctx, request, protocol.WorktreeRemoved, result); err != nil {
				_ = stream.writer.conn.Close()
			}
		}
		if result.State != protocol.Removed {
			return
		}
		if operationID, err := randomUUID(); err == nil {
			_, _ = registry.Terminate(work, TerminateSessionRequest{
				OperationID: operationID, SessionID: sessionID, Generation: generation,
			})
		}
	}()
	return nil
}
