package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
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
	// Found on your network (docs/found-on-network.md, N1).
	OfferPairing(ctx context.Context, via string) (protocol.AdminRPCPairOfferResultPayload, error)
	EndOffer(ctx context.Context, link *string) error
}

const (
	// OfferInterval is how often a waiting `pterm pair --via` offers this
	// computer to home again.
	OfferInterval = 30 * time.Second
	// OfferWait is how long `pterm pair --via` waits for a phone while it
	// offers this computer.
	OfferWait = 10 * time.Minute
)

// offerInterval and offerWait are OfferInterval and OfferWait; tests shorten them.
var offerInterval, offerWait = OfferInterval, OfferWait

const setOriginHint = "run `pterm config origin https://<your-private-host>`"

// PairOptions selects the link origin and whether Pair waits for the phone.
type PairOptions struct {
	// Origin overrides the configured origin (--origin).
	Origin string
	// Fallback is the gateway's own loopback origin, used when no origin is
	// configured. Such a link works only in a browser on this computer.
	Fallback string
	ShowQR   bool
	// QRColumns is the terminal width; a QR code wider than it is left out.
	// Zero means unknown.
	QRColumns int
	// Confirmer, when set, makes Pair wait until the grant expires for a
	// phone to open the link, show its code, and ask before approving. Nil
	// prints the manual approval steps instead (--no-wait). With Via (and
	// no AllowTagged or NoOffer) it also offers this computer to home, so
	// the app lists it under "Found on your network", and waits up to
	// OfferWait.
	Confirmer Confirmer
	// NoOffer keeps a Via wait to the link only: stdin is not a terminal
	// (for example a pipe), so no person is known to wait there.
	NoOffer bool
	// PollInterval is how often pending pairings are read; zero is 1s.
	PollInterval time.Duration
	// Via binds the grant to the Termivoa app served by another computer
	// (--via <name|origin>): Pair prints an add-computer link for that app
	// instead of a pairing link. AllowTagged accepts a tagged app node.
	Via         string
	AllowTagged bool
	// FirstComputerWarning says, before a first-computer pairing link, that
	// it is not the way to add a computer to an app. Only the standalone
	// `pterm pair` sets it; setup has already asked.
	FirstComputerWarning bool
}

// ErrNotPaired is returned when the owner did not approve the phone that
// opened the link; Pair has already said so and how to try again.
var ErrNotPaired = errors.New("the phone was not approved")

// PairCommand is the `pterm pair` command line that makes a new link of the
// same kind.
func PairCommand(options PairOptions) string {
	switch {
	case options.Via == "":
		return "pterm pair"
	case options.AllowTagged:
		return "pterm pair --via " + options.Via + " --allow-tagged"
	}
	return "pterm pair --via " + options.Via
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
		if code, err = renderQR(link, options.QRColumns); err != nil {
			code = "No QR code: " + err.Error() + ".\n"
		} else {
			instruction = "Scan the QR code or open this link"
		}
	}
	// Said before the code, so a second computer's owner does not scan it.
	if options.FirstComputerWarning {
		code = "This pairing is for a FIRST computer. To add this computer to a Termivoa app you already use, " +
			"run `pterm pair --via <home>` instead.\n" + code
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
	deadline, err := linkDeadline(grant.ExpiresAt, options)
	if err != nil {
		return err
	}
	return waitForPhone(ctx, admin, options, phoneWait{deadline: deadline, expired: linkExpired(options)}, seen, output)
}

// linkDeadline is when a wait for one link ends: the grant's expiry, plus
// two polls, because the broker may record a redemption made just before
// expiry a moment later.
func linkDeadline(expiresAt string, options PairOptions) (time.Time, error) {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return time.Time{}, errors.New("broker returned a malformed pairing expiry")
	}
	return expires.Add(2 * pollInterval(options)), nil
}

func linkExpired(options PairOptions) error {
	return noPhoneError(fmt.Sprintf("the link expired before a phone opened it; run `%s` again", PairCommand(options)))
}

// ErrNoPhone is the end of a wait that no phone came to: the link expired,
// or nobody added this computer in time. Setup reports it as "not connected
// yet", not as a failure.
var ErrNoPhone = errors.New("no phone came")

// noPhoneError is a wait's own text for ErrNoPhone.
type noPhoneError string

func (text noPhoneError) Error() string   { return string(text) }
func (noPhoneError) Is(target error) bool { return target == ErrNoPhone }

func pollInterval(options PairOptions) time.Duration {
	if options.PollInterval <= 0 {
		return time.Second
	}
	return options.PollInterval
}

// pairVia creates a grant bound to the app named by options.Via and prints it
// only inside an add-computer link that the app at that origin opens and,
// when ShowQR is set, a QR code of that link for the app's own scanner. The
// phone's camera app must not scan it: it opens links in the browser, whose
// cookies are not the installed app's, and the output says so. The link needs
// this computer's own https origin, never the loopback fallback, because the
// phone's app calls it directly.
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
	// No QR code is no loss: the link below is the main way (a terminal too
	// narrow for the code is usually a phone-sized Termivoa session, where
	// the owner taps the link).
	qrCode := ""
	if options.ShowQR {
		qrCode, _ = renderQR(link, options.QRColumns)
	}
	wait := phoneWait{appOrigin: appOrigin, self: machineName(origin)}
	if options.Confirmer != nil && !options.NoOffer {
		return offerAndWait(ctx, admin, options, grant, wait, link, qrCode, seen, output)
	}
	next := "Then run `pterm pair pending` and approve with `pterm pair approve <pending-id>` after comparing the code."
	if options.Confirmer != nil {
		next = "Waiting for the phone (Ctrl+C stops waiting)."
	}
	code := ""
	if qrCode != "" {
		// Said before the code, so the owner reads it before scanning.
		code = "Scan this QR code in the Termivoa app: Settings → + Add a computer → Scan QR.\n" +
			"The phone's Camera app will NOT work for this code: it opens the browser, not the app.\n" + qrCode
	}
	if err := writeString(output, fmt.Sprintf(
		"%sAdd-computer link (expires %s):\n%s\n"+
			"Open it within 2 minutes in the Termivoa app at %s: if this terminal is a Termivoa session "+
			"in that app, tap the link, then confirm \"Add this computer?\"; "+
			"or in Settings → + Add a computer, paste the link. %s\n"+
			"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n",
		code, grant.ExpiresAt, link, describeApp(appOrigin, grant.AppNode), next)); err != nil || options.Confirmer == nil {
		return err
	}
	if wait.deadline, err = linkDeadline(grant.ExpiresAt, options); err != nil {
		return err
	}
	wait.expired = linkExpired(options)
	return waitForPhone(ctx, admin, options, wait, seen, output)
}

// offerAndWait offers this computer to home (admin.pair.offer) at once and
// every offerInterval while it waits for a phone. The app at home then lists
// this computer under "Found on your network" with an Add button; the link
// and QR code stay as the fallback. Once home took an offer the wait lasts up
// to offerWait; until then it ends when the link expires, because the link
// is the only way left. Every exit ends the offer, which deletes the unused
// grants.
func offerAndWait(ctx context.Context, admin Admin, options PairOptions, grant protocol.AdminRPCPairGrantResultPayload,
	wait phoneWait, link, qrCode string, seen map[string]bool, output io.Writer,
) error {
	defer func() {
		// Ctrl+C has already cancelled ctx. A failed end leaves grants that
		// expire within 2 minutes.
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = admin.EndOffer(endCtx, &grant.Grant)
	}()
	home := machineName(wait.appOrigin)
	again := fmt.Errorf("Run %s again", PairCommand(options))
	fallback := " Use the QR code or the link below."
	tagged := "Add does not work with tagged computers: %s is tagged." + fallback
	delivered, offering := false, true
	// offer returns the line for the offer's result, or "" after a failure
	// that the next call may not have (it tries again). It stops on
	// home_changed, and when this computer's broker cannot take the request.
	offer := func(first bool) (string, error) {
		result, err := admin.OfferPairing(ctx, options.Via)
		var remote *localclient.RemoteError
		switch {
		case errors.As(err, &remote) && remote.Code == protocol.HomeChanged:
			return "", fmt.Errorf("%s is now a different Tailscale machine. %w.", home, again)
		case errors.As(err, &remote) && remote.Code == protocol.HostUnavailable:
			return "This computer cannot read its Tailscale status." + fallback, nil
		case errors.As(err, &remote) && remote.Code == protocol.InvalidRequest && options.AllowTagged:
			// The link grant passed with --allow-tagged and home is not
			// tagged (checked before), so this computer is.
			offering = false
			return fmt.Sprintf(tagged, "this computer"), nil
		case err != nil && ctx.Err() != nil:
			return "", ctx.Err()
		case err != nil && remote == nil && first:
			// The broker closed the connection: it is older than this pterm.
			return "", errors.New("this computer's Termivoa is older than this pterm. " +
				"Restart Termivoa on this computer (pterm shutdown, then pterm launch), then try again")
		case err != nil && first:
			return "This computer could not offer itself to " + home + "." + fallback, nil
		case err != nil:
			return "", nil
		case result.AppOrigin != wait.appOrigin:
			return "", fmt.Errorf("broker offered this computer to %s, not %s", originText(result.AppOrigin), wait.appOrigin)
		case result.Delivered:
			delivered = true
			return fmt.Sprintf("Sent to %s (%s).", home, wait.appOrigin), nil
		case result.Problem != nil && *result.Problem == protocol.Busy:
			return fmt.Sprintf("%s is busy right now.%s", home, fallback), nil
		case result.Problem != nil && *result.Problem == protocol.NotAccepted:
			return fmt.Sprintf("Termivoa on %s cannot receive this yet. Update it, or use the QR code or the link below.", home), nil
		}
		return home + " did not answer." + fallback, nil
	}
	var shown string
	switch {
	case grant.Origin == nil:
		// Home needs this computer's own address in the offer.
		offering = false
		shown = "Add needs this computer's address: run pterm setup, or pterm config origin https://<this computer>." + fallback
	case grant.AppNode != nil && len(grant.AppNode.Tags) > 0:
		// Home drops every offer from or to a tagged computer.
		offering = false
		shown = fmt.Sprintf(tagged, home)
	default:
		var err error
		if shown, err = offer(true); err != nil {
			return err
		}
	}
	linkEnds, err := linkDeadline(grant.ExpiresAt, options)
	if err != nil {
		return err
	}
	// The QR code and the link: shown at once when the push failed, else
	// only when the owner presses Enter (or a later push fails).
	linkShown := false
	showLink := func(lead string) error {
		if linkShown {
			return nil
		}
		linkShown = true
		if time.Now().After(linkEnds) {
			return writeString(output, fmt.Sprintf("The QR code and link expired (they work for 2 minutes). "+
				"Add in the app still works; for a new link, press Ctrl+C and run %s again.\n", PairCommand(options)))
		}
		text := lead + "No Add button? In the app tap + Add a computer → Scan QR, or paste the link. They work for 2 minutes:\n" +
			qrCode + link + "\n"
		if qrCode != "" {
			text += "The phone's Camera app will NOT work for this code: it opens the browser, not the app.\n"
		}
		return writeString(output, text+"The link and QR code are a single-use secret: do not share them in chats, issues, screenshots or logs.\n")
	}
	// Enter is read only on a terminal (a confirmer that can Ask), and only
	// until a phone asks: the reader stops before the approval prompt, and it
	// reads no farther than a newline. It keeps reading, and drops, every line
	// after the first, so a second Enter (or typed-ahead y) never answers the
	// y/N prompt.
	asker, canAsk := options.Confirmer.(interface {
		Ask(context.Context, string) (string, error)
	})
	line, waiting := shown, "Waiting for your phone (Ctrl+C stops)…"
	if delivered {
		line += fmt.Sprintf("\nOn your phone, open Termivoa → Settings → Computers and tap Add next to %q.", wait.self)
		waiting = fmt.Sprintf("Waiting for your phone (up to %d minutes; Ctrl+C stops)…", int(OfferWait/time.Minute))
	}
	if err := writeString(output, fmt.Sprintf("Adding this computer (%s) to your Termivoa app on %s.\n%s\n%s\n", wait.self, home, line, waiting)); err != nil {
		return err
	}
	if delivered && canAsk {
		if err := writeString(output, "No Add button? Press Enter to show a QR code and a link.\n"); err != nil {
			return err
		}
		enterCtx, stopEnter := context.WithCancel(ctx)
		pressed, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			var once sync.Once
			for enterCtx.Err() == nil {
				if _, err := asker.Ask(enterCtx, ""); err == nil {
					once.Do(func() { close(pressed) })
				}
				// A closed terminal reads as an empty line at once: do not spin.
				select {
				case <-enterCtx.Done():
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
		stop := func() {
			stopEnter()
			<-stopped
		}
		defer stop()
		wait.enter, wait.beforePrompt = pressed, stop
		wait.entered = func() error { return showLink("") }
	} else if err := showLink("\n"); err != nil {
		return err
	}
	wait.deadline = time.Now().Add(offerWait)
	wait.expired = noPhoneError(fmt.Sprintf("Nobody added this computer in %d minutes. %s.", int(OfferWait/time.Minute), again))
	wait.linkEnds = linkEnds
	wait.linkEnded = func() error {
		switch {
		case !delivered:
			return linkExpired(options)
		case linkShown:
			return writeString(output, "The QR code and link above expired. Add in the app still works.\n")
		}
		return nil
	}
	if offering {
		wait.reoffer = func() error {
			next, err := offer(false)
			if err != nil || next == "" || next == shown {
				return err
			}
			shown = next
			if strings.HasPrefix(next, "Sent to ") {
				next += fmt.Sprintf(" Tap Add next to %q in the app.", wait.self)
			}
			if err := writeString(output, next+"\n"); err != nil || strings.HasPrefix(next, "Sent to ") {
				return err
			}
			// The failure line points to the QR code and the link "below".
			return showLink("")
		}
	}
	return waitForPhone(ctx, admin, options, wait, seen, output)
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
	return fmt.Sprintf("%s (%s, %s)", machineName(origin), origin, appOwner(node))
}

// appOwner is a Termivoa app computer's Tailscale owner or, for a tagged
// node, its tags.
func appOwner(node *protocol.TailnetNode) string {
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
	return who
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
// another computer's app names THIS computer (self) and quotes what the
// phone's code screen says: in a relay through a wrong home, the phone names
// another computer (docs/found-on-network.md, R1). It also names that app,
// from the pending item itself, with its full origin and owner, so a
// look-alike home (another tailnet or another owner) shows.
func pairingPrompt(item protocol.AdminRPCPendingPairing, self string) string {
	if item.AppOrigin != nil {
		origin := *item.AppOrigin
		home := displayText(origin)
		if protocol.ValidateComputerOrigin(origin) == nil {
			home = machineName(origin)
		}
		return fmt.Sprintf("Phone %s wants to add this computer (%s) to your Termivoa app on %s.\n%s is %s (%s).\n"+
			"Approve only if your phone shows %s and says \"type y in the terminal on %s\". [y/N] ",
			displayText(item.DeviceLabel), self, home, home, originText(origin), appOwner(item.AppNode), item.VerificationCode, self)
	}
	return fmt.Sprintf("Approve only if the phone shows exactly this code.\nPhone %s shows code %s. Approve? [y/N] ",
		displayText(item.DeviceLabel), item.VerificationCode)
}

// phoneWait is what waitForPhone waits for, and how long.
type phoneWait struct {
	deadline time.Time
	expired  error // returned at the deadline
	// appOrigin, when set, offers only pairings bound to that app; self is
	// this computer's machine name for the prompt.
	appOrigin, self string
	// reoffer, when set, runs every offerInterval; its error ends the wait.
	reoffer func() error
	// linkEnded, when set, runs once after linkEnds (the link expired); its
	// error ends the wait.
	linkEnds  time.Time
	linkEnded func() error
	// enter, when set, is closed when the owner presses Enter; entered then
	// runs once and its error ends the wait. beforePrompt, when set, runs
	// before the approval prompt reads the terminal.
	enter        <-chan struct{}
	entered      func() error
	beforePrompt func()
}

// waitForPhone approves at most one new pending pairing, and only after the
// owner confirms that the phone shows the same code. It never auto-approves.
// The prompt blocks the loop, so no offer is sent while it waits.
func waitForPhone(ctx context.Context, admin Admin, options PairOptions, wait phoneWait,
	seen map[string]bool, output io.Writer,
) error {
	interval := pollInterval(options)
	offered := time.Now()
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
			// A plain pairing (no appOrigin) offers only plain pairings: a
			// redeemed bound grant (a link or Found offer of another wait)
			// needs the prompt that names this computer and its app.
			if wait.appOrigin == "" && item.AppOrigin != nil ||
				wait.appOrigin != "" && (item.AppOrigin == nil || *item.AppOrigin != wait.appOrigin) {
				continue
			}
			if wait.beforePrompt != nil {
				wait.beforePrompt()
			}
			approve, err := options.Confirmer.Confirm(ctx, pairingPrompt(item, wait.self))
			if err != nil {
				return err
			}
			if !approve {
				return errors.Join(ErrNotPaired, writeString(output,
					"Not approved; that phone stays unpaired. Run `"+PairCommand(options)+"` to try again.\n"))
			}
			if wait.appOrigin == "" {
				return PairApprove(ctx, admin, item.ID, output)
			}
			if err := approvePending(ctx, admin, item.ID); err != nil {
				return err
			}
			text := fmt.Sprintf("Done. %s is in your Termivoa app on %s. Open the app and tap it.\n", wait.self, machineName(wait.appOrigin))
			if len(wait.self) > 12 {
				text += "Tip: give it a short name in the app: Settings, tap the computer, then Rename.\n"
			}
			return writeString(output, text)
		}
		if wait.linkEnded != nil && time.Now().After(wait.linkEnds) {
			ended := wait.linkEnded
			wait.linkEnded = nil
			if err := ended(); err != nil {
				return err
			}
		}
		if time.Now().After(wait.deadline) {
			return wait.expired
		}
		if wait.reoffer != nil && time.Since(offered) >= offerInterval {
			offered = time.Now()
			if err := wait.reoffer(); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait.enter:
			wait.enter = nil
			if err := wait.entered(); err != nil {
				return err
			}
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
	if err := approvePending(ctx, admin, pendingID); err != nil {
		return err
	}
	return writeString(output, fmt.Sprintf("Approved %s. The phone finishes pairing on its next poll.\n", pendingID))
}

func approvePending(ctx context.Context, admin Admin, pendingID string) error {
	if !protocol.ValidUUID(pendingID) {
		return errors.New("pending ID must be a canonical lowercase UUID")
	}
	if err := admin.ApprovePairing(ctx, pendingID); err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("approval outcome is uncertain—run pterm pair pending and pterm devices before approving again: %w", err)
		}
		return err
	}
	return nil
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
