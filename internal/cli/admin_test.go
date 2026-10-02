package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
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
		if err != nil {
			t.Fatal(err)
		}
		if confirmer.calls != 1 || !strings.Contains(confirmer.prompt, "shows code 123-456. Approve? [y/N]") ||
			strings.Contains(confirmer.prompt, "\x1b") || strings.Contains(confirmer.prompt, "111-111") {
			t.Fatalf("prompt %d times: %q", confirmer.calls, confirmer.prompt)
		}
		if answer && (len(admin.approved) != 1 || admin.approved[0] != adminPending) {
			t.Fatalf("approved = %v", admin.approved)
		}
		if !answer && (len(admin.approved) != 0 || !strings.Contains(output.String(), "Not approved")) {
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
	if err == nil || !strings.Contains(err.Error(), "expired") || confirmer.calls != 0 {
		t.Fatalf("expired wait = %v after %d prompts", err, confirmer.calls)
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
			Via: test.via, AllowTagged: test.allowTagged, ShowQR: true, Fallback: "http://127.0.0.1:8787",
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
			!strings.Contains(text, `tap "Add this computer"`) || !strings.Contains(text, "Settings → Computers → Add computer") ||
			!strings.Contains(text, "single-use secret") {
			t.Fatalf("via %q output = %q", test.via, text)
		}
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
			"Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailx.ts.net, owner ada@example.com). Approve? [y/N] "},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456", AppOrigin: &home, AppNode: &protocol.TailnetNode{ID: "n1", Tags: []string{"tag:server"}}},
			"Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailx.ts.net, tags tag:server). Approve? [y/N] "},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456", AppOrigin: &home, AppNode: owned(hostile)},
			"Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailx.ts.net, owner \"ada\\x1b[2J@example.com\"). Approve? [y/N] "},
		{protocol.AdminRPCPendingPairing{DeviceLabel: "iPhone", VerificationCode: "123-456"},
			"Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456. Approve? [y/N] "},
	} {
		if got := pairingPrompt(test.item); got != test.want {
			t.Fatalf("prompt = %q, want %q", got, test.want)
		}
	}
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
	want := "Approve only if the phone shows exactly this code.\nPhone \"iPhone\" shows code 123-456 and will use this computer from the Termivoa app at mac (https://mac.tailx.ts.net, tags tag:server). Approve? [y/N] "
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
