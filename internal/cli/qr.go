package cli

import (
	"errors"
	"strings"

	"rsc.io/qr"
)

const (
	qrQuietZone = 4  // modules of light border that scanners require
	qrMaxWidth  = 80 // terminal columns, quiet zone included
	// Explicit black-on-bright-white, so the code scans on dark and light
	// terminal themes alike.
	qrColors = "\x1b[30;107m"
	qrReset  = "\x1b[0m"
)

// renderQR draws text as a QR code with Unicode half blocks: one terminal
// row holds two module rows. columns is the terminal width, zero when
// unknown: a code wider than the terminal would wrap into noise (a phone-sized
// Termivoa session). Errors never include text, which is a secret.
func renderQR(text string, columns int) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", errors.New("the pairing link cannot be encoded as a QR code")
	}
	width := code.Size + 2*qrQuietZone
	if width > qrMaxWidth {
		return "", errors.New("the pairing link is too long for a terminal QR code")
	}
	if columns > 0 && width > columns {
		return "", errors.New("this terminal is too narrow for it")
	}
	var builder strings.Builder
	// Black reports false outside the code, which draws the quiet zone.
	for y := -qrQuietZone; y < code.Size+qrQuietZone; y += 2 {
		builder.WriteString(qrColors)
		for x := -qrQuietZone; x < code.Size+qrQuietZone; x++ {
			switch top, bottom := code.Black(x, y), code.Black(x, y+1); {
			case top && bottom:
				builder.WriteString("█")
			case top:
				builder.WriteString("▀")
			case bottom:
				builder.WriteString("▄")
			default:
				builder.WriteByte(' ')
			}
		}
		builder.WriteString(qrReset + "\n")
	}
	return builder.String(), nil
}
