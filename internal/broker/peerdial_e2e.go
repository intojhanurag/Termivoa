//go:build e2e

package broker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"
)

// In e2e builds only (scripts/e2e-multicomputer), an offer push reaches home
// through the harness front: an HTTP CONNECT proxy that stands in for
// MagicDNS and serves every test host with one self-signed key. The dial
// opens CONNECT <home host>:443 through PTERM_E2E_FRONT and accepts only a
// certificate for that host whose SPKI hash is PTERM_E2E_SPKI (the hash the
// front prints at start). The URL, headers, node check and limits stay the
// production code's; only the pinned-IP guard is replaced. Production builds
// do not compile this file.
func init() {
	front, spki := os.Getenv("PTERM_E2E_FRONT"), os.Getenv("PTERM_E2E_SPKI")
	if front == "" {
		return
	}
	dialHome = func(ctx context.Context, _ netip.Addr, host string) (net.Conn, error) {
		var dialer net.Dialer
		raw, err := dialer.DialContext(ctx, "tcp", front)
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = raw.SetDeadline(deadline)
		}
		target := net.JoinHostPort(host, "443")
		if _, err := fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
			_ = raw.Close()
			return nil, err
		}
		// The front sends nothing after its answer until the ClientHello.
		response, err := http.ReadResponse(bufio.NewReader(raw), nil)
		if err != nil || response.StatusCode != http.StatusOK {
			_ = raw.Close()
			return nil, errors.Join(errOfferDial, err)
		}
		conn := tls.Client(raw, &tls.Config{
			ServerName: host, MinVersion: tls.VersionTLS12,
			InsecureSkipVerify: true, // Replaced by the SPKI and name check below.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errOfferDial
				}
				leaf := state.PeerCertificates[0]
				sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
				if base64.StdEncoding.EncodeToString(sum[:]) != spki {
					return errors.New("broker: e2e front certificate does not match PTERM_E2E_SPKI")
				}
				return leaf.VerifyHostname(host)
			},
		})
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		_ = raw.SetDeadline(time.Time{})
		return conn, nil
	}
}
