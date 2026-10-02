package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

func dnsHost(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) }

// tailnetFindings checks each unrevoked device's pinned app node against the
// current tailnet: the app host must be in this tailnet, its pinned node must
// still exist and still own that name, and, when neither node is tagged, it
// must have this computer's owner. Each origin and node pair is reported once.
// Without a MagicDNS suffix (logged out) nothing can be checked; that is
// reported once.
func tailnetFindings(status *tailnet.Status, devices []protocol.AdminRPCDevice) []string {
	checked := map[string]bool{}
	var findings []string
	for _, device := range devices {
		if device.Revoked || device.AppOrigin == nil || device.AppNode == nil {
			continue
		}
		if status.Suffix == "" {
			return []string{"this computer's tailnet is unknown (is Tailscale logged out?); bound app origins cannot be checked"}
		}
		origin, pinned := *device.AppOrigin, device.AppNode.ID
		if checked[origin+" "+pinned] {
			continue
		}
		checked[origin+" "+pinned] = true
		host := strings.TrimPrefix(origin, "https://")
		peer, found := status.Peer(host)
		switch {
		case !strings.HasSuffix(host, "."+status.Suffix):
			findings = append(findings, origin+": different tailnet; not supported")
		case !found:
			findings = append(findings, fmt.Sprintf("%s: pinned node %s is gone; phones bound to it stop working", origin, pinned))
		default:
			tagged := len(device.AppNode.Tags) > 0
			switch status.Trust(origin, pinned, tagged) {
			case tailnet.Changed:
				findings = append(findings, origin+": node ID changed (reused name?)")
			case tailnet.Untrusted:
				if peer.Tagged() != tagged {
					findings = append(findings, origin+": tags changed since pairing; phones bound to it are refused until paired again")
					break
				}
				findings = append(findings, fmt.Sprintf("%s: owner mismatch (%s; this computer: %s)",
					origin, strconv.QuoteToGraphic(peer.Owner), strconv.QuoteToGraphic(status.Self.Owner)))
			}
		}
	}
	return findings
}

// otherServeHandlers lists what else Tailscale Serve offers on this
// computer's own HTTPS origin: handlers on <host>:443 other than "/" proxying
// the gateway, and a TCP forward on 443. lifecycle.ServeFindings already
// counts each as a problem; this names them so setup can refuse them.
func otherServeHandlers(data []byte, dnsName string) []string {
	var raw struct {
		TCP map[string]struct{ TCPForward string }
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var found []string
	if forward := raw.TCP["443"].TCPForward; forward != "" {
		found = append(found, tcpForwardPrefix+forward)
	}
	for hostPort, web := range raw.Web {
		if strings.ToLower(hostPort) != dnsHost(dnsName)+":443" {
			continue
		}
		for path, handler := range web.Handlers {
			if path != "/" || strings.TrimSuffix(handler.Proxy, "/") != strings.TrimSuffix(lifecycle.LocalURL, "/") {
				found = append(found, path)
			}
		}
	}
	slices.Sort(found)
	return found
}

const tcpForwardPrefix = "TCP forward to "

// otherHandlersProblem explains why other Serve handlers on this computer's
// origin are refused (docs/multi-computer.md, "Roles") and how to move them.
func otherHandlersProblem(dnsName string, handlers []string) string {
	remove := make([]string, len(handlers))
	for index, handler := range handlers {
		remove[index] = "`tailscale serve --https=443 --set-path=" + handler + " off`"
		if strings.HasPrefix(handler, tcpForwardPrefix) {
			remove[index] = "`tailscale serve --tcp=443 off`"
		}
	}
	return fmt.Sprintf("tailscale serve: other handlers share this computer's Termivoa origin https://%s: %s. "+
		"Any handler there is same-origin with the Termivoa app and could control this computer and, if it is home, "+
		"every computer added to it. Move each one to another HTTPS port (for example `tailscale serve --bg --https=8444 <target>`) "+
		"or to another computer, then remove it here: %s.",
		dnsHost(dnsName), strings.Join(handlers, ", "), strings.Join(remove, ", "))
}

// inspectMultiComputer prints the protocol revision and, when the broker
// answers, its bound app origins (the gateway's CORS list), the app origins
// of pending pairings, and tailnet and owner mismatches of bound devices.
func inspectMultiComputer(ctx context.Context, env lifecycleEnv, report hostReport, output io.Writer) (problems int) {
	fmt.Fprintf(output, "  protocol revision %d (oldest supported %d)\n", protocol.ProtocolRevision, protocol.MinProtocolRevision)
	if !report.broker || report.listErr != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout+2*time.Second)
	defer cancel()
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		fmt.Fprintf(output, "  ! multi-computer: cannot read devices: %v\n", err)
		return 1
	}
	defer admin.Close()
	devices, err := admin.Devices(ctx)
	var pending []protocol.AdminRPCPendingPairing
	if err == nil {
		pending, err = admin.PendingPairings(ctx)
	}
	if err != nil {
		fmt.Fprintf(output, "  ! multi-computer: cannot read devices: %v\n", err)
		return 1
	}
	var bound, pairing []string
	for _, device := range devices {
		if !device.Revoked && device.AppOrigin != nil {
			bound = append(bound, *device.AppOrigin)
		}
	}
	for _, item := range pending {
		if item.AppOrigin != nil {
			pairing = append(pairing, *item.AppOrigin)
		}
	}
	slices.Sort(bound)
	slices.Sort(pairing)
	if bound = slices.Compact(bound); len(bound) == 0 {
		fmt.Fprintln(output, "  multi-computer: no bound app origins (CORS off)")
	} else {
		fmt.Fprintf(output, "  multi-computer: CORS list (bound app origins): %s\n", strings.Join(bound, ", "))
	}
	if pairing = slices.Compact(pairing); len(pairing) > 0 {
		fmt.Fprintf(output, "  multi-computer: pairing CORS list (pending pairings): %s\n", strings.Join(pairing, ", "))
	}
	if report.tailscale.tailnet == nil {
		if len(bound) > 0 {
			fmt.Fprintln(output, "  multi-computer: tailnet and owner checks skipped (no Tailscale status)")
		}
		return 0
	}
	for _, finding := range tailnetFindings(report.tailscale.tailnet, devices) {
		fmt.Fprintf(output, "  ! multi-computer: %s\n", finding)
		problems++
	}
	return problems
}
