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
