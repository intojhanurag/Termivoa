package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

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
	RenameDevice(context.Context, string, string) error
	AddProject(context.Context, string, *string) (protocol.AdminRPCProject, error)
	Projects(context.Context) ([]protocol.AdminRPCProject, error)
	ShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error)
	DetectShellProfiles(context.Context) ([]protocol.AdminRPCShellProfile, error)
	PairingOrigin(context.Context, *string) (*string, error)
	// Multi-computer administration (docs/multi-computer.md).
	CreateBoundPairingGrant(context.Context, protocol.AdminRPCPairGrantPayload) (protocol.AdminRPCPairGrantResultPayload, error)
	RevokeDevicesVia(ctx context.Context, appOrigin string) (protocol.AdminRPCRevokeViaResultPayload, error)
	Computers(context.Context) ([]protocol.AdminRPCComputer, error)
	RemoveComputer(ctx context.Context, origin string) (protocol.AdminRPCComputerRemoveResultPayload, error)
}

const setOriginHint = "run `pterm config origin https://<your-private-host>`"

// PairOptions selects the link origin and whether Pair waits for the phone.
type PairOptions struct {
	// Origin overrides the configured origin (--origin).
	Origin string
	// Fallback is the gateway's own loopback origin, used when no origin is
	// configured. Such a link works only in a browser on this computer.
	Fallback string
	ShowQR   bool
	// Confirmer, when set, makes Pair wait until the grant expires for a
	// phone to open the link, show its code, and ask before approving. Nil
	// prints the manual approval steps instead (--no-wait).
	Confirmer Confirmer
	// PollInterval is how often pending pairings are read; zero is 1s.
	PollInterval time.Duration
	// Via binds the grant to the Termivoa app served by another computer
	// (--via <name|origin>): Pair prints an add-computer link for that app
	// instead of a pairing link. AllowTagged accepts a tagged app node.
	Via         string
	AllowTagged bool
}

// Pair creates one grant and prints it only inside the pairing link and,
// when ShowQR is set, a terminal QR code of that same link. The grant is the
// URL fragment by design, so it never reaches the server's request line; it
// must not appear anywhere else in output or errors.
func Pair(ctx context.Context, admin Admin, options PairOptions, output io.Writer) error {
	if options.Origin != "" {
		if err := protocol.ValidatePairingOrigin(options.Origin); err != nil {
			return fmt.Errorf("invalid --origin: %w", err)
		}
	}
	if options.AllowTagged && options.Via == "" {
		return errors.New("--allow-tagged needs --via")
	}
	if options.Via != "" {
		if err := protocol.ValidatePairVia(options.Via); err != nil {
			return fmt.Errorf("invalid --via: %w", err)
		}
		if options.Origin != "" && protocol.ValidateComputerOrigin(options.Origin) != nil {
			return errors.New("the add-computer link needs this computer's tailnet https address as --origin")
		}
	}
	// Pending pairings that exist before this grant belong to someone else.
	seen := map[string]bool{}
	if options.Confirmer != nil {
		pending, err := admin.PendingPairings(ctx)
		if err != nil {
			return err
		}
		for _, item := range pending {
			seen[item.ID] = true
		}
	}
	if options.Via != "" {
		return pairVia(ctx, admin, options, seen, output)
	}
	grant, err := admin.CreatePairingGrant(ctx)
	if err != nil {
		return err
	}
	origin, localOnly := options.Origin, false
	if origin == "" && grant.Origin != nil {
		origin = *grant.Origin
	}
	if origin == "" && options.Fallback != "" {
		origin, localOnly = options.Fallback, true
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
	if options.ShowQR {
		if code, err = renderQR(link); err != nil {
			code = "No QR code: " + err.Error() + ".\n"
		} else {
			instruction = "Scan the QR code or open this link"
		}
	}
	where, next := "on the phone", "Then run `pterm pair pending` and approve with "+
		"`pterm pair approve <pending-id>` after comparing the code."
	if localOnly {
		where = "in a browser on this computer"
		next = "No phone address is configured, so this link works only on this computer; " +
			"for your phone, run `pterm setup`. " + next
	}
	if options.Confirmer != nil {
		next = "Waiting for it to open the link (Ctrl+C stops waiting)."
		if localOnly {
			next = "No phone address is configured, so this link works only on this computer; " +
				"for your phone, run `pterm setup`. " + next
		}
	}
	if err := writeString(output, fmt.Sprintf(
		"%sPairing link (expires %s):\n%s\n"+
			"%s %s within 2 minutes. %s\n"+
			"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n",
		code, grant.ExpiresAt, link, instruction, where, next)); err != nil || options.Confirmer == nil {
		return err
	}
	return waitForPhone(ctx, admin, options, grant.ExpiresAt, "", seen, output)
}

// pairVia creates a grant bound to the app named by options.Via and prints it
// only inside an add-computer link that the app at that origin opens. There
// is no QR code: a phone camera opens links in the browser, whose cookies are
// not the installed app's. The link needs this computer's own https origin,
// never the loopback fallback, because the phone's app calls it directly.
func pairVia(ctx context.Context, admin Admin, options PairOptions, seen map[string]bool, output io.Writer) error {
	request := protocol.AdminRPCPairGrantPayload{Via: &options.Via}
	if options.AllowTagged {
		allow := true
		request.AllowTagged = &allow
	}
	grant, err := admin.CreateBoundPairingGrant(ctx, request)
	if err != nil {
		return err
	}
	appOrigin, err := boundAppOrigin(options.Via, grant)
	if err != nil {
		return err
	}
	origin := options.Origin
	if origin == "" && grant.Origin != nil {
		origin = *grant.Origin
	}
	if protocol.ValidateComputerOrigin(origin) != nil {
		return errors.New("the add-computer link needs this computer's tailnet https address; run `pterm setup` or pass --origin")
	}
	if protocol.ValidateSecretText(grant.Grant) != nil {
		return errors.New("broker returned a malformed pairing grant")
	}
	link := origin + "/add-computer#grant=" + grant.Grant + "&via=" + url.QueryEscape(appOrigin)
	next := "Then run `pterm pair pending` and approve with `pterm pair approve <pending-id>` after comparing the code."
	if options.Confirmer != nil {
		next = "Waiting for the phone (Ctrl+C stops waiting)."
	}
	if err := writeString(output, fmt.Sprintf(
		"Add-computer link (expires %s):\n%s\n"+
			"Open it within 2 minutes in the Termivoa app at %s: run this command inside a Termivoa session "+
			"in that app and tap the link, then confirm \"Add this computer?\"; "+
			"or in Settings → Computers tap \"+ Add a computer\" and paste the link. %s\n"+
			"The link is a single-use secret: do not share it in chats, issues, screenshots or logs.\n",
		grant.ExpiresAt, link, describeApp(appOrigin, grant.AppNode), next)); err != nil || options.Confirmer == nil {
		return err
	}
	return waitForPhone(ctx, admin, options, grant.ExpiresAt, appOrigin, seen, output)
}

// boundAppOrigin checks that the broker bound the grant to the app the owner
// named: exactly that origin, or the node whose first DNS label is the short
// name. Anything else is refused before the grant is shown.
func boundAppOrigin(via string, grant protocol.AdminRPCPairGrantResultPayload) (string, error) {
	if grant.AppOrigin == nil || grant.AppNode == nil || protocol.ValidateComputerOrigin(*grant.AppOrigin) != nil {
		return "", errors.New("broker did not bind the grant to a valid app origin")
	}
	origin, want := *grant.AppOrigin, machineName(*grant.AppOrigin)
	if strings.HasPrefix(via, "https://") {
		want = origin
	}
	if want != via {
		return "", fmt.Errorf("broker bound the grant to %s, not %s", origin, via)
	}
	return origin, nil
}

// machineName is the first DNS label of a computer origin's host.
func machineName(origin string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(origin, "https://"), ".")
	return name
}

// describeApp names the computer that serves a Termivoa app: machine name,
// full origin, and its Tailscale owner or, for a tagged node, its tags.
func describeApp(origin string, node *protocol.TailnetNode) string {
	if protocol.ValidateComputerOrigin(origin) != nil {
		return displayText(origin)
	}
	who := "owner unknown"
	switch {
	case node != nil && len(node.Tags) > 0:
		tags := make([]string, len(node.Tags))
		for index, tag := range node.Tags {
			tags[index] = plainText(tag)
		}
		who = "tags " + strings.Join(tags, ", ")
	case node != nil && node.Owner != nil:
		who = "owner " + plainText(*node.Owner)
	}
	return fmt.Sprintf("%s (%s, %s)", machineName(origin), origin, who)
}

// plainText prints a nonempty printable-ASCII word as is and escapes
// anything else, as displayText does.
func plainText(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] < '!' || value[index] > '~' {
			return displayText(value)
		}
	}
	if value == "" {
		return displayText(value)
	}
	return value
}

// pairingPrompt asks the owner to compare the phone's code. A pairing bound to
// another computer's app also names that app, from the pending item itself.
func pairingPrompt(item protocol.AdminRPCPendingPairing) string {
	app := ""
	if item.AppOrigin != nil {
		app = " and will use this computer from the Termivoa app at " + describeApp(*item.AppOrigin, item.AppNode)
	}
	return fmt.Sprintf("Approve only if the phone shows exactly this code.\nPhone %s shows code %s%s. Approve? [y/N] ",
		displayText(item.DeviceLabel), item.VerificationCode, app)
}

// waitForPhone approves at most one new pending pairing, and only after the
// owner confirms that the phone shows the same code. It never auto-approves.
// With appOrigin set, only pairings bound to that app are offered.
func waitForPhone(ctx context.Context, admin Admin, options PairOptions, expiresAt, appOrigin string,
	seen map[string]bool, output io.Writer,
) error {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return errors.New("broker returned a malformed pairing expiry")
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	// The broker may record a redemption made just before expiry a moment later.
	deadline := expires.Add(2 * interval)
	for {
		pending, err := admin.PendingPairings(ctx)
		if err != nil {
			return err
		}
		for _, item := range pending {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			if appOrigin != "" && (item.AppOrigin == nil || *item.AppOrigin != appOrigin) {
				continue
			}
			approve, err := options.Confirmer.Confirm(ctx, pairingPrompt(item))
			if err != nil {
				return err
			}
			if !approve {
				return writeString(output, "Not approved; that phone stays unpaired. Run `pterm pair` to try again.\n")
			}
			return PairApprove(ctx, admin, item.ID, output)
		}
		if time.Now().After(deadline) {
			return errors.New("the pairing link expired before a phone opened it; run `pterm pair` again")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func PairPending(ctx context.Context, admin Admin, output io.Writer) error {
	pending, err := admin.PendingPairings(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return writeString(output, "No pending pairings.\n")
	}
	return writeTable(output, "PENDING ID\tCODE\tLABEL\tEXPIRES\tVIA", len(pending), func(index int) string {
		item := pending[index]
		return strings.Join([]string{item.ID, item.VerificationCode,
			displayText(item.DeviceLabel), item.ExpiresAt, viaText(item.AppOrigin)}, "\t")
	})
}

// PairApprove approves one pending pairing. It is never retried: a lost
// acknowledgement leaves the outcome for the owner to inspect.
func PairApprove(ctx context.Context, admin Admin, pendingID string, output io.Writer) error {
	if !protocol.ValidUUID(pendingID) {
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
	return writeTable(output, "DEVICE ID\tLABEL\tSTATUS\tLAST USED\tVIA", len(devices), func(index int) string {
		device := devices[index]
		return strings.Join([]string{device.ID, displayText(device.Label),
			deviceStatus(device), device.LastUsedAt, viaText(device.AppOrigin)}, "\t")
	})
}

// viaText is the app origin a phone uses this computer from, or "-" when it
// uses this computer's own page.
func viaText(appOrigin *string) string {
	if appOrigin == nil {
		return "-"
	}
	return originText(*appOrigin)
}

// originText prints a canonical computer origin as is and escapes anything
// else; origins were validated by the protocol decoder.
func originText(origin string) string {
	if protocol.ValidateComputerOrigin(origin) != nil {
		return displayText(origin)
	}
	return origin
}

// RenameDevice sets a device's display label. Surrounding spaces are removed.
func RenameDevice(ctx context.Context, admin Admin, deviceID, label string, output io.Writer) error {
	if !protocol.ValidUUID(deviceID) {
		return errors.New("device ID must be a canonical lowercase UUID")
	}
	label = strings.TrimSpace(label)
	if protocol.ValidateDeviceLabel(label) != nil {
		return errors.New("device label must be 1 to 64 bytes of plain text")
	}
	if err := admin.RenameDevice(ctx, deviceID, label); err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("rename outcome is uncertain—run pterm devices to check: %w", err)
		}
		return err
	}
	return writeString(output, fmt.Sprintf("Renamed device %s to %s\n", deviceID, displayText(label)))
}

// Revoke resolves the exact device, asks for explicit confirmation, and sends
// one revocation. Device labels are untrusted and shown escaped.
func Revoke(ctx context.Context, admin Admin, deviceID string, confirmer Confirmer, output io.Writer) error {
	if !protocol.ValidUUID(deviceID) {
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
	// On home, the phone's other computers are read first: revocation may
	// delete its computer list.
	others := phoneComputers(ctx, admin, deviceID)
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
	text := fmt.Sprintf("Revoked %s; closed %d live connection(s).\n", deviceID, result.ClosedStreams)
	if len(others) > 0 {
		text += "This phone also used these computers. Run `pterm revoke` on each:\n"
		for _, computer := range others {
			text += fmt.Sprintf("  %s (%s)\n", displayText(computer.Label), originText(computer.Origin))
		}
	}
	return writeString(output, text)
}

// phoneComputers lists the other computers one phone uses through this home
// computer. It is a hint only: a failed read gives none.
func phoneComputers(ctx context.Context, admin Admin, deviceID string) []protocol.AdminRPCComputer {
	all, err := admin.Computers(ctx)
	if err != nil {
		return nil
	}
	var mine []protocol.AdminRPCComputer
	for _, computer := range all {
		if computer.DeviceID == deviceID {
			mine = append(mine, computer)
		}
	}
	return mine
}

// RevokeVia revokes every device that uses this computer from the Termivoa
// app at appOrigin, and that app's pairing links and pending pairings, for
// example after that home computer was compromised.
func RevokeVia(ctx context.Context, admin Admin, appOrigin string, confirmer Confirmer, output io.Writer) error {
	if err := protocol.ValidateComputerOrigin(appOrigin); err != nil {
		return fmt.Errorf("invalid --via: %w", err)
	}
	devices, err := admin.Devices(ctx)
	if err != nil {
		return err
	}
	count := 0
	for _, device := range devices {
		if !device.Revoked && device.AppOrigin != nil && *device.AppOrigin == appOrigin {
			count++
		}
	}
	// The request also removes the app's pairing links and pending pairings,
	// so it is sent even when no device uses the app now.
	prompt := fmt.Sprintf(
		"Revoke browser access for %d device(s) that use this computer from the Termivoa app at %s? Their live connections close now. [y/N] ",
		count, appOrigin)
	if count == 0 {
		prompt = fmt.Sprintf("No device uses this computer from the Termivoa app at %s now, but its pending pairings "+
			"and pairing links will be removed. Continue? [y/N] ", appOrigin)
	}
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
	result, err := admin.RevokeDevicesVia(ctx, appOrigin)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("revocation outcome is uncertain—run pterm devices and check the devices bound to %s before trying again: %w", appOrigin, err)
		}
		return err
	}
	if result.AppOrigin != appOrigin {
		return errors.New("broker revocation acknowledgement has the wrong app origin")
	}
	return writeString(output, fmt.Sprintf("Revoked %d device(s) bound to %s; closed %d live connection(s).\n",
		len(result.DeviceIDS), appOrigin, result.ClosedStreams))
}

// Computers lists, on the home computer, the other computers each phone uses.
func Computers(ctx context.Context, admin Admin, output io.Writer) error {
	list, err := admin.Computers(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return writeString(output, "No computers.\n")
	}
	return writeTable(output, "DEVICE ID\tLABEL\tORIGIN\tADDED", len(list), func(index int) string {
		computer := list[index]
		return strings.Join([]string{computer.DeviceID, displayText(computer.Label),
			originText(computer.Origin), computer.CreatedAt}, "\t")
	})
}

// RemoveComputer drops a computer from every phone's list on this home
// computer. It cannot sign a phone out on that computer, so it says how.
func RemoveComputer(ctx context.Context, admin Admin, origin string, output io.Writer) error {
	if err := protocol.ValidateComputerOrigin(origin); err != nil {
		return fmt.Errorf("invalid computer origin: %w", err)
	}
	result, err := admin.RemoveComputer(ctx, origin)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("outcome is uncertain—run pterm computers to check: %w", err)
		}
		return err
	}
	if result.Origin != origin {
		return errors.New("broker acknowledgement has the wrong computer origin")
	}
	return writeString(output, fmt.Sprintf(
		"Removed %s from %d device list(s). This does not sign the phone out there: run `pterm revoke` on %s (%s).\n",
		origin, result.Removed, machineName(origin), origin))
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
	return string(device.Status)
}
