// Package cli orchestrates user-facing local commands without directly owning
// broker state, SQLite, PTYs, or process lifecycle.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	listPageSize = int64(protocol.MaxBrokerRPCListSessions)
	// The broker currently retains at most 4,096 session records. Keeping the
	// client cap independent and larger prevents a corrupt endpoint from
	// producing an unbounded cursor chain.
	maxListPages = 32
)

var ErrSessionNotFound = errors.New("managed session was not found")

// Management is the bounded broker-management surface used by local commands.
// Its concrete implementation authenticates over restricted local IPC.
type Management interface {
	ListPage(context.Context, *string, int64) (protocol.BrokerRPCSessionListResultPayload, error)
	Status(context.Context, localclient.SessionRef) (protocol.BrokerRPCSessionRecord, error)
	Create(context.Context, string, string, string) (protocol.BrokerRPCSessionMutationResultPayload, error)
	Terminate(context.Context, localclient.SessionRef, string) (protocol.BrokerRPCSessionMutationResultPayload, error)
}

// Confirmer obtains the explicit destructive-action confirmation required by
// pterm terminate. The prompt has already been escaped for terminal display.
type Confirmer interface {
	Confirm(context.Context, string) (bool, error)
}

// IDSource returns one fresh operation ID. It is deliberately separate from
// the per-RPC correlation IDs owned by localclient.
type IDSource func() string

// NewOperationID returns a random UUID for durable mutation deduplication. It
// must be called once per user-confirmed operation.
func NewOperationID() string { return uuid.New().String() }

type App struct {
	management Management
	confirmer  Confirmer
	newID      IDSource
	output     io.Writer
}

func NewApp(management Management, confirmer Confirmer, newID IDSource, output io.Writer) (*App, error) {
	if management == nil {
		return nil, errors.New("CLI management client must not be nil")
	}
	if confirmer == nil {
		return nil, errors.New("CLI confirmer must not be nil")
	}
	if newID == nil {
		return nil, errors.New("CLI operation ID source must not be nil")
	}
	if output == nil {
		return nil, errors.New("CLI output must not be nil")
	}
	return &App{management: management, confirmer: confirmer, newID: newID, output: output}, nil
}

// List prints one bounded, stable view assembled before any user-visible
// output. All display text is escaped so a legacy stored title cannot issue
// terminal control sequences.
func (app *App) List(ctx context.Context) error {
	sessions, err := app.listAll(ctx)
	if err != nil {
		return err
	}
	var output strings.Builder
	if len(sessions) == 0 {
		output.WriteString("No managed sessions.\n")
		return writeString(app.output, output.String())
	}
	output.WriteString("SESSION ID                            STATE     TITLE  PROJECT ID                            SHELL PROFILE ID\n")
	for _, session := range sessions {
		fmt.Fprintf(&output, "%s  %-8s  %s  %s  %s\n",
			session.ID,
			session.State,
			displayText(session.Title),
			session.ProjectID,
			session.ShellProfileID,
		)
	}
	return writeString(app.output, output.String())
}

// ResolveSession finds exactly one generation for a complete session ID and
// refreshes its state. Attach and destructive actions use the same identity
// checks; a restarted or vanished session is never silently replaced.
func (app *App) ResolveSession(ctx context.Context, sessionID string) (protocol.BrokerRPCSessionRecord, error) {
	if !protocol.ValidUUID(sessionID) {
		return protocol.BrokerRPCSessionRecord{}, errors.New("session ID must be a canonical lowercase UUID")
	}
	sessions, err := app.listAll(ctx)
	if err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	var selected *protocol.BrokerRPCSessionRecord
	for index := range sessions {
		if sessions[index].ID != sessionID {
			continue
		}
		if selected != nil {
			return protocol.BrokerRPCSessionRecord{}, errors.New("broker returned more than one generation for the requested session ID")
		}
		candidate := sessions[index]
		selected = &candidate
	}
	if selected == nil {
		return protocol.BrokerRPCSessionRecord{}, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	ref := localclient.SessionRef{ID: selected.ID, Generation: selected.Generation}
	current, err := app.management.Status(ctx, ref)
	if err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	if current.ID != ref.ID || current.Generation != ref.Generation {
		return protocol.BrokerRPCSessionRecord{}, errors.New("broker status response changed session identity")
	}
	return current, nil
}

// Create sends one deduplicated creation operation using saved project/shell
// IDs only. The broker resolves paths and executables. No confirmation or
// implicit retry is performed, and attachment is deliberately a separate step:
// an attachment failure does not destroy the successfully created session.
func (app *App) Create(ctx context.Context, projectID, shellProfileID string) (protocol.BrokerRPCSessionRecord, error) {
	if ctx == nil {
		return protocol.BrokerRPCSessionRecord{}, errors.New("CLI context must not be nil")
	}
	if !protocol.ValidUUID(projectID) || !protocol.ValidUUID(shellProfileID) {
		return protocol.BrokerRPCSessionRecord{}, errors.New("project and shell profile IDs must be canonical lowercase UUIDs")
	}
	if err := ctx.Err(); err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	operationID := app.newID()
	if !protocol.ValidUUID(operationID) {
		return protocol.BrokerRPCSessionRecord{}, errors.New("generated creation operation ID must be a canonical lowercase UUID")
	}
	if err := ctx.Err(); err != nil {
		return protocol.BrokerRPCSessionRecord{}, err
	}
	result, err := app.management.Create(ctx, operationID, projectID, shellProfileID)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return protocol.BrokerRPCSessionRecord{}, fmt.Errorf("delivery uncertain—run pterm list and inspect the sessions before creating another: %w", err)
		}
		return protocol.BrokerRPCSessionRecord{}, err
	}
	if result.OperationID != operationID || result.Session.ProjectID != projectID || result.Session.ShellProfileID != shellProfileID ||
		!protocol.ValidUUID(result.Session.ID) || !protocol.ValidUUID(result.Session.Generation) {
		return protocol.BrokerRPCSessionRecord{}, errors.New("delivery uncertain—creation acknowledgement did not match the requested configuration or session identity; run pterm list before creating another")
	}
	return result.Session, nil
}

// Terminate resolves the exact full session ID, refreshes that generation's
// state, asks for explicit confirmation, and sends exactly one operation ID.
// It never retries an ambiguously delivered mutation.
func (app *App) Terminate(ctx context.Context, sessionID string) error {
	current, err := app.ResolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	ref := localclient.SessionRef{ID: current.ID, Generation: current.Generation}
	if done, message := terminalStateMessage(current); done {
		return writeString(app.output, message)
	}

	prompt := fmt.Sprintf(
		"Terminate session %s (%s)? This ends its managed process tree. [y/N] ",
		displayText(current.Title), current.ID,
	)
	confirmed, err := app.confirmer.Confirm(ctx, prompt)
	if err != nil {
		return err
	}
	if !confirmed {
		return writeString(app.output, "Termination cancelled.\n")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	operationID := app.newID()
	result, err := app.management.Terminate(ctx, ref, operationID)
	if err != nil {
		if localclient.IsDeliveryUncertain(err) {
			return fmt.Errorf("delivery uncertain—run pterm list and inspect the session before trying again: %w", err)
		}
		return err
	}
	if result.OperationID != operationID {
		return errors.New("broker termination acknowledgement has the wrong operation ID")
	}
	if result.Session.ID != ref.ID || result.Session.Generation != ref.Generation {
		return errors.New("broker termination acknowledgement changed session identity")
	}
	return writeString(app.output, terminationAcknowledgement(result.Session))
}

func (app *App) listAll(ctx context.Context) ([]protocol.BrokerRPCSessionRecord, error) {
	if ctx == nil {
		return nil, errors.New("CLI context must not be nil")
	}
	var cursor *string
	sessions := make([]protocol.BrokerRPCSessionRecord, 0)
	seenCursors := make(map[string]struct{})
	seenSessions := make(map[string]struct{})
	for pageNumber := 0; pageNumber < maxListPages; pageNumber++ {
		page, err := app.management.ListPage(ctx, cursor, listPageSize)
		if err != nil {
			return nil, err
		}
		for _, session := range page.Sessions {
			key := session.ID + "\x00" + session.Generation
			if _, exists := seenSessions[key]; exists {
				return nil, errors.New("broker session pagination returned a duplicate identity")
			}
			seenSessions[key] = struct{}{}
			sessions = append(sessions, session)
		}
		if page.NextCursor == nil {
			return sessions, nil
		}
		if _, exists := seenCursors[*page.NextCursor]; exists {
			return nil, errors.New("broker session pagination repeated a cursor")
		}
		seenCursors[*page.NextCursor] = struct{}{}
		value := *page.NextCursor
		cursor = &value
	}
	return nil, fmt.Errorf("broker session pagination exceeded %d pages", maxListPages)
}

func terminalStateMessage(session protocol.BrokerRPCSessionRecord) (bool, string) {
	switch session.State {
	case protocol.Exiting:
		return true, fmt.Sprintf("Session %s (%s) is already exiting; no new termination request was sent.\n",
			displayText(session.Title), session.ID)
	case protocol.Exited, protocol.Lost:
		return true, fmt.Sprintf("Session %s (%s) is already %s; no live process was terminated.\n",
			displayText(session.Title), session.ID, session.State)
	default:
		return false, ""
	}
}

func terminationAcknowledgement(session protocol.BrokerRPCSessionRecord) string {
	switch session.State {
	case protocol.Exited, protocol.Lost:
		return fmt.Sprintf("Termination was acknowledged; session %s (%s) is %s and has no live process.\n",
			displayText(session.Title), session.ID, session.State)
	case protocol.Exiting:
		return fmt.Sprintf("Termination accepted for session %s (%s); the managed process tree is exiting.\n",
			displayText(session.Title), session.ID)
	default:
		return fmt.Sprintf("Termination acknowledged for session %s (%s); broker reported state %s.\n",
			displayText(session.Title), session.ID, session.State)
	}
}

// displayText returns quoted graphic text. ESC, newlines, bidi controls, and
// all other non-graphic runes are rendered as escapes rather than emitted.
func displayText(value string) string {
	return strconv.QuoteToGraphic(value)
}

func writeString(output io.Writer, value string) error {
	if _, err := io.WriteString(output, value); err != nil {
		return fmt.Errorf("write CLI output: %w", err)
	}
	return nil
}
