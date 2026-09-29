package broker

import (
	"context"

	"github.com/intojhanurag/phone-terminal/internal/gitchanges"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Changes view (docs/changes.md). git never runs on the session actor: each
// request runs in its own goroutine under the viewer's context, so detaching
// or closing the session cancels it. A viewer has at most one in flight.
var (
	readChangesSummary = gitchanges.Summary
	readChangesPatch   = gitchanges.Patch
)

// ChangesFolder returns the folder whose git changes viewers may read: the
// live working folder when known, else the project folder the session
// started in.
func (session *Session) ChangesFolder(ctx context.Context) (string, error) {
	presence, err := session.Presence(ctx, false)
	if err != nil || presence.Cwd != "" {
		return presence.Cwd, err
	}
	return session.directory, nil
}

func (stream *sessionConnection) readChanges(ctx context.Context, request protocol.Envelope) error {
	if !stream.changesBusy.CompareAndSwap(false, true) {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a changes request is already running")
	}
	folder, err := stream.session.ChangesFolder(ctx)
	if err != nil {
		stream.changesBusy.Store(false)
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	viewer := stream.viewer
	go func() {
		kind, payload := protocol.ChangesSummary, any(nil)
		if request.Type == protocol.ChangesList {
			payload = readChangesSummary(viewer.ctx, folder)
		} else {
			file, _ := protocol.DecodePayload[protocol.ChangesFilePayload](request)
			kind, payload = protocol.ChangesPatch, readChangesPatch(viewer.ctx, folder, file.Path)
		}
		// Free the slot before replying: the client may ask again as soon as
		// the reply arrives.
		stream.changesBusy.Store(false)
		if viewer.ctx.Err() != nil || stream.revoked() {
			return // The viewer is gone; the dispatch loop ends the stream.
		}
		if err := stream.send(viewer.ctx, request, kind, payload); err != nil {
			_ = stream.writer.conn.Close()
		}
	}()
	return nil
}

var (
	commitChanges = gitchanges.Commit
	pushChanges   = gitchanges.Push
)

// writeChanges runs changes.commit or changes.push for the controller. The
// lease is checked right before git starts, and one write runs per session
// whoever asks. A started write finishes even if the viewer leaves, because
// a commit cut short is worse than a lost reply; only the reply is dropped.
func (stream *sessionConnection) writeChanges(
	ctx context.Context, request protocol.Envelope, identity func(string) MutationIdentity,
) error {
	commit, _ := protocol.DecodePayload[protocol.ChangesCommitPayload](request)
	lease := commit.Lease
	if request.Type == protocol.ChangesPush {
		push, _ := protocol.DecodePayload[protocol.ChangesPushPayload](request)
		lease = push.Lease
	}
	folder, err := stream.session.ChangesFolder(ctx)
	if err == nil {
		err = stream.session.AuthorizeControl(ctx, identity(lease))
	}
	if err != nil {
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	writes, sessionID := &stream.server.changesWrites, stream.sessionID
	if _, busy := writes.LoadOrStore(sessionID, struct{}{}); busy {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a commit or push is already running for this session")
	}
	viewer := stream.viewer
	go func() {
		work := context.WithoutCancel(viewer.ctx)
		kind, payload := protocol.ChangesPushed, any(nil)
		if request.Type == protocol.ChangesCommit {
			kind, payload = protocol.ChangesCommitted, commitChanges(work, folder, commit.Message, commit.Paths)
		} else {
			payload = pushChanges(work, folder)
		}
		writes.Delete(sessionID)
		if viewer.ctx.Err() != nil || stream.revoked() {
			return // The viewer is gone; the dispatch loop ends the stream.
		}
		if err := stream.send(viewer.ctx, request, kind, payload); err != nil {
			_ = stream.writer.conn.Close()
		}
	}()
	return nil
}
