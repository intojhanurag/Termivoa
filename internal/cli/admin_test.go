package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
