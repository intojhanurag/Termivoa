package broker

import (
	"context"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/projectfiles"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Files view: files.list, files.read and files.search read the files root of
// the session's working folder (projectfiles). Like changes.list, the work
// runs off the session actor in a goroutine under the viewer's context, any
// viewer may ask, and a viewer has at most one files request in flight.
var (
	listFiles   = projectfiles.List
	readFile    = projectfiles.Read
	searchFiles = projectfiles.Search
)

const filesTimeout = 10 * time.Second

func (stream *sessionConnection) readFiles(ctx context.Context, request protocol.Envelope) error {
	var name string
	if request.Type != protocol.FilesSearch {
		if request.Type == protocol.FilesList {
			payload, _ := protocol.DecodePayload[protocol.FilesListPayload](request)
			name = payload.Path
		} else {
			payload, _ := protocol.DecodePayload[protocol.FilesReadPayload](request)
			name = payload.Path
		}
		var err error
		name, err = projectfiles.CleanPath(name)
		if err != nil || (request.Type == protocol.FilesRead && name == "") {
			return stream.failure(ctx, request, protocol.InvalidRequest,
				"path must name a file or folder relative to the files root, without .. segments")
		}
	}
	if !stream.filesBusy.CompareAndSwap(false, true) {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a files request is already running")
	}
	folder, err := stream.session.ChangesFolder(ctx)
	if err != nil {
		stream.filesBusy.Store(false)
		failure := classifySessionError(err)
		return stream.failure(ctx, request, failure.code, failure.message)
	}
	viewer := stream.viewer
	go func() {
		work, cancel := context.WithTimeout(viewer.ctx, filesTimeout)
		var kind protocol.MessageType
		var payload any
		switch request.Type {
		case protocol.FilesList:
			kind, payload = protocol.FilesListed, listFiles(work, folder, name)
		case protocol.FilesRead:
			kind, payload = protocol.FilesContent, readFile(work, folder, name)
		default:
			search, _ := protocol.DecodePayload[protocol.FilesSearchPayload](request)
			kind, payload = protocol.FilesFound, searchFiles(work, folder, search.Query)
		}
		cancel()
		// Free the slot before replying: the client may ask again as soon as
		// the reply arrives.
		stream.filesBusy.Store(false)
		if viewer.ctx.Err() != nil || stream.revoked() {
			return // The viewer is gone; the dispatch loop ends the stream.
		}
		if err := stream.send(viewer.ctx, request, kind, payload); err != nil {
			_ = stream.writer.conn.Close()
		}
	}()
	return nil
}
