package protocol

import "errors"

// MaxPreviewPorts bounds previews on a session record or session.state.
const MaxPreviewPorts = 8

// ValidatePreviewPorts applies the previewPorts schema plus the rules it does
// not express: ports are unique and sorted, since is an RFC 3339 time. A nil
// list means none; a present list is never empty.
func ValidatePreviewPorts(previews []PreviewPort) error {
	if previews == nil {
		return nil
	}
	if len(previews) == 0 || len(previews) > MaxPreviewPorts {
		return errors.New("previews are outside bounds")
	}
	for index, preview := range previews {
		if !ValidPreviewPort(preview.Port) || index > 0 && preview.Port <= previews[index-1].Port {
			return errors.New("preview ports must be unique, sorted and 1..65535")
		}
		if _, err := parseBrokerRPCTime(preview.Since, "since"); err != nil {
			return err
		}
	}
	return nil
}

// ValidPreviewPort reports whether port is a TCP port number.
func ValidPreviewPort(port int64) bool { return port >= 1 && port <= 65535 }
