package broker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// Dev-server preview: which local TCP ports a session's own processes listen
// on. The operating system is the only source (see ptyhost.ListeningPorts);
// screen text is never trusted to name a port. Work is bounded: a session is
// scanned only while it is running and had output recently, or while it
// already has ports open (to notice them closing).
const (
	previewScanDelay   = time.Second      // first scan after output
	previewScanEvery   = 2 * time.Second  // spacing while output is recent
	previewActiveFor   = 30 * time.Second // keep scanning this long after output
	previewIdleEvery   = 10 * time.Second // spacing while ports are open and output is quiet
	previewCheckMaxAge = 2 * time.Second  // gateway checks reuse a scan at most this old
	previewScanTimeout = 3 * time.Second
)

// ErrPreviewPort rejects a port that no process of the session listens on.
var ErrPreviewPort = errors.New("broker: port is not a dev server of this session")

type portLister interface {
	ListeningPorts(context.Context) ([]ptyhost.PortTarget, error)
}

// previewScanner caches the last OS scan. The session actor's background
// scans and gateway checks share it, so a page load of many assets costs at
// most one scan per previewCheckMaxAge. Scans are serialized by mu.
type previewScanner struct {
	list  func(context.Context) ([]ptyhost.PortTarget, error)
	mu    sync.Mutex
	at    time.Time
	ports []ptyhost.PortTarget
}

func newPreviewScanner(process processRuntime) *previewScanner {
	lister, ok := process.(portLister)
	if !ok {
		return nil
	}
	return &previewScanner{list: lister.ListeningPorts}
}

// scan returns ports verified at most maxAge ago; maxAge 0 always rescans.
func (scanner *previewScanner) scan(ctx context.Context, maxAge time.Duration) ([]ptyhost.PortTarget, error) {
	if scanner == nil {
		return nil, nil
	}
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	if !scanner.at.IsZero() && time.Since(scanner.at) < maxAge {
		return scanner.ports, nil
	}
	ctx, cancel := context.WithTimeout(ctx, previewScanTimeout)
	defer cancel()
	ports, err := scanner.list(ctx)
	if err != nil {
		return nil, err
	}
	scanner.at, scanner.ports = time.Now(), ports
	return ports, nil
}

type previewPort struct {
	port  uint16
	since time.Time
}

type previewScanResult struct {
	ports []ptyhost.PortTarget
	err   error
}

// mergePreviews returns the ports of found, sorted as found is, keeping the
// first-seen time of ports that stay open. changed reports a different set.
func mergePreviews(current []previewPort, found []ptyhost.PortTarget, now time.Time) ([]previewPort, bool) {
	next := make([]previewPort, 0, len(found))
	for _, target := range found {
		since := now
		for _, existing := range current {
			if existing.port == target.Port {
				since = existing.since
			}
		}
		next = append(next, previewPort{port: target.Port, since: since})
	}
	changed := len(next) != len(current)
	for index := 0; !changed && index < len(next); index++ {
		changed = next[index].port != current[index].port
	}
	return next, changed
}

// previewPayload converts previews for the wire; nil when there are none.
func previewPayload(previews []previewPort) []protocol.PreviewPort {
	if len(previews) == 0 {
		return nil
	}
	payload := make([]protocol.PreviewPort, 0, len(previews))
	for _, preview := range previews {
		payload = append(payload, protocol.PreviewPort{
			Port: int64(preview.port), Since: preview.since.UTC().Format(millisecondRFC3339),
		})
	}
	return payload
}

// PreviewTarget re-verifies from the operating system (reusing a scan at
// most previewCheckMaxAge old) that a process of this running session
// listens on port, and returns the loopback address to dial for it.
func (session *Session) PreviewTarget(ctx context.Context, port uint16) (string, error) {
	status, err := session.Status(ctx)
	if err != nil {
		return "", err
	}
	if status.State != SessionRunning {
		return "", ErrSessionNotRunning
	}
	ports, err := session.previewScanner.scan(ctx, previewCheckMaxAge)
	if err != nil {
		return "", err
	}
	for _, target := range ports {
		if target.Port == port {
			return target.Host, nil
		}
	}
	return "", ErrPreviewPort
}

func later(first, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}
