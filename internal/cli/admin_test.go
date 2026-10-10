package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	adminDevice  = "20000000-0000-4000-8000-000000000001"
	adminPending = "20000000-0000-4000-8000-000000000002"
	adminProject = "20000000-0000-4000-8000-000000000003"
	adminGrant   = "Zq9_-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"
	adminOrigin  = "https://desk.example.ts.net"
	hostileLabel = "Phone\x1b]0;owned\x07\x1b[2J\u202e"
)

type fakeAdmin struct {
	grant       protocol.AdminRPCPairGrantResultPayload
	grantCalls  int
	pending     []protocol.AdminRPCPendingPairing
	approved    []string
	approveErr  error
	devices     []protocol.AdminRPCDevice
	revoked     []string
	revoke      protocol.AdminRPCDeviceRevokeResultPayload
	revokeErr   error
	addedPath   string
	addedName   *string
	project     protocol.AdminRPCProject
	projects    []protocol.AdminRPCProject
	profiles    []protocol.AdminRPCShellProfile
	origin      *string
	originCalls []*string
	renamed     []string
	afterGrant  func(*fakeAdmin) // runs after a grant is created

	// multi-computer
	boundRequests []protocol.AdminRPCPairGrantPayload
	revokeVia     protocol.AdminRPCRevokeViaResultPayload
	revokeViaErr  error
	revokedVia    []string
	computers     []protocol.AdminRPCComputer
	removeResult  protocol.AdminRPCComputerRemoveResultPayload
	removed       []string

	// Found on your network: replies[i] answers offer i; the last repeats.
	// No replies: every offer is delivered to mac.
	offers     []string
	replies    []offerReply
	afterOffer func(admin *fakeAdmin, call int)
	ends       []*string
}

type offerReply struct {
	result protocol.AdminRPCPairOfferResultPayload
	err    error
}

func (admin *fakeAdmin) OfferPairing(_ context.Context, via string) (protocol.AdminRPCPairOfferResultPayload, error) {
	admin.offers = append(admin.offers, via)
	if admin.afterOffer != nil {
		admin.afterOffer(admin, len(admin.offers))
	}
	if len(admin.replies) == 0 {
		return offered(""), nil
	}
	reply := admin.replies[min(len(admin.offers), len(admin.replies))-1]
	return reply.result, reply.err
}

func (admin *fakeAdmin) EndOffer(ctx context.Context, link *string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	admin.ends = append(admin.ends, link)
	return nil
}

// offered is an offer result for mac: delivered, or the problem named.
func offered(problem protocol.AdminRPCPairOfferProblem) protocol.AdminRPCPairOfferResultPayload {
	result := protocol.AdminRPCPairOfferResultPayload{AppOrigin: homeOrigin, AppNode: *owned("ada@example.com"),
		ExpiresAt: "2026-09-27T00:02:00Z", Delivered: problem == ""}
	if problem != "" {
		result.Problem = &problem
	}
	return result
}

func (admin *fakeAdmin) CreateBoundPairingGrant(_ context.Context, request protocol.AdminRPCPairGrantPayload) (protocol.AdminRPCPairGrantResultPayload, error) {
	admin.boundRequests = append(admin.boundRequests, request)
	if admin.afterGrant != nil {
		admin.afterGrant(admin)
	}
	return admin.grant, nil
}

func (admin *fakeAdmin) RevokeDevicesVia(_ context.Context, appOrigin string) (protocol.AdminRPCRevokeViaResultPayload, error) {
	admin.revokedVia = append(admin.revokedVia, appOrigin)
	return admin.revokeVia, admin.revokeViaErr
}

func (admin *fakeAdmin) Computers(context.Context) ([]protocol.AdminRPCComputer, error) {
	return admin.computers, nil
}

func (admin *fakeAdmin) RemoveComputer(_ context.Context, origin string) (protocol.AdminRPCComputerRemoveResultPayload, error) {
	admin.removed = append(admin.removed, origin)
	return admin.removeResult, nil
}

// The multi-computer methods are part of Admin itself, not behind a type
// assertion with a fallback that never runs; the real client implements them.
func TestAdminIncludesMultiComputerMethods(t *testing.T) {
	var _ Admin = (*localclient.AdminClient)(nil)
	_, _, _, _ = Admin.CreateBoundPairingGrant, Admin.RevokeDevicesVia, Admin.Computers, Admin.RemoveComputer
}

func (admin *fakeAdmin) CreatePairingGrant(context.Context) (protocol.AdminRPCPairGrantResultPayload, error) {
	admin.grantCalls++
	if admin.afterGrant != nil {
		admin.afterGrant(admin)
	}
	return admin.grant, nil
}

func (admin *fakeAdmin) PendingPairings(context.Context) ([]protocol.AdminRPCPendingPairing, error) {
	return admin.pending, nil
}

func (admin *fakeAdmin) ApprovePairing(_ context.Context, pendingID string) error {
	admin.approved = append(admin.approved, pendingID)
	return admin.approveErr
}

func (admin *fakeAdmin) Devices(context.Context) ([]protocol.AdminRPCDevice, error) {
	return admin.devices, nil
}

func (admin *fakeAdmin) RevokeDevice(_ context.Context, deviceID string) (protocol.AdminRPCDeviceRevokeResultPayload, error) {
	admin.revoked = append(admin.revoked, deviceID)
	admin.computers = nil // revocation deletes the phone's computer list
	return admin.revoke, admin.revokeErr
}

func (admin *fakeAdmin) RenameDevice(_ context.Context, deviceID, label string) error {
	admin.renamed = append(admin.renamed, deviceID+"="+label)
	return nil
}

func (admin *fakeAdmin) AddProject(_ context.Context, path string, name *string) (protocol.AdminRPCProject, error) {
	admin.addedPath, admin.addedName = path, name
	return admin.project, nil
}

func (admin *fakeAdmin) Projects(context.Context) ([]protocol.AdminRPCProject, error) {
	return admin.projects, nil
}

func (admin *fakeAdmin) ShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error) {
	return admin.profiles, nil
}

func (admin *fakeAdmin) DetectShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error) {
	return admin.profiles, nil
}

func (admin *fakeAdmin) PairingOrigin(_ context.Context, origin *string) (*string, error) {
	admin.originCalls = append(admin.originCalls, origin)
	if origin != nil {
		admin.origin = origin
	}
	return admin.origin, nil
}

func testGrant(origin *string) protocol.AdminRPCPairGrantResultPayload {
	return protocol.AdminRPCPairGrantResultPayload{Grant: adminGrant, ExpiresAt: "2026-09-27T00:02:00Z", Origin: origin}
}

func TestPairPrintsGrantOnlyInsideLink(t *testing.T) {
	origin := adminOrigin
	for _, test := range []struct {
		name, flag, want string
		configured       *string
	}{
		{name: "configured", configured: &origin, want: adminOrigin},
		{name: "flag overrides", flag: "http://127.0.0.1:8788", configured: &origin, want: "http://127.0.0.1:8788"},
		{name: "flag only", flag: adminOrigin, want: adminOrigin},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := &fakeAdmin{grant: testGrant(test.configured)}
			var output bytes.Buffer
			if err := Pair(context.Background(), admin, PairOptions{Origin: test.flag}, &output); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			if strings.Count(text, adminGrant) != 1 {
				t.Fatalf("grant appears %d times: %q", strings.Count(text, adminGrant), text)
			}
			link := test.want + "/pair#" + adminGrant
			lines := strings.Split(text, "\n")
			found := false
			for _, line := range lines {
				if strings.Contains(line, adminGrant) {
					if line != link {
						t.Fatalf("grant line = %q, want exactly %q", line, link)
					}
					found = true
				}
			}
			if !found || !strings.Contains(text, "2026-09-27T00:02:00Z") ||
				!strings.Contains(text, "`pterm pair approve <pending-id>`") || strings.Contains(text, "\x1b[") ||
				!strings.Contains(text, "Open this link") {
				t.Fatalf("unexpected pairing output %q", text)
			}
		})
	}
}

func TestPairWithoutOriginExplainsHowToConfigure(t *testing.T) {
	admin := &fakeAdmin{grant: testGrant(nil)}
	var output bytes.Buffer
	err := Pair(context.Background(), admin, PairOptions{ShowQR: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "pterm config origin https://<your-private-host>") ||
		!strings.Contains(err.Error(), "--origin") || strings.Contains(err.Error(), adminGrant) {
		t.Fatalf("unexpected missing-origin error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("missing origin printed %q", output.String())
	}
}

func TestPairRejectsInvalidOriginFlagBeforeCreatingGrant(t *testing.T) {
	for _, flag := range []string{"http://example.com", "https://Example.com", "https://host/path", "ftp://host"} {
		admin := &fakeAdmin{grant: testGrant(nil)}
		if err := Pair(context.Background(), admin, PairOptions{Origin: flag, ShowQR: true}, &bytes.Buffer{}); err == nil {
			t.Fatalf("origin %q accepted", flag)
		}
		if admin.grantCalls != 0 {
			t.Fatalf("origin %q created a grant", flag)
		}
	}
}

func TestPairPendingAndDevicesEscapeUntrustedLabels(t *testing.T) {
	admin := &fakeAdmin{
		pending: []protocol.AdminRPCPendingPairing{{
			ID: adminPending, VerificationCode: "123-456", DeviceLabel: hostileLabel, ExpiresAt: "2026-09-27T00:05:00Z",
		}},
		devices: []protocol.AdminRPCDevice{{
			ID: adminDevice, Label: hostileLabel, Revoked: true, Status: protocol.AdminRPCDeviceStatusRevoked,
			CreatedAt: "2026-09-27T00:00:00Z", LastUsedAt: "2026-09-27T00:01:00Z",
		}},
		projects: []protocol.AdminRPCProject{{ID: adminProject, Name: hostileLabel, Path: "/tmp/\x1b[31mred"}},
		profiles: []protocol.AdminRPCShellProfile{{
			ID: adminProject, Name: hostileLabel, Executable: "/bin/zsh", Arguments: []string{"-l", "\x1b[0m"},
		}},
	}
	var output bytes.Buffer
	for _, run := range []func() error{
		func() error { return PairPending(context.Background(), admin, &output) },
		func() error { return Devices(context.Background(), admin, &output) },
		func() error { return ProjectList(context.Background(), admin, &output) },
		func() error { return ProfileList(context.Background(), admin, &output) },
	} {
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	text := output.String()
	if strings.ContainsAny(text, "\x1b\x07\u202e") {
		t.Fatalf("terminal controls reached output: %q", text)
	}
	for _, want := range []string{"123-456", `"Phone\x1b]0;owned\a\x1b[2J\u202e"`, "revoked", `"/bin/zsh" "-l"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q missing %q", text, want)
		}
	}
}

func TestAdminListsReportEmpty(t *testing.T) {
	admin := &fakeAdmin{}
	var output bytes.Buffer
	for _, run := range []func() error{
		func() error { return PairPending(context.Background(), admin, &output) },
		func() error { return Devices(context.Background(), admin, &output) },
		func() error { return ProjectList(context.Background(), admin, &output) },
		func() error { return ProfileDetect(context.Background(), admin, &output) },
	} {
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	want := "No pending pairings.\nNo paired devices.\nNo saved projects.\nNo shell profiles.\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestDevicesPrintsEachStatus(t *testing.T) {
	admin := &fakeAdmin{devices: []protocol.AdminRPCDevice{
		{ID: adminDevice, Label: "Active phone", Status: protocol.Active,
			CreatedAt: "2026-09-27T00:00:00Z", LastUsedAt: "2026-09-27T00:01:00Z"},
		{ID: adminPending, Label: "Idle phone", Status: protocol.Expired,
			CreatedAt: "2026-09-01T00:00:00Z", LastUsedAt: "2026-09-01T00:01:00Z"},
		{ID: adminProject, Label: "Revoked phone", Revoked: true, Status: protocol.AdminRPCDeviceStatusRevoked,
			CreatedAt: "2026-08-01T00:00:00Z", LastUsedAt: "2026-08-01T00:01:00Z"},
	}}
	var output bytes.Buffer
	if err := Devices(context.Background(), admin, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"active", "expired", "revoked"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q missing status %q", text, want)
		}
	}
}

func TestPairApproveValidatesIDAndReportsUncertainty(t *testing.T) {
	admin := &fakeAdmin{}
	if err := PairApprove(context.Background(), admin, "not-a-uuid", &bytes.Buffer{}); err == nil || len(admin.approved) != 0 {
		t.Fatalf("invalid pending ID sent: %v", err)
	}
	var output bytes.Buffer
	if err := PairApprove(context.Background(), admin, adminPending, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Approved "+adminPending+". The phone finishes pairing on its next poll.\n" {
		t.Fatalf("unexpected approval output %q", output.String())
	}
	admin.approveErr = uncertainTestError{}
	if err := PairApprove(context.Background(), admin, adminPending, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("unexpected uncertain approval error: %v", err)
	}
}

func TestRevokeConfirmsExactDeviceAndSendsOnce(t *testing.T) {
	admin := &fakeAdmin{
		devices: []protocol.AdminRPCDevice{{ID: adminDevice, Label: hostileLabel}},
		revoke:  protocol.AdminRPCDeviceRevokeResultPayload{DeviceID: adminDevice, ClosedStreams: 2},
	}
	confirmer := &fakeConfirmer{answer: true}
	var output bytes.Buffer
	if err := Revoke(context.Background(), admin, adminDevice, confirmer, &output); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(confirmer.prompt, "\x1b\x07\u202e") || !strings.Contains(confirmer.prompt, adminDevice) ||
		!strings.HasPrefix(confirmer.prompt, "Revoke browser access for device ") {
		t.Fatalf("unsafe or incomplete prompt %q", confirmer.prompt)
	}
	if len(admin.revoked) != 1 || output.String() != "Revoked "+adminDevice+"; closed 2 live connection(s).\n" {
		t.Fatalf("revoked=%v output=%q", admin.revoked, output.String())
	}
}

func TestRevokeDeclinedDoesNotSend(t *testing.T) {
	admin := &fakeAdmin{devices: []protocol.AdminRPCDevice{{ID: adminDevice, Label: "Phone"}}}
	confirmer := &fakeConfirmer{answer: false}
	var output bytes.Buffer
	if err := Revoke(context.Background(), admin, adminDevice, confirmer, &output); err != nil {
		t.Fatal(err)
	}
	if confirmer.calls != 1 || len(admin.revoked) != 0 || output.String() != "Revocation cancelled.\n" {
		t.Fatalf("calls=%d revoked=%v output=%q", confirmer.calls, admin.revoked, output.String())
	}
}

func TestRevokeMissingOrInvalidDeviceDoesNotPrompt(t *testing.T) {
	admin := &fakeAdmin{devices: []protocol.AdminRPCDevice{{ID: adminPending, Label: "Other"}}}
	confirmer := &fakeConfirmer{answer: true}
	for _, id := range []string{adminDevice, "20000000-0000-4000-8000-00000000000G", strings.ToUpper(adminDevice)} {
		if err := Revoke(context.Background(), admin, id, confirmer, &bytes.Buffer{}); err == nil {
			t.Fatalf("revoke %q succeeded", id)
		}
	}
	if confirmer.calls != 0 || len(admin.revoked) != 0 {
		t.Fatalf("prompted %d times, revoked %v", confirmer.calls, admin.revoked)
	}
}

func TestRevokeUncertainDeliveryRequiresInspection(t *testing.T) {
	admin := &fakeAdmin{
		devices:   []protocol.AdminRPCDevice{{ID: adminDevice, Label: "Phone"}},
		revokeErr: uncertainTestError{},
	}
	err := Revoke(context.Background(), admin, adminDevice, &fakeConfirmer{answer: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "uncertain—run pterm devices") || len(admin.revoked) != 1 {
		t.Fatalf("unexpected uncertain revocation: %v (%d sends)", err, len(admin.revoked))
	}
}

func TestProjectAddSendsAbsolutePath(t *testing.T) {
	name := "Web"
	admin := &fakeAdmin{project: protocol.AdminRPCProject{ID: adminProject, Name: "Web", Path: "/canonical/web"}}
	var output bytes.Buffer
	if err := ProjectAdd(context.Background(), admin, "relative/web", &name, &output); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(admin.addedPath) || !strings.HasSuffix(admin.addedPath, filepath.Join("relative", "web")) ||
		admin.addedName != &name {
		t.Fatalf("sent path=%q name=%v", admin.addedPath, admin.addedName)
	}
	if output.String() != `Saved project "Web" `+adminProject+` at "/canonical/web"`+"\n" {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestConfigOriginValidatesSetAndShows(t *testing.T) {
	admin := &fakeAdmin{}
	var output bytes.Buffer
	if err := ConfigOrigin(context.Background(), admin, nil, &output); err != nil ||
		!strings.HasPrefix(output.String(), "No pairing origin is configured.") {
		t.Fatalf("show empty = %q, %v", output.String(), err)
	}
	bad := "http://example.com"
	if err := ConfigOrigin(context.Background(), admin, &bad, &bytes.Buffer{}); err == nil || len(admin.originCalls) != 1 {
		t.Fatalf("invalid origin sent: %v (%d calls)", err, len(admin.originCalls))
	}
	value := adminOrigin
	output.Reset()
	if err := ConfigOrigin(context.Background(), admin, &value, &output); err != nil ||
		output.String() != "Pairing origin set to "+adminOrigin+"\n" {
		t.Fatalf("set = %q, %v", output.String(), err)
	}
	output.Reset()
	if err := ConfigOrigin(context.Background(), admin, nil, &output); err != nil ||
		output.String() != "Pairing origin: "+adminOrigin+"\n" {
		t.Fatalf("show = %q, %v", output.String(), err)
	}
}

// Pair waits for a phone that redeems this grant, shows its code, and
// approves only after the owner says yes. Pending pairings that existed
// before the grant are never offered.
func TestPairWaitsAndApprovesOnlyAfterConfirmation(t *testing.T) {
	origin := adminOrigin
	older := protocol.AdminRPCPendingPairing{ID: adminDevice, VerificationCode: "111-111", DeviceLabel: "Older", ExpiresAt: "2026-09-27T00:05:00Z"}
	for _, answer := range []bool{true, false} {
		admin := &fakeAdmin{grant: testGrant(&origin), pending: []protocol.AdminRPCPendingPairing{older}}
		admin.grant.ExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
		admin.afterGrant = func(admin *fakeAdmin) {
			admin.pending = append(admin.pending, protocol.AdminRPCPendingPairing{
				ID: adminPending, VerificationCode: "123-456", DeviceLabel: hostileLabel, ExpiresAt: "2026-09-27T00:05:00Z",
			})
		}
		confirmer := &fakeConfirmer{answer: answer}
		var output bytes.Buffer
		err := Pair(context.Background(), admin, PairOptions{Confirmer: confirmer, PollInterval: time.Millisecond}, &output)
		if answer && err != nil || !answer && !errors.Is(err, ErrNotPaired) {
			t.Fatalf("answer %v: %v", answer, err)
		}
		if confirmer.calls != 1 || !strings.Contains(confirmer.prompt, "shows code 123-456. Approve? [y/N]") ||
			strings.Contains(confirmer.prompt, "\x1b") || strings.Contains(confirmer.prompt, "111-111") {
			t.Fatalf("prompt %d times: %q", confirmer.calls, confirmer.prompt)
		}
		if answer && (len(admin.approved) != 1 || admin.approved[0] != adminPending) {
			t.Fatalf("approved = %v", admin.approved)
		}
		if !answer && (len(admin.approved) != 0 || !strings.Contains(output.String(), "Not approved; that phone stays unpaired. Run `pterm pair` to try again.")) {
			t.Fatalf("declined pairing approved %v: %q", admin.approved, output.String())
		}
	}
}

func TestPairStopsWaitingWhenTheGrantExpires(t *testing.T) {
	origin := adminOrigin
	admin := &fakeAdmin{grant: testGrant(&origin)}
	admin.grant.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	confirmer := &fakeConfirmer{answer: true}
	err := Pair(context.Background(), admin, PairOptions{Confirmer: confirmer, PollInterval: time.Millisecond}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), "run `pterm pair` again") || confirmer.calls != 0 {
		t.Fatalf("expired wait = %v after %d prompts", err, confirmer.calls)
	}
}

// An add-computer wait that ends without an approved phone names the
// command that makes a new add-computer link, not the first-computer one.
func TestPairViaEndingWithoutAPhoneNamesTheViaCommand(t *testing.T) {
	admin := &fakeAdmin{grant: boundGrant(homeOrigin, &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server"}})}
	admin.grant.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	options := PairOptions{Via: "mac", AllowTagged: true, Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}
	err := Pair(context.Background(), admin, options, &bytes.Buffer{})
	if err == nil || errors.Is(err, ErrNotPaired) || !strings.Contains(err.Error(), "run `pterm pair --via mac --allow-tagged` again") {
		t.Fatalf("expired --via wait = %v", err)
	}

	admin = &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com"))}
	admin.grant.ExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	appOrigin := homeOrigin
	admin.afterGrant = func(admin *fakeAdmin) {
		admin.pending = append(admin.pending, protocol.AdminRPCPendingPairing{
			ID: adminPending, VerificationCode: "123-456", DeviceLabel: "Phone", ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &appOrigin,
		})
	}
	var output bytes.Buffer
	options = PairOptions{Via: "mac", Confirmer: &fakeConfirmer{answer: false}, PollInterval: time.Millisecond}
	if err := Pair(context.Background(), admin, options, &output); !errors.Is(err, ErrNotPaired) ||
		!strings.Contains(output.String(), "Not approved; that phone stays unpaired. Run `pterm pair --via mac` to try again.") {
		t.Fatalf("declined --via: %v\n%s", err, output.String())
	}
}

func TestPairFallsBackToTheLocalPage(t *testing.T) {
	admin := &fakeAdmin{grant: testGrant(nil)}
	var output bytes.Buffer
	if err := Pair(context.Background(), admin, PairOptions{Fallback: "http://127.0.0.1:8787"}, &output); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "\nhttp://127.0.0.1:8787/pair#"+adminGrant+"\n") ||
		!strings.Contains(text, "works only on this computer") || !strings.Contains(text, "pterm setup") {
		t.Fatalf("fallback output = %q", text)
	}
}

func TestRenameDeviceTrimsAndValidates(t *testing.T) {
	admin := &fakeAdmin{}
	var output bytes.Buffer
	if err := RenameDevice(context.Background(), admin, adminDevice, "  Work phone ", &output); err != nil {
		t.Fatal(err)
	}
	if len(admin.renamed) != 1 || admin.renamed[0] != adminDevice+"=Work phone" || !strings.Contains(output.String(), `"Work phone"`) {
		t.Fatalf("rename = %v, %q", admin.renamed, output.String())
	}
	for _, test := range []struct{ id, label string }{{"bad", "x"}, {adminDevice, " "}, {adminDevice, "a\x1b"}, {adminDevice, strings.Repeat("x", 65)}} {
		if err := RenameDevice(context.Background(), admin, test.id, test.label, &output); err == nil {
			t.Fatalf("rename %q %q accepted", test.id, test.label)
		}
	}
	if len(admin.renamed) != 1 {
		t.Fatalf("invalid renames reached the broker: %v", admin.renamed)
	}
}

const (
	homeOrigin = "https://mac.tailx.ts.net"
	peerOrigin = "https://vps.tailx.ts.net"
)

func owned(owner string) *protocol.TailnetNode {
	return &protocol.TailnetNode{ID: "nMac1CNTRL", Owner: &owner, Tags: []string{}}
}

func boundGrant(appOrigin string, node *protocol.TailnetNode) protocol.AdminRPCPairGrantResultPayload {
	origin := peerOrigin
	grant := testGrant(&origin)
	grant.AppOrigin, grant.AppNode = &appOrigin, node
	return grant
}

func TestPairViaPrintsOnlyTheAddComputerLink(t *testing.T) {
	link := peerOrigin + "/add-computer#grant=" + adminGrant + "&via=https%3A%2F%2Fmac.tailx.ts.net"
	for _, test := range []struct {
		via         string
		allowTagged bool
		node        *protocol.TailnetNode
		describe    string
	}{
		{via: "mac", node: owned("ada@example.com"), describe: "mac (https://mac.tailx.ts.net, owner ada@example.com)"},
		{via: homeOrigin, node: owned("ada@example.com"), describe: "mac (https://mac.tailx.ts.net, owner ada@example.com)"},
		{via: "mac", allowTagged: true, node: &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server", "tag:home"}},
			describe: "mac (https://mac.tailx.ts.net, tags tag:server, tag:home)"},
	} {
		admin := &fakeAdmin{grant: boundGrant(homeOrigin, test.node)}
		var output bytes.Buffer
		if err := Pair(context.Background(), admin, PairOptions{
			Via: test.via, AllowTagged: test.allowTagged, Fallback: "http://127.0.0.1:8787",
		}, &output); err != nil {
			t.Fatal(err)
		}
		text := output.String()
		if admin.grantCalls != 0 || len(admin.boundRequests) != 1 || *admin.boundRequests[0].Via != test.via ||
			(admin.boundRequests[0].AllowTagged != nil) != test.allowTagged {
			t.Fatalf("grant calls=%d requests=%+v", admin.grantCalls, admin.boundRequests)
		}
		if strings.Count(text, adminGrant) != 1 || !strings.Contains(text, "\n"+link+"\n") || strings.Contains(text, "\x1b[") ||
			!strings.Contains(text, "in the Termivoa app at "+test.describe+":") ||
			!strings.Contains(text, `if this terminal is a Termivoa session in that app, tap the link, then confirm "Add this computer?"; `+
				`or in Settings → + Add a computer, paste the link.`) ||
			!strings.Contains(text, "single-use secret") {
			t.Fatalf("via %q output = %q", test.via, text)
		}
	}
}

// On a terminal, --via also draws the link as a QR code and says where to
// scan it. A terminal narrower than the code (a phone-sized Termivoa session)
// gets the link only.
func TestPairViaQRSaysWhereToScan(t *testing.T) {
	admin := &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com"))}
	var output bytes.Buffer
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac", ShowQR: true, QRColumns: 80}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	// Where to scan comes first, then the code, then the link.
	scan := "Scan this QR code in the Termivoa app: Settings → + Add a computer → Scan QR.\n" +
		"The phone's Camera app will NOT work for this code: it opens the browser, not the app.\n" + qrColors
	if !strings.HasPrefix(text, scan) || strings.Index(text, qrColors) > strings.Index(text, "Add-computer link") ||
		!strings.Contains(text, `or in Settings → + Add a computer, paste the link.`) || strings.Contains(text, "Safari") {
		t.Errorf("--via QR output:\n%s", text)
	}
	if strings.Count(text, adminGrant) != 1 {
		t.Errorf("the grant appears %d times", strings.Count(text, adminGrant))
	}

	// Too narrow: the link alone, without a word about the missing code.
	output.Reset()
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac", ShowQR: true, QRColumns: 40}, &output); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); strings.Contains(text, "\x1b[") || strings.Contains(text, "QR code:") || strings.Contains(text, "Scan") ||
		!strings.HasPrefix(text, "Add-computer link") || !strings.Contains(text, "/add-computer#grant=") {
		t.Errorf("narrow terminal output = %q", text)
	}
}

func TestPairViaRefusesWhatItCannotBind(t *testing.T) {
	loopback := "http://127.0.0.1:8788"
	for name, test := range map[string]struct {
		admin   Admin
		options PairOptions
		grants  int
	}{
		"loopback fallback": {admin: &fakeAdmin{grant: func() protocol.AdminRPCPairGrantResultPayload {
			g := boundGrant(homeOrigin, owned("a"))
			g.Origin = nil
			return g
		}()}, options: PairOptions{Via: "mac", Fallback: loopback}, grants: 1},
		"loopback origin flag": {admin: &fakeAdmin{grant: boundGrant(homeOrigin, owned("a"))}, options: PairOptions{Via: "mac", Origin: loopback}},
		"other short name":     {admin: &fakeAdmin{grant: boundGrant("https://macbook.tailx.ts.net", owned("a"))}, options: PairOptions{Via: "mac"}, grants: 1},
		"other origin":         {admin: &fakeAdmin{grant: boundGrant("https://mac.other.ts.net", owned("a"))}, options: PairOptions{Via: homeOrigin}, grants: 1},
		"no app node":          {admin: &fakeAdmin{grant: boundGrant(homeOrigin, nil)}, options: PairOptions{Via: "mac"}, grants: 1},
		"no app origin":        {admin: &fakeAdmin{grant: testGrant(nil)}, options: PairOptions{Via: "mac"}, grants: 1},
		"invalid via":          {admin: &fakeAdmin{}, options: PairOptions{Via: "Mac"}},
		"tagged without via":   {admin: &fakeAdmin{}, options: PairOptions{AllowTagged: true}},
	} {
		var output bytes.Buffer
		err := Pair(context.Background(), test.admin, test.options, &output)
		if err == nil || strings.Contains(err.Error(), adminGrant) || output.Len() != 0 {
			t.Fatalf("%s: err=%v output=%q", name, err, output.String())
		}
		if fake, ok := test.admin.(*fakeAdmin); ok && (len(fake.boundRequests) != test.grants || fake.grantCalls != 0) {
			t.Fatalf("%s: %d bound grants, %d plain grants", name, len(fake.boundRequests), fake.grantCalls)
		}
		if strings.HasPrefix(name, "loopback") && !strings.Contains(err.Error(), "tailnet https address") {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestPairingPromptNamesTheApp(t *testing.T) {
	home := homeOrigin
	hostile := "ada\x1b[2J@example.com"
	for _, test := range []struct {
		item protocol.AdminRPCPendingPairing
		want string
	}{
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456", AppOrigin: &home, AppNode: owned("ada@example.com")},
			viaPrompt("iPhone", "123-456", "owner ada@example.com")},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456", AppOrigin: &home, AppNode: &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server"}}},
			viaPrompt("iPhone", "123-456", "tags tag:server")},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456", AppOrigin: &home, AppNode: owned(hostile)},
			viaPrompt("iPhone", "123-456", `owner "ada\x1b[2J@example.com"`)},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456"},
			"Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456. Approve? [y/N] "},
	} {
		if got := pairingPrompt(test.item, "vps"); got != test.want {
			t.Fatalf("prompt = %q, want %q", got, test.want)
		}
	}
}

// viaPrompt is the N5 prompt on vps for a pairing bound to the app on mac,
// whose owner (or tags) is owner.
func viaPrompt(label, code, owner string) string {
	return "Phone \"" + label + "\" wants to add this computer (vps) to your Termivoa app on mac.\n" +
		"mac is https://mac.tailx.ts.net (" + owner + ").\n" +
		"Approve only if your phone shows " + code + " and says \"type y in the terminal on vps\". [y/N] "
}

// With --via, only new pending pairings bound to that app are offered.
func TestPairViaOffersOnlyPairingsForItsApp(t *testing.T) {
	home, other := homeOrigin, "https://other.tailx.ts.net"
	admin := &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com"))}
	admin.grant.ExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	admin.afterGrant = func(admin *fakeAdmin) {
		admin.pending = []protocol.AdminRPCPendingPairing{
			{ID: adminDevice, VerificationCode: "111-111", DeviceLabel: "Other app", AppOrigin: &other, AppNode: owned("x")},
			{ID: adminProject, VerificationCode: "222-222", DeviceLabel: "Own page"},
			{ID: adminPending, VerificationCode: "123-456", DeviceLabel: "iPhone", AppOrigin: &home,
				AppNode: &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server"}}},
		}
	}
	confirmer := &fakeConfirmer{answer: true}
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: confirmer, PollInterval: time.Millisecond}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := viaPrompt("iPhone", "123-456", "tags tag:server")
	if confirmer.calls != 1 || confirmer.prompt != want || len(admin.approved) != 1 || admin.approved[0] != adminPending {
		t.Fatalf("calls=%d prompt=%q approved=%v", confirmer.calls, confirmer.prompt, admin.approved)
	}
}

func TestPairPendingAndDevicesShowVia(t *testing.T) {
	home := homeOrigin
	admin := &fakeAdmin{
		pending: []protocol.AdminRPCPendingPairing{
			{ID: adminPending, VerificationCode: "123-456", DeviceLabel: "A", ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &home},
			{ID: adminDevice, VerificationCode: "654-321", DeviceLabel: "B", ExpiresAt: "2026-09-27T00:05:00Z"},
		},
		devices: []protocol.AdminRPCDevice{
			{ID: adminDevice, Label: "A", Status: protocol.Active, LastUsedAt: "2026-09-27T00:01:00Z", AppOrigin: &home},
			{ID: adminPending, Label: "B", Status: protocol.Active, LastUsedAt: "2026-09-27T00:01:00Z"},
		},
	}
	var output bytes.Buffer
	if err := PairPending(context.Background(), admin, &output); err != nil {
		t.Fatal(err)
	}
	if err := Devices(context.Background(), admin, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 || !strings.HasSuffix(lines[0], "VIA") || !strings.HasSuffix(lines[1], homeOrigin) || !strings.HasSuffix(lines[2], " -") ||
		!strings.HasSuffix(lines[3], "VIA") || !strings.HasSuffix(lines[4], homeOrigin) || !strings.HasSuffix(lines[5], " -") {
		t.Fatalf("tables = %q", output.String())
	}
}

// RevokeVia always sends the request after confirmation, even when no device
// uses the app: the broker also removes that app's pairing links and pending
// pairings. The device count only changes the prompt.
func TestRevokeViaCountsConfirmsAndChecksTheAcknowledgement(t *testing.T) {
	home, other := homeOrigin, "https://other.tailx.ts.net"
	devices := []protocol.AdminRPCDevice{
		{ID: adminDevice, AppOrigin: &home}, {ID: adminPending, AppOrigin: &home},
		{ID: adminProject, AppOrigin: &home, Revoked: true}, {ID: adminGrant, AppOrigin: &other}, {ID: "own"},
	}
	ack := protocol.AdminRPCRevokeViaResultPayload{AppOrigin: homeOrigin, DeviceIDS: []string{adminDevice, adminPending}, ClosedStreams: 3}
	prompt := "Revoke browser access for 2 device(s) that use this computer from the Termivoa app at https://mac.tailx.ts.net? Their live connections close now. [y/N] "
	nonePrompt := "No device uses this computer from the Termivoa app at https://mac.tailx.ts.net now, but its pending pairings " +
		"and pairing links will be removed. Continue? [y/N] "
	for name, test := range map[string]struct {
		devices []protocol.AdminRPCDevice
		answer  bool
		ack     protocol.AdminRPCRevokeViaResultPayload
		ackErr  error
		prompt  string
		sends   int
		output  string
		err     string
	}{
		"no device": {devices: devices[2:], answer: true, ack: protocol.AdminRPCRevokeViaResultPayload{AppOrigin: homeOrigin},
			prompt: nonePrompt, sends: 1, output: "Revoked 0 device(s) bound to https://mac.tailx.ts.net; closed 0 live connection(s).\n"},
		"no device cancelled": {devices: devices[2:], prompt: nonePrompt, output: "Revocation cancelled.\n"},
		"cancelled":           {devices: devices, prompt: prompt, output: "Revocation cancelled.\n"},
		"revoked":             {devices: devices, answer: true, ack: ack, prompt: prompt, sends: 1, output: "Revoked 2 device(s) bound to https://mac.tailx.ts.net; closed 3 live connection(s).\n"},
		"wrong ack":           {devices: devices, answer: true, ack: protocol.AdminRPCRevokeViaResultPayload{AppOrigin: other}, prompt: prompt, sends: 1, err: "wrong app origin"},
		"uncertain":           {devices: devices, answer: true, ackErr: uncertainTestError{}, prompt: prompt, sends: 1, err: "uncertain—run pterm devices"},
	} {
		fake := &fakeAdmin{devices: test.devices, revokeVia: test.ack, revokeViaErr: test.ackErr}
		confirmer := &fakeConfirmer{answer: test.answer}
		var output bytes.Buffer
		err := RevokeVia(context.Background(), fake, homeOrigin, confirmer, &output)
		if (err == nil) != (test.err == "") || err != nil && !strings.Contains(err.Error(), test.err) ||
			confirmer.calls != 1 || len(fake.revokedVia) != test.sends || output.String() != test.output ||
			confirmer.prompt != test.prompt {
			t.Fatalf("%s: err=%v prompts=%d %q sends=%d output=%q", name, err, confirmer.calls, confirmer.prompt, len(fake.revokedVia), output.String())
		}
	}
	if err := RevokeVia(context.Background(), &fakeAdmin{}, "https://mac.tailx.ts.net:443", &fakeConfirmer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("explicit port accepted")
	}
}

// On home, revoking a phone names the other computers it used, read before
// the revocation deletes them.
func TestRevokeListsThePhonesOtherComputers(t *testing.T) {
	fake := &fakeAdmin{
		devices: []protocol.AdminRPCDevice{{ID: adminDevice, Label: "Phone"}},
		revoke:  protocol.AdminRPCDeviceRevokeResultPayload{DeviceID: adminDevice, ClosedStreams: 1},
		computers: []protocol.AdminRPCComputer{
			{DeviceID: adminDevice, Label: "vps\x1b[2J", Origin: peerOrigin},
			{DeviceID: adminPending, Label: "other phone's", Origin: "https://linux.tailx.ts.net"},
		},
	}
	first := "Revoked " + adminDevice + "; closed 1 live connection(s).\n"
	var output bytes.Buffer
	if err := Revoke(context.Background(), fake, adminDevice, &fakeConfirmer{answer: true}, &output); err != nil {
		t.Fatal(err)
	}
	want := first + "This phone also used these computers. Run `pterm revoke` on each:\n  \"vps\\x1b[2J\" (https://vps.tailx.ts.net)\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestComputersListAndRemove(t *testing.T) {
	fake := &fakeAdmin{
		computers:    []protocol.AdminRPCComputer{{DeviceID: adminDevice, Label: "vps\x1b", Origin: peerOrigin, CreatedAt: "2026-10-01T00:00:00Z"}},
		removeResult: protocol.AdminRPCComputerRemoveResultPayload{Origin: peerOrigin, Removed: 2},
	}
	var output bytes.Buffer
	if err := Computers(context.Background(), fake, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output.String(), "\n")
	if !strings.HasPrefix(lines[0], "DEVICE ID") || !strings.Contains(lines[0], "LABEL") || !strings.Contains(lines[0], "ORIGIN") ||
		!strings.Contains(lines[0], "ADDED") || !strings.Contains(lines[1], `"vps\x1b"`) || !strings.Contains(lines[1], peerOrigin) {
		t.Fatalf("table = %q", output.String())
	}
	output.Reset()
	if err := Computers(context.Background(), &fakeAdmin{}, &output); err != nil || output.String() != "No computers.\n" {
		t.Fatalf("empty = %q, %v", output.String(), err)
	}
	output.Reset()
	if err := RemoveComputer(context.Background(), fake, peerOrigin, &output); err != nil {
		t.Fatal(err)
	}
	if want := "Removed https://vps.tailx.ts.net from 2 device list(s). This does not sign the phone out there: run `pterm revoke` on vps (https://vps.tailx.ts.net).\n"; output.String() != want {
		t.Fatalf("remove = %q", output.String())
	}
	fake.removeResult.Origin = homeOrigin
	if err := RemoveComputer(context.Background(), fake, peerOrigin, &bytes.Buffer{}); err == nil {
		t.Fatal("wrong acknowledgement accepted")
	}
	if err := RemoveComputer(context.Background(), fake, "https://VPS.tailx.ts.net", &bytes.Buffer{}); err == nil || len(fake.removed) != 2 {
		t.Fatalf("invalid origin: %v (%d sends)", err, len(fake.removed))
	}
}

// shortOffers runs offers every interval and ends a wait after wait.
func shortOffers(t *testing.T, interval, wait time.Duration) {
	t.Helper()
	offerInterval, offerWait = interval, wait
	t.Cleanup(func() { offerInterval, offerWait = OfferInterval, OfferWait })
}

const offerLink = peerOrigin + "/add-computer#grant=" + adminGrant + "&via=https%3A%2F%2Fmac.tailx.ts.net"

// liveGrant is a bound grant for mac whose link has not expired.
func liveGrant(node *protocol.TailnetNode) protocol.AdminRPCPairGrantResultPayload {
	grant := boundGrant(homeOrigin, node)
	grant.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	return grant
}

// The N4 screen, for each result of the first offer. Nobody adds the
// computer, so the wait ends with the timeout text, and the offer ends.
func TestPairViaOfferScreen(t *testing.T) {
	if OfferWait != 10*time.Minute || OfferInterval != 30*time.Second || offerWait != OfferWait || offerInterval != OfferInterval {
		t.Fatalf("wait %s, interval %s", OfferWait, OfferInterval)
	}
	shortOffers(t, time.Hour, 20*time.Millisecond)
	tail := "\nNo Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:\n" + offerLink + "\n" +
		"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n"
	waiting, waitingLong := "Waiting for your phone (Ctrl+C stops)…\n", "Waiting for your phone (up to 10 minutes; Ctrl+C stops)…\n"
	head := "Adding this computer (vps) to your Termivoa app on mac.\n"
	for name, test := range map[string]struct {
		reply offerReply
		line  string
	}{
		"delivered": {offerReply{result: offered("")}, "Sent to mac (https://mac.tailx.ts.net).\n" +
			"On your phone, open Termivoa → Settings → Computers and tap Add next to \"vps\".\n" + waitingLong},
		"unreachable":  {offerReply{result: offered(protocol.Unreachable)}, "mac did not answer. Use the QR code or the link below.\n" + waiting},
		"busy":         {offerReply{result: offered(protocol.Busy)}, "mac is busy right now. Use the QR code or the link below.\n" + waiting},
		"not accepted": {offerReply{result: offered(protocol.NotAccepted)}, "Termivoa on mac cannot receive this yet. Update it, or use the QR code or the link below.\n" + waiting},
		"no status here": {offerReply{err: &localclient.RemoteError{Code: protocol.HostUnavailable, Message: "x"}},
			"This computer cannot read its Tailscale status. Use the QR code or the link below.\n" + waiting},
		"rpc error": {offerReply{err: &localclient.RemoteError{Code: protocol.LimitExceeded, Message: "home says \x1b[2J"}},
			"This computer could not offer itself to mac. Use the QR code or the link below.\n" + waiting},
	} {
		admin := &fakeAdmin{grant: liveGrant(owned("ada@example.com")), replies: []offerReply{test.reply}}
		var output bytes.Buffer
		err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
		if err == nil || err.Error() != "Nobody added this computer in 10 minutes. Run pterm pair --via mac again." || !errors.Is(err, ErrNoPhone) {
			t.Fatalf("%s: wait ended with %v", name, err)
		}
		if want := head + test.line + tail; output.String() != want {
			t.Fatalf("%s: screen =\n%s\nwant\n%s", name, output.String(), want)
		}
		if len(admin.offers) != 1 || admin.offers[0] != "mac" || len(admin.ends) != 1 || *admin.ends[0] != adminGrant {
			t.Fatalf("%s: offers %v, ends %v", name, admin.offers, admin.ends)
		}
	}

	// No saved address for this computer (the link uses --origin): no offer.
	admin := &fakeAdmin{grant: liveGrant(owned("ada@example.com"))}
	admin.grant.Origin = nil
	var output bytes.Buffer
	_ = Pair(context.Background(), admin, PairOptions{Via: "mac", Origin: peerOrigin, Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
	if want := head + "Add needs this computer's address: run pterm setup, or pterm config origin https://<this computer>. Use the QR code or the link below.\n" +
		waiting + tail; output.String() != want || len(admin.offers) != 0 {
		t.Fatalf("no address: %d offers, screen =\n%s", len(admin.offers), output.String())
	}

	// A broker older than this pterm closes the connection on the offer.
	admin = &fakeAdmin{grant: liveGrant(owned("ada@example.com")), replies: []offerReply{{err: io.EOF}}}
	output.Reset()
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output); err == nil ||
		!strings.Contains(err.Error(), "Restart Termivoa on this computer (pterm shutdown, then pterm launch), then try again") || len(admin.ends) != 1 {
		t.Fatalf("old broker: %v, ends %v", err, admin.ends)
	}

	// With a QR code, the Camera sentence stays under the link.
	admin = &fakeAdmin{grant: liveGrant(owned("ada@example.com"))}
	output.Reset()
	_ = Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, ShowQR: true, QRColumns: 80, PollInterval: time.Millisecond}, &output)
	text := output.String()
	if !strings.Contains(text, "paste the link. They work for 2 minutes:\n"+qrColors) ||
		!strings.Contains(text, offerLink+"\nThe phone's Camera app will NOT work for this code: it opens the browser, not the app.\n"+
			"The link and QR code are a single-use secret") || strings.Count(text, adminGrant) != 1 {
		t.Fatalf("QR screen:\n%s", text)
	}
}

// When the link expires: with no offer delivered yet, the link was the only
// way, so the wait ends with the link text; after a delivered offer it says
// so once and goes on (Add still works).
func TestPairViaLinkExpiryDuringTheOfferWait(t *testing.T) {
	shortOffers(t, 2*time.Millisecond, 300*time.Millisecond)
	expiring := func(replies ...offerReply) *fakeAdmin {
		admin := &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com")), replies: replies}
		admin.grant.ExpiresAt = time.Now().Add(50 * time.Millisecond).UTC().Format(time.RFC3339Nano)
		return admin
	}
	admin := expiring(offerReply{result: offered(protocol.NotAccepted)})
	var output bytes.Buffer
	start := time.Now()
	err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
	if err == nil || err.Error() != "the link expired before a phone opened it; run `pterm pair --via mac` again" || !errors.Is(err, ErrNoPhone) ||
		time.Since(start) > 250*time.Millisecond || len(admin.ends) != 1 {
		t.Fatalf("never delivered: %v after %s\n%s", err, time.Since(start), output.String())
	}
	admin = expiring(offerReply{result: offered(protocol.NotAccepted)}, offerReply{result: offered("")})
	output.Reset()
	err = Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
	if err == nil || !strings.HasPrefix(err.Error(), "Nobody added this computer") ||
		strings.Count(output.String(), "The QR code and link above expired. Add in the app still works.\n") != 1 {
		t.Fatalf("delivered: %v\n%s", err, output.String())
	}
}

// Every interval the offer goes again; a line shows only when the result
// changes; an RPC error on a later call shows nothing.
func TestPairViaOfferResendsAndReportsChanges(t *testing.T) {
	shortOffers(t, 2*time.Millisecond, 300*time.Millisecond)
	rpcError := offerReply{err: &localclient.RemoteError{Code: protocol.LimitExceeded, Message: "x"}}
	admin := &fakeAdmin{grant: liveGrant(owned("ada@example.com")), replies: []offerReply{
		{result: offered(protocol.Unreachable)}, {result: offered(protocol.Unreachable)}, {result: offered("")}, rpcError,
		{result: offered("")}, {result: offered(protocol.Busy)}, rpcError, {result: offered(protocol.NotAccepted)},
	}}
	var output bytes.Buffer
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output); err == nil {
		t.Fatal("the wait did not time out")
	}
	_, after, _ := strings.Cut(output.String(), "screenshots or logs.\n")
	if want := "Sent to mac (https://mac.tailx.ts.net). Tap Add next to \"vps\" in the app.\n" +
		"mac is busy right now. Use the QR code or the link below.\n" +
		"Termivoa on mac cannot receive this yet. Update it, or use the QR code or the link below.\n"; after != want {
		t.Fatalf("change lines = %q", after)
	}
	if len(admin.offers) < 10 || len(admin.ends) != 1 {
		t.Fatalf("%d offers, %d ends", len(admin.offers), len(admin.ends))
	}
}

// A new pairing bound to mac gets the N5 prompt; the command ends on y, N,
// Ctrl+C or home_changed, and always ends the offer.
func TestPairViaOfferEndsOnEveryExit(t *testing.T) {
	shortOffers(t, 2*time.Millisecond, time.Minute)
	home := homeOrigin
	pendingAfter := func(calls int) func(*fakeAdmin, int) {
		return func(admin *fakeAdmin, call int) {
			if call == calls {
				admin.pending = []protocol.AdminRPCPendingPairing{{ID: adminPending, VerificationCode: "123-456",
					DeviceLabel: "iPhone", ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &home, AppNode: owned("ada@example.com")}}
			}
		}
	}
	for name, test := range map[string]struct {
		confirmer *fakeConfirmer
		replies   []offerReply
		cancel    bool
		want      string // error text; "" is success
	}{
		"y":          {confirmer: &fakeConfirmer{answer: true}},
		"N":          {confirmer: &fakeConfirmer{}, want: ErrNotPaired.Error()},
		"Ctrl+C":     {confirmer: &fakeConfirmer{}, cancel: true, want: context.Canceled.Error()},
		"home moved": {confirmer: &fakeConfirmer{}, replies: []offerReply{{result: offered("")}, {err: &localclient.RemoteError{Code: protocol.HomeChanged}}}, want: "mac is now a different Tailscale machine. Run pterm pair --via mac again."},
	} {
		admin := &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com")), replies: test.replies, afterOffer: pendingAfter(3)}
		ctx, cancel := context.WithCancel(context.Background())
		if test.cancel {
			admin.afterOffer = func(_ *fakeAdmin, call int) {
				if call == 3 {
					cancel()
				}
			}
		}
		err := Pair(ctx, admin, PairOptions{Via: "mac", Confirmer: test.confirmer, PollInterval: time.Millisecond}, &bytes.Buffer{})
		cancel()
		if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Fatalf("%s: %v", name, err)
		}
		if len(admin.ends) != 1 || *admin.ends[0] != adminGrant {
			t.Fatalf("%s: ends %v", name, admin.ends)
		}
		prompted := test.confirmer.calls == 1
		if prompted != (name == "y" || name == "N") || prompted && test.confirmer.prompt != viaPrompt("iPhone", "123-456", "owner ada@example.com") {
			t.Fatalf("%s: %d prompts: %q", name, test.confirmer.calls, test.confirmer.prompt)
		}
		if (name == "y") != (len(admin.approved) == 1) {
			t.Fatalf("%s: approved %v", name, admin.approved)
		}
	}
}

// --no-wait and piped input never offer. --allow-tagged offers only when
// neither computer is tagged, and the line names the tagged one.
func TestPairViaOffersOnlyInAnUntaggedWait(t *testing.T) {
	admin := &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com"))}
	var output bytes.Buffer
	if err := Pair(context.Background(), admin, PairOptions{Via: "mac"}, &output); err != nil || strings.Contains(output.String(), "tagged") || len(admin.offers) != 0 {
		t.Fatalf("--no-wait: %v\n%s", err, output.String())
	}
	// A tagged home: no offer; the link is the way, so the wait ends with it.
	admin = &fakeAdmin{grant: boundGrant(homeOrigin, &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server"}})}
	admin.grant.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	output.Reset()
	err := Pair(context.Background(), admin, PairOptions{Via: "mac", AllowTagged: true, Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
	if !errors.Is(err, ErrNoPhone) || !strings.Contains(output.String(), "\nAdd does not work with tagged computers: mac is tagged. Use the QR code or the link below.\n") ||
		len(admin.offers) != 0 || len(admin.ends) != 1 {
		t.Fatalf("tagged home: %v, %d offers\n%s", err, len(admin.offers), output.String())
	}
	// --allow-tagged with an untagged home: offered; when the broker refuses
	// because this computer is tagged, the line says so and offers stop.
	shortOffers(t, 2*time.Millisecond, 50*time.Millisecond)
	for name, test := range map[string]struct {
		reply offerReply
		line  string
		calls int
	}{
		"neither tagged": {offerReply{result: offered("")}, "Sent to mac (https://mac.tailx.ts.net).", 2},
		"this computer is tagged": {offerReply{err: &localclient.RemoteError{Code: protocol.InvalidRequest, Message: "x"}},
			"Add does not work with tagged computers: this computer is tagged. Use the QR code or the link below.", 1},
	} {
		admin = &fakeAdmin{grant: liveGrant(owned("ada@example.com")), replies: []offerReply{test.reply}}
		output.Reset()
		_ = Pair(context.Background(), admin, PairOptions{Via: "mac", AllowTagged: true, Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
		if !strings.Contains(output.String(), "\n"+test.line+"\n") || len(admin.offers) < test.calls || test.calls == 1 && len(admin.offers) != 1 {
			t.Fatalf("%s: %d offers\n%s", name, len(admin.offers), output.String())
		}
	}
	// Input that is not a terminal (a pipe) waits for the link, as before
	// Found, and offers nothing.
	admin = &fakeAdmin{grant: boundGrant(homeOrigin, owned("ada@example.com"))}
	admin.grant.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	output.Reset()
	err = Pair(context.Background(), admin, PairOptions{Via: "mac", NoOffer: true, Confirmer: &fakeConfirmer{}, PollInterval: time.Millisecond}, &output)
	if err == nil || !strings.HasPrefix(output.String(), "Add-computer link") || !strings.Contains(output.String(), "Waiting for the phone") ||
		strings.Contains(output.String(), "tagged") || len(admin.offers) != 0 || len(admin.ends) != 0 {
		t.Fatalf("piped: %v, offers %v, ends %v\n%s", err, admin.offers, admin.ends, output.String())
	}
}

// A plain pairing (no --via) never offers a pairing bound to another app: its
// prompt could not name this computer and that app (S2).
func TestPairOffersOnlyPlainPairings(t *testing.T) {
	origin, home := adminOrigin, homeOrigin
	admin := &fakeAdmin{grant: testGrant(&origin)}
	admin.grant.ExpiresAt = time.Now().Add(50 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	admin.afterGrant = func(admin *fakeAdmin) {
		admin.pending = []protocol.AdminRPCPendingPairing{{ID: adminPending, VerificationCode: "123-456", DeviceLabel: "iPhone",
			ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &home, AppNode: owned("ada@example.com")}}
	}
	confirmer := &fakeConfirmer{answer: true}
	err := Pair(context.Background(), admin, PairOptions{Confirmer: confirmer, PollInterval: time.Millisecond}, &bytes.Buffer{})
	if !errors.Is(err, ErrNoPhone) || confirmer.calls != 0 || len(admin.approved) != 0 {
		t.Fatalf("bound pairing offered to a plain pair: %v, %d prompts", err, confirmer.calls)
	}
}

// pipeConfirmer reads a pipe as pterm's fileConfirmer reads the terminal:
// a fresh FileConfirmationReader for each answer.
type pipeConfirmer struct {
	input  *os.File
	output io.Writer
}

func (confirmer pipeConfirmer) line(ctx context.Context, read func(*LineConfirmer) error) error {
	reader, err := NewFileConfirmationReader(confirmer.input)
	if err != nil {
		return err
	}
	defer reader.Close()
	line, err := NewLineConfirmer(reader, confirmer.output)
	if err != nil {
		return err
	}
	return read(line)
}

func (confirmer pipeConfirmer) Confirm(ctx context.Context, prompt string) (answer bool, err error) {
	return answer, confirmer.line(ctx, func(line *LineConfirmer) error {
		answer, err = line.Confirm(ctx, prompt)
		return err
	})
}

func (confirmer pipeConfirmer) Ask(ctx context.Context, prompt string) (answer string, err error) {
	return answer, confirmer.line(ctx, func(line *LineConfirmer) error {
		answer, err = line.Ask(ctx, prompt)
		return err
	})
}

// lockedBuffer is output that the test reads while Pair writes.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *lockedBuffer) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(buffer.String(), text); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("output lacks %q:\n%s", text, buffer.String())
		}
	}
}

// pendingLater is a fakeAdmin whose phone (a pairing bound to mac) arrives
// when the test says so.
type pendingLater struct {
	*fakeAdmin
	ready atomic.Bool
}

func (admin *pendingLater) PendingPairings(context.Context) ([]protocol.AdminRPCPendingPairing, error) {
	if !admin.ready.Load() {
		return nil, nil
	}
	home := homeOrigin
	return []protocol.AdminRPCPendingPairing{{ID: adminPending, VerificationCode: "123-456", DeviceLabel: "iPhone",
		ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &home, AppNode: owned("ada@example.com")}}, nil
}

// startTerminalWait runs `pterm pair --via mac` on a terminal (a pipe read
// like one) and returns its input, its output and its result.
func startTerminalWait(t *testing.T, admin Admin) (*os.File, *lockedBuffer, <-chan error) {
	t.Helper()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	output, done, finished := &lockedBuffer{}, make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		done <- Pair(ctx, admin, PairOptions{Via: "mac", Confirmer: pipeConfirmer{input, output}, PollInterval: time.Millisecond}, output)
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
		_ = writer.Close()
		_ = input.Close()
	})
	return writer, output, done
}

// After a delivered push the screen has no QR code and no link, only one
// line that says Enter shows them. Enter prints them (the grant once); the
// later y still reaches the approval prompt, which names this computer, the
// app's address and owner, and the phone's code-screen words. Success says
// where the computer is now.
func TestPairViaEnterShowsTheLinkAfterADeliveredPush(t *testing.T) {
	admin := &pendingLater{fakeAdmin: &fakeAdmin{grant: liveGrant(owned("ada@example.com"))}}
	input, output, done := startTerminalWait(t, admin)
	screen := "Adding this computer (vps) to your Termivoa app on mac.\n" +
		"Sent to mac (https://mac.tailx.ts.net).\n" +
		"On your phone, open Termivoa → Settings → Computers and tap Add next to \"vps\".\n" +
		"Waiting for your phone (up to 10 minutes; Ctrl+C stops)…\n" +
		"No Add button? Press Enter to show a QR code and a link.\n"
	output.waitFor(t, screen)
	time.Sleep(20 * time.Millisecond) // a few polls: nothing more is printed
	if output.String() != screen {
		t.Fatalf("screen =\n%s", output.String())
	}
	if _, err := input.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	link := "No Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:\n" + offerLink + "\n" +
		"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n"
	output.waitFor(t, link)
	admin.ready.Store(true)
	output.waitFor(t, "[y/N] ")
	if _, err := input.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := screen + link + viaPrompt("iPhone", "123-456", "owner ada@example.com") +
		"Done. vps is in your Termivoa app on mac. Open the app and tap it.\n"
	if output.String() != want || len(admin.approved) != 1 || admin.approved[0] != adminPending || strings.Count(want, adminGrant) != 1 {
		t.Fatalf("approved %v; output =\n%s\nwant\n%s", admin.approved, output.String(), want)
	}
}

// Keys pressed after Enter (a second Enter, a typed-ahead y) are dropped:
// only an answer typed at the prompt approves or declines.
func TestPairViaLaterKeysDoNotAnswerThePrompt(t *testing.T) {
	admin := &pendingLater{fakeAdmin: &fakeAdmin{grant: liveGrant(owned("ada@example.com"))}}
	input, output, done := startTerminalWait(t, admin)
	output.waitFor(t, "Press Enter to show a QR code and a link.\n")
	if _, err := input.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	output.waitFor(t, offerLink)
	if _, err := input.WriteString("\nn\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the reader drops both lines
	admin.ready.Store(true)
	output.waitFor(t, "[y/N] ")
	if _, err := input.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || len(admin.approved) != 1 {
		t.Fatalf("err %v, approved %v; output =\n%s", err, admin.approved, output.String())
	}
}

// A failed push prints the QR code and the link at once, as before, and no
// Enter line. A delivered push that fails later prints its line and then
// the QR code and the link it points to.
func TestPairViaFailedPushShowsTheLinkAtOnce(t *testing.T) {
	shortOffers(t, 2*time.Millisecond, time.Minute)
	admin := &pendingLater{fakeAdmin: &fakeAdmin{grant: liveGrant(owned("ada@example.com")), replies: []offerReply{{result: offered(protocol.Unreachable)}}}}
	_, output, _ := startTerminalWait(t, admin)
	link := "\nNo Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:\n" + offerLink + "\n"
	output.waitFor(t, "screenshots or logs.\n")
	if want := "Adding this computer (vps) to your Termivoa app on mac.\nmac did not answer. Use the QR code or the link below.\n" +
		"Waiting for your phone (Ctrl+C stops)…\n" + link; !strings.HasPrefix(output.String(), want) || strings.Contains(output.String(), "Press Enter") {
		t.Fatalf("screen =\n%s", output.String())
	}

	admin = &pendingLater{fakeAdmin: &fakeAdmin{grant: liveGrant(owned("ada@example.com")),
		replies: []offerReply{{result: offered("")}, {result: offered(protocol.Busy)}}}}
	_, output, _ = startTerminalWait(t, admin)
	output.waitFor(t, "screenshots or logs.\n")
	if _, after, _ := strings.Cut(output.String(), "Press Enter to show a QR code and a link.\n"); !strings.HasPrefix(after,
		"mac is busy right now. Use the QR code or the link below.\n"+link[1:]) {
		t.Fatalf("later failure:\n%s", output.String())
	}
}

// A link that expires before anyone asked for it is not mentioned; Enter
// after that says so and that Add still works.
func TestPairViaEnterAfterTheLinkExpired(t *testing.T) {
	admin := &pendingLater{fakeAdmin: &fakeAdmin{grant: liveGrant(owned("ada@example.com"))}}
	admin.grant.ExpiresAt = time.Now().Add(20 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	input, output, _ := startTerminalWait(t, admin)
	output.waitFor(t, "Press Enter")
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(output.String(), "expired") {
		t.Fatalf("an unseen link is said to expire:\n%s", output.String())
	}
	if _, err := input.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	output.waitFor(t, "Press Enter to show a QR code and a link.\nThe QR code and link expired (they work for 2 minutes). "+
		"Add in the app still works; for a new link, press Ctrl+C and run pterm pair --via mac again.\n")
	if strings.Contains(output.String(), adminGrant) {
		t.Fatalf("expired grant printed:\n%s", output.String())
	}
}

// A long machine name gets one hint after success.
func TestPairViaSuccessHintsAShortName(t *testing.T) {
	for self, tip := range map[string]bool{"vps": false, "anurags-macbook": true} {
		admin := &fakeAdmin{grant: liveGrant(owned("ada@example.com")), afterOffer: func(admin *fakeAdmin, _ int) {
			home := homeOrigin
			admin.pending = []protocol.AdminRPCPendingPairing{{ID: adminPending, VerificationCode: "123-456", DeviceLabel: "iPhone",
				ExpiresAt: "2026-09-27T00:05:00Z", AppOrigin: &home, AppNode: owned("ada@example.com")}}
		}}
		origin := "https://" + self + ".tailx.ts.net"
		admin.grant.Origin = &origin
		var output bytes.Buffer
		if err := Pair(context.Background(), admin, PairOptions{Via: "mac", Confirmer: &fakeConfirmer{answer: true}, PollInterval: time.Millisecond}, &output); err != nil {
			t.Fatal(err)
		}
		want := "\nDone. " + self + " is in your Termivoa app on mac. Open the app and tap it.\n"
		if tip {
			want += "Tip: give it a short name in the app: Settings, tap the computer, then Rename.\n"
		}
		if !strings.HasSuffix(output.String(), want) {
			t.Errorf("%s: output ends\n%s", self, output.String())
		}
	}
}
