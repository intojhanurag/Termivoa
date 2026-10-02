package protocol

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxComputers                 = 8
	MaxBoundOrigins              = 64
	MaxPairingOrigins            = 16
	MaxAdminComputers            = 512
	MaxComputerOriginBytes       = 261
	MaxComputerLabelBytes        = 64
	MaxTailnetOwnerBytes         = 256
	MaxTailnetTags               = 16
	MaxThumbnailUpdatesPerSecond = 30
)

// ValidateComputerOrigin accepts only the canonical origin of a Termivoa
// computer: https, a lowercase DNS host of at least two labels, the default
// port (an explicit port, even :443, is rejected), and no trailing dot, user,
// path, query or fragment. Bound origins are compared as exact strings, so
// each computer has exactly one spelling.
func ValidateComputerOrigin(value string) error {
	host, ok := strings.CutPrefix(value, "https://")
	if !ok || len(value) > MaxComputerOriginBytes {
		return errors.New("computer origin must be https://host")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return errors.New("computer origin host needs at least two labels")
	}
	for _, label := range labels {
		if !validDNSLabel(label) {
			return errors.New("computer origin host is not a lowercase DNS name")
		}
	}
	return nil
}

func validDNSLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for index := 0; index < len(label); index++ {
		character := label[index]
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '-' {
			return false
		}
	}
	return true
}

// ValidatePairVia accepts a full computer origin or a single lowercase DNS
// label (the short form of pterm pair --via).
func ValidatePairVia(value string) error {
	if strings.HasPrefix(value, "https://") {
		return ValidateComputerOrigin(value)
	}
	if !validDNSLabel(value) {
		return errors.New("via must be an https origin or one lowercase DNS label")
	}
	return nil
}

// ValidateComputerLabel bounds a computer's display name. It is never identity.
func ValidateComputerLabel(value string) error {
	return validateBrokerRPCText(value, MaxComputerLabelBytes, "computer label")
}

func validateComputer(computer HTTPComputer) error {
	if err := ValidateComputerOrigin(computer.Origin); err != nil {
		return err
	}
	return ValidateComputerLabel(computer.Label)
}

func validateComputerList(computers []HTTPComputer) error {
	if computers == nil || len(computers) > MaxComputers {
		return errors.New("computer list is outside bounds")
	}
	for index, computer := range computers {
		if err := validateComputer(computer); err != nil {
			return fmt.Errorf("computers[%d]: %w", index, err)
		}
	}
	return nil
}

// validateOriginSet checks a list of unique computer origins. An absent list
// is allowed only when optional is true; a present one must not be empty then.
func validateOriginSet(origins []string, limit int, optional bool, name string) error {
	if origins == nil && optional {
		return nil
	}
	if origins == nil || len(origins) > limit || optional && len(origins) == 0 {
		return fmt.Errorf("%s is outside bounds", name)
	}
	seen := make(map[string]bool, len(origins))
	for _, origin := range origins {
		if err := ValidateComputerOrigin(origin); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if seen[origin] {
			return fmt.Errorf("%s contains a duplicate", name)
		}
		seen[origin] = true
	}
	return nil
}

func validateTailnetNode(node TailnetNode) error {
	if node.ID == "" || len(node.ID) > 64 {
		return errors.New("node id is outside bounds")
	}
	for index := 0; index < len(node.ID); index++ {
		character := node.ID[index]
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return errors.New("node id has an invalid character")
		}
	}
	if node.Owner != nil {
		if err := validateBrokerRPCText(*node.Owner, MaxTailnetOwnerBytes, "node owner"); err != nil {
			return err
		}
	}
	if node.Tags == nil || len(node.Tags) > MaxTailnetTags {
		return errors.New("node tags are outside bounds")
	}
	for _, tag := range node.Tags {
		name, ok := strings.CutPrefix(tag, "tag:")
		if !ok || name == "" || len(name) > 60 {
			return errors.New("node tag is outside bounds")
		}
		for index := 0; index < len(name); index++ {
			if name[index] < '!' || name[index] > '~' {
				return errors.New("node tag must be printable ASCII")
			}
		}
	}
	return nil
}

// validateAppBinding checks the optional appOrigin and appNode of a grant,
// pending pairing or device.
func validateAppBinding(appOrigin *string, appNode *TailnetNode) error {
	if appOrigin != nil {
		if err := ValidateComputerOrigin(*appOrigin); err != nil {
			return fmt.Errorf("appOrigin: %w", err)
		}
	}
	if appNode != nil {
		if appOrigin == nil {
			return errors.New("appNode needs appOrigin")
		}
		return validateTailnetNode(*appNode)
	}
	return nil
}

func validateComputerState(state HTTPComputerState) error {
	if state != HTTPComputerStatePending && state != Confirmed {
		return errors.New("computer state is not a recognized value")
	}
	return nil
}

// ValidateSessionAttach bounds the optional thumbnail mode of session.attach.
func ValidateSessionAttach(payload SessionAttachPayload) error {
	if payload.Thumbnail != nil &&
		(payload.Thumbnail.MaxUpdatesPerSecond < 1 || payload.Thumbnail.MaxUpdatesPerSecond > MaxThumbnailUpdatesPerSecond) {
		return errors.New("thumbnail maxUpdatesPerSecond is outside 1..30")
	}
	return nil
}

func validateGatewayComputerPayload(envelope GatewayRPCEnvelope) error {
	switch envelope.Type {
	case GatewayComputersAdd:
		payload, err := DecodeRPCPayload[GatewayRPCComputerAddPayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if err := validateComputer(HTTPComputer{Origin: payload.Request.Origin, Label: payload.Request.Label}); err != nil {
			return err
		}
		return validateComputerState(payload.Request.State)
	case GatewayComputersRemove:
		payload, err := DecodeRPCPayload[GatewayRPCComputerRemovePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		return ValidateComputerOrigin(payload.Origin)
	case GatewayComputersListResult:
		payload, err := DecodeRPCPayload[GatewayRPCComputerListResultPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateComputerList(payload.Computers); err != nil {
			return err
		}
		return validateOriginSet(payload.PendingOrigins, MaxComputers, false, "pendingOrigins")
	case GatewayComputersAddResult:
		payload, err := DecodeRPCPayload[HTTPComputerAddResponse](envelope)
		if err != nil {
			return err
		}
		if err := validateComputer(payload.Computer); err != nil {
			return err
		}
		if err := validateComputerState(payload.State); err != nil {
			return err
		}
		return validateTailnetNode(payload.Node)
	default:
		return errors.New("unknown gateway computers message type")
	}
}

func validateAdminComputerPayload(envelope AdminRPCEnvelope) error {
	switch envelope.Type {
	case AdminPairGrant:
		payload, err := DecodeRPCPayload[AdminRPCPairGrantPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Via != nil {
			return ValidatePairVia(*payload.Via)
		}
		if payload.AllowTagged != nil {
			return errors.New("allowTagged needs via")
		}
		return nil
	case AdminDeviceRevokeVia:
		payload, err := DecodeRPCPayload[AdminRPCRevokeViaPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateComputerOrigin(payload.AppOrigin)
	case AdminDeviceRevokeViaResult:
		payload, err := DecodeRPCPayload[AdminRPCRevokeViaResultPayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateComputerOrigin(payload.AppOrigin); err != nil {
			return err
		}
		if payload.DeviceIDS == nil || len(payload.DeviceIDS) > MaxAdminDevices || payload.ClosedStreams < 0 || payload.ClosedStreams > 65536 {
			return errors.New("revocation result is outside bounds")
		}
		for _, id := range payload.DeviceIDS {
			if !ValidUUID(id) {
				return errors.New("device id must be a canonical UUID")
			}
		}
		return nil
	case AdminComputersListResult:
		payload, err := DecodeRPCPayload[AdminRPCComputerListResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Computers == nil || len(payload.Computers) > MaxAdminComputers {
			return errors.New("computer list is outside bounds")
		}
		for _, computer := range payload.Computers {
			if !ValidUUID(computer.DeviceID) {
				return errors.New("device id must be a canonical UUID")
			}
			if err := validateComputer(HTTPComputer{Origin: computer.Origin, Label: computer.Label}); err != nil {
				return err
			}
			if _, err := parseBrokerRPCTime(computer.CreatedAt, "createdAt"); err != nil {
				return err
			}
		}
		return nil
	case AdminComputersRemove:
		payload, err := DecodeRPCPayload[AdminRPCComputerRemovePayload](envelope)
		if err != nil {
			return err
		}
		return ValidateComputerOrigin(payload.Origin)
	case AdminComputersRemoveResult:
		payload, err := DecodeRPCPayload[AdminRPCComputerRemoveResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Removed < 0 || payload.Removed > MaxAdminDevices {
			return errors.New("removed count is outside bounds")
		}
		return ValidateComputerOrigin(payload.Origin)
	default:
		return errors.New("unknown admin RPC message type")
	}
}
