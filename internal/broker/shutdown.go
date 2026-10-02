package broker

import (
	"context"
	"errors"
	"uuid"
)

// TerminateAll ends every starting or running session through Terminate, the
// same durable path as an explicit terminate request, then waits a bounded
// time for every live process tree to end. It returns how many terminations
// it requested. A session that ends concurrently is not an error.
func (registry *Registry) TerminateAll(ctx context.Context) (int, error) {
	statuses, err := registry.List(ctx)
	if err != nil {
		return 0, err
	}
	terminated := 0
	var ended []<-chan struct{}
	for _, status := range statuses {
		if status.State != SessionStarting && status.State != SessionRunning && status.State != SessionExiting {
			continue
		}
		if status.State != SessionExiting {
			_, err := registry.Terminate(ctx, TerminateSessionRequest{
				OperationID: uuid.New().String(), SessionID: status.ID, Generation: status.Generation,
			})
			switch {
			case errors.Is(err, ErrSessionMissing), errors.Is(err, ErrGenerationChanged):
				continue
			case err != nil:
				return terminated, err
			}
			terminated++
		}
		registry.mu.Lock()
		entry, ok := registry.sessions[status.ID]
		registry.mu.Unlock()
		if ok {
			ended = append(ended, entry.session.Ended())
		}
	}
	// Registry.Close still closes anything that outlives this bound.
	wait, cancel := context.WithTimeout(ctx, 2*shutdownTimeout)
	defer cancel()
	for _, done := range ended {
		select {
		case <-done:
		case <-wait.Done():
			return terminated, nil
		}
	}
	return terminated, nil
}
