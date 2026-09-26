package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Admin is the local administration surface used by pairing, device, project,
// profile and origin commands. Its concrete implementation is
// localclient.AdminClient over restricted CLI IPC.
type Admin interface {
	CreatePairingGrant(context.Context) (protocol.AdminRPCPairGrantResultPayload, error)
	PendingPairings(context.Context) ([]protocol.AdminRPCPendingPairing, error)
	ApprovePairing(context.Context, string) error
	Devices(context.Context) ([]protocol.AdminRPCDevice, error)
	RevokeDevice(context.Context, string) (protocol.AdminRPCDeviceRevokeResultPayload, error)
	AddProject(context.Context, string, *string) (protocol.AdminRPCProject, error)
	Projects(context.Context) ([]protocol.AdminRPCProject, error)
	ShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error)
	DetectShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error)
	PairingOrigin(context.Context, *string) (*string, error)
}

const setOriginHint = "run `pterm config origin https://<your-private-host>`"

// Pair creates one grant and prints it only inside the pairing link and,
// when showQR is set, a terminal QR code of that same link. The grant is the
// URL fragment by design, so it never reaches the server's request line; it
// must not appear anywhere else in output or errors.
func Pair(ctx context.Context, admin Admin, originFlag string, showQR bool, output io.Writer) error {
	if originFlag != "" {
		if err := protocol.ValidatePairingOrigin(originFlag); err != nil {
			return fmt.Errorf("invalid --origin: %w", err)
		}
	}
	grant, err := admin.CreatePairingGrant(ctx)
	if err != nil {
		return err
	}
	origin := originFlag
	if origin == "" && grant.Origin != nil {
		origin = *grant.Origin
	}
	if origin == "" {
		return fmt.Errorf("no pairing origin is configured; %s or pass --origin", setOriginHint)
	}
	if err := protocol.ValidatePairingOrigin(origin); err != nil {
		return fmt.Errorf("configured pairing origin is invalid; %s: %w", setOriginHint, err)
	}
	if protocol.ValidateSecretText(grant.Grant) != nil {
		return errors.New("broker returned a malformed pairing grant")
	}
	link := origin + "/pair#" + grant.Grant
	code, instruction := "", "Open this link"
	if showQR {
		if code, err = renderQR(link); err != nil {
			code = "No QR code: " + err.Error() + ".\n"
		} else {
			instruction = "Scan the QR code or open this link"
		}
	}
	return writeString(output, fmt.Sprintf(
		"%sPairing link (expires %s):\n%s\n"+
			"%s on the phone within 2 minutes. Then run `pterm pair pending` and approve with "+
			"`pterm pair approve <pending-id>` after comparing the code.\n"+
			"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n",
		code, grant.ExpiresAt, link, instruction))
}

func PairPending(ctx context.Context, admin Admin, output io.Writer) error {
	pending, err := admin.PendingPairings(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return writeString(output, "No pending pairings.\n")
	}
	return writeTable(output, "PENDING ID\tCODE\tLABEL\tEXPIRES", len(pending), func(index int) string {
		item := pending[index]
		return strings.Join([]string{item.ID, item.VerificationCode,
			displayText(item.DeviceLabel), item.ExpiresAt}, "\t")
	})
}

// PairApprove approves one pending pairing. It is never retried: a lost
// acknowledgement leaves the outcome for the owner to inspect.
func PairApprove(ctx context.Context, admin Admin, pendingID string, output io.Writer) error {
	if !validUUID(pendingID) {
		return errors.New("pending ID must be a canonical lowercase UUID")
	}
	if err := admin.ApprovePairing(ctx, pendingID); err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("approval outcome is uncertain—run pterm pair pending and pterm devices before approving again: %w", err)
		}
		return err
	}
	return writeString(output, fmt.Sprintf("Approved %s. The phone finishes pairing on its next poll.\n", pendingID))
}

func Devices(ctx context.Context, admin Admin, output io.Writer) error {
	devices, err := admin.Devices(ctx)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return writeString(output, "No paired devices.\n")
	}
	return writeTable(output, "DEVICE ID\tLABEL\tSTATUS\tLAST USED", len(devices), func(index int) string {
		device := devices[index]
		return strings.Join([]string{device.ID, displayText(device.Label),
			deviceStatus(device), device.LastUsedAt}, "\t")
	})
}

// Revoke resolves the exact device, asks for explicit confirmation, and sends
// one revocation. Device labels are untrusted and shown escaped.
func Revoke(ctx context.Context, admin Admin, deviceID string, confirmer Confirmer, output io.Writer) error {
	if !validUUID(deviceID) {
		return errors.New("device ID must be a canonical lowercase UUID")
	}
	devices, err := admin.Devices(ctx)
	if err != nil {
		return err
	}
	var device *protocol.AdminRPCDevice
	for index := range devices {
		if devices[index].ID == deviceID {
			device = &devices[index]
			break
		}
	}
	if device == nil {
		return fmt.Errorf("paired device was not found: %s", deviceID)
	}
	if device.Revoked {
		return writeString(output, fmt.Sprintf("Device %s (%s) is already revoked; no request was sent.\n",
			displayText(device.Label), device.ID))
	}
	prompt := fmt.Sprintf(
		"Revoke browser access for device %s (%s)? Its live connections close now; commands already sent cannot be undone. [y/N] ",
		displayText(device.Label), device.ID,
	)
	confirmed, err := confirmer.Confirm(ctx, prompt)
	if err != nil {
		return err
	}
	if !confirmed {
		return writeString(output, "Revocation cancelled.\n")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := admin.RevokeDevice(ctx, deviceID)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("revocation outcome is uncertain—run pterm devices and check device %s before trying again: %w", deviceID, err)
		}
		return err
	}
	if result.DeviceID != deviceID {
		return errors.New("broker revocation acknowledgement has the wrong device ID")
	}
	return writeString(output, fmt.Sprintf("Revoked %s; closed %d live connection(s).\n", deviceID, result.ClosedStreams))
}

// ProjectAdd sends an absolute path; the broker checks that it is an existing
// directory and stores its canonical form.
func ProjectAdd(ctx context.Context, admin Admin, path string, name *string, output io.Writer) error {
	if path == "" {
		return errors.New("project path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	project, err := admin.AddProject(ctx, absolute, name)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("outcome is uncertain—run pterm project list before adding it again: %w", err)
		}
		return err
	}
	return writeString(output, fmt.Sprintf("Saved project %s %s at %s\n",
		displayText(project.Name), project.ID, displayText(project.Path)))
}

func ProjectList(ctx context.Context, admin Admin, output io.Writer) error {
	projects, err := admin.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return writeString(output, "No saved projects.\n")
	}
	return writeTable(output, "PROJECT ID\tNAME\tPATH", len(projects), func(index int) string {
		project := projects[index]
		return strings.Join([]string{project.ID, displayText(project.Name), displayText(project.Path)}, "\t")
	})
}

func ProfileList(ctx context.Context, admin Admin, output io.Writer) error {
	profiles, err := admin.ShellProfiles(ctx)
	if err != nil {
		return err
	}
	return writeProfiles(output, profiles)
}

// ProfileDetect runs the broker's idempotent host shell discovery and prints
// the resulting saved profiles.
func ProfileDetect(ctx context.Context, admin Admin, output io.Writer) error {
	profiles, err := admin.DetectShellProfiles(ctx)
	if err != nil {
		return err
	}
	return writeProfiles(output, profiles)
}

// ConfigOrigin shows the configured pairing origin when value is nil and
// replaces it otherwise. The value is validated locally before it is sent.
func ConfigOrigin(ctx context.Context, admin Admin, value *string, output io.Writer) error {
	if value != nil {
		if err := protocol.ValidatePairingOrigin(*value); err != nil {
			return fmt.Errorf("invalid pairing origin: %w", err)
		}
	}
	origin, err := admin.PairingOrigin(ctx, value)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("outcome is uncertain—run pterm config origin to check the current value: %w", err)
		}
		return err
	}
	if origin == nil {
		if value != nil {
			return errors.New("broker did not acknowledge the new pairing origin")
		}
		return writeString(output, fmt.Sprintf("No pairing origin is configured. To set one, %s.\n", setOriginHint))
	}
	if err := protocol.ValidatePairingOrigin(*origin); err != nil {
		return fmt.Errorf("broker returned an invalid pairing origin: %w", err)
	}
	if value != nil {
		return writeString(output, fmt.Sprintf("Pairing origin set to %s\n", *origin))
	}
	return writeString(output, fmt.Sprintf("Pairing origin: %s\n", *origin))
}

func writeProfiles(output io.Writer, profiles []protocol.AdminRPCShellProfile) error {
	if len(profiles) == 0 {
		return writeString(output, "No shell profiles.\n")
	}
	return writeTable(output, "PROFILE ID\tNAME\tCOMMAND", len(profiles), func(index int) string {
		profile := profiles[index]
		command := []string{displayText(profile.Executable)}
		for _, argument := range profile.Arguments {
			command = append(command, displayText(argument))
		}
		return strings.Join([]string{profile.ID, displayText(profile.Name), strings.Join(command, " ")}, "\t")
	})
}

// writeTable assembles the whole table before one write. Free-text cells
// (labels, names, paths, arguments) must already be escaped; IDs, codes and
// times were shape-validated by the protocol decoder, as in pterm list.
func writeTable(output io.Writer, header string, rows int, row func(int) string) error {
	var table strings.Builder
	writer := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, header)
	for index := 0; index < rows; index++ {
		fmt.Fprintln(writer, row(index))
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return writeString(output, table.String())
}

func deviceStatus(device protocol.AdminRPCDevice) string {
	if device.Revoked {
		return "revoked"
	}
	return "active"
}
