package broker

import (
	"context"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/projecttasks"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Project tasks (docs/project-tasks.md). Files are read off the session
// actor, one request per viewer, like changes.list. Nothing runs.
var listProjectTasks = projecttasks.List

const tasksTimeout = 3 * time.Second

// ProjectFolder is the folder the session started in. Tasks use it, not the
// live folder, because the phone may run a task in a new session, which
// opens there.
func (session *Session) ProjectFolder() string { return session.directory }

func (stream *sessionConnection) listTasks(ctx context.Context, request protocol.Envelope) error {
	if !stream.tasksBusy.CompareAndSwap(false, true) {
		return stream.failure(ctx, request, protocol.LimitExceeded, "a tasks request is already running")
	}
	folder, viewer := stream.session.ProjectFolder(), stream.viewer
	go func() {
		work, cancel := context.WithTimeout(viewer.ctx, tasksTimeout)
		payload := listProjectTasks(work, folder)
		cancel()
		stream.tasksBusy.Store(false)
		if viewer.ctx.Err() != nil || stream.revoked() {
			return // The viewer is gone; the dispatch loop ends the stream.
		}
		if err := stream.send(viewer.ctx, request, protocol.TasksListed, payload); err != nil {
			_ = stream.writer.conn.Close()
		}
	}()
	return nil
}
