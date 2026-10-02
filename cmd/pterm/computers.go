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

	"github.com/intojhanurag/phone-terminal/internal/gateway"
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
			switch status.Trust(origin, pinned, device.AppNode.Tags) {
			case tailnet.Changed:
				findings = append(findings, origin+": node ID changed (reused name?)")
			case tailnet.Untrusted:
				if !tailnet.SameTags(peer.Tags, device.AppNode.Tags) {
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
// Another port on the same host is no fix: cookies ignore ports, so a handler
// there still receives the Termivoa cookie (otherPortsWarning).
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
		"every computer added to it. Move each one to another computer or to its own Tailscale Service hostname, "+
		"or stop serving it; another port on this computer does not help, because browsers send the Termivoa cookie "+
		"to every port of a host. Then remove it here: %s.",
		dnsHost(dnsName), strings.Join(handlers, ", "), strings.Join(remove, ", "))
}

// otherPortHandlers lists Serve handlers on other ports of this computer's
// own host: HTTPS handlers ("HTTPS 8080 /") and TCP forwards. Browsers send
// the Termivoa cookie to every port of a host, so each one receives it.
// Port 443 is otherServeHandlers'; Termivoa's own preview handler on 8443 is
// left out, since the gateway strips the cookie there.
func otherPortHandlers(data []byte, dnsName string) []string {
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
	for port, tcp := range raw.TCP {
		if port != "443" && tcp.TCPForward != "" {
			found = append(found, "TCP "+port+" forward to "+tcp.TCPForward)
		}
	}
	for hostPort, web := range raw.Web {
		host, port, ok := strings.Cut(strings.ToLower(hostPort), ":")
		if !ok || host != dnsHost(dnsName) || port == "443" {
			continue
		}
		for path, handler := range web.Handlers {
			if port == gateway.PreviewPublicPort && path == "/" &&
				strings.TrimSuffix(handler.Proxy, "/") == strings.TrimSuffix(lifecycle.PreviewLocalURL, "/") {
				continue
			}
			found = append(found, "HTTPS "+port+" "+path)
		}
	}
	slices.Sort(found)
	return found
}

// otherPortsWarning is a warning, not a problem: setup does not refuse other
// ports. A handler there is another origin, so it cannot read the app or call
// the API from the browser; it only receives the cookie when the phone opens
// it, which takes the owner choosing to serve and to open it. Refusing would
// break common setups (a dev tool or dashboard on the same computer).
func otherPortsWarning(handlers []string) string {
	return "tailscale serve (warning): other ports of this computer's host receive the Termivoa cookie, because browsers " +
		"send it to every port of a host: " + strings.Join(handlers, ", ") + ". What runs there can act as any phone that opens it. " +
		"Unless you trust it as much as Termivoa, move it to another computer or to its own Tailscale Service hostname, or remove it."
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
		if device.AppOrigin != nil { // revoked too: the CORS list keeps them so the app can read the 401
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
