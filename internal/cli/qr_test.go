package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"rsc.io/qr"
)

// The rendered half blocks must decode back to exactly the encoder's module
// matrix, surrounded by a light quiet zone, in explicit dark-on-light colors.
func TestRenderQRMatchesModuleMatrixWithQuietZone(t *testing.T) {
	link := "https://mac.example.ts.net/pair#" + adminGrant
	rendered, err := renderQR(link)
	if err != nil {
		t.Fatal(err)
	}
	code, err := qr.Encode(link, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	width := code.Size + 2*qrQuietZone
	lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(lines) != (width+1)/2 {
		t.Fatalf("%d rows, want %d", len(lines), (width+1)/2)
	}
	for row, line := range lines {
		if !strings.HasPrefix(line, qrColors) || !strings.HasSuffix(line, qrReset) {
			t.Fatalf("row %d lacks explicit colors: %q", row, line)
		}
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, qrColors), qrReset))
		if len(cells) != width {
			t.Fatalf("row %d has %d cells, want %d", row, len(cells), width)
		}
		for column, cell := range cells {
			top, bottom := cell == '█' || cell == '▀', cell == '█' || cell == '▄'
			x, y := column-qrQuietZone, 2*row-qrQuietZone
			if top != code.Black(x, y) || bottom != code.Black(x, y+1) {
				t.Fatalf("module (%d,%d) mismatch: cell %q", x, y, cell)
			}
			inCode := x >= 0 && x < code.Size && y >= 0 && y < code.Size
			if !inCode && top {
				t.Fatalf("quiet zone is dark at (%d,%d)", x, y)
			}
		}
	}
	// A finder pattern's dark corner proves the matrix is not empty.
	if !code.Black(0, 0) || !code.Black(code.Size-1, 0) || !code.Black(0, code.Size-1) {
		t.Fatal("encoder output lacks finder patterns")
	}
}

func TestPairQRAndTooLongLinkKeepTheSecretOutOfErrors(t *testing.T) {
	origin := "https://mac.example.ts.net"
	admin := &fakeAdmin{grant: testGrant(&origin)}
	var output bytes.Buffer
	if err := Pair(context.Background(), admin, PairOptions{ShowQR: true}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, qrColors+" ") || !strings.Contains(text, "Scan the QR code") ||
		strings.Count(text, adminGrant) != 1 {
		t.Fatalf("pairing output with QR = %q", text)
	}
	if _, err := renderQR("https://" + strings.Repeat("a", 480) + "/pair#" + adminGrant); err == nil ||
		strings.Contains(err.Error(), adminGrant) {
		t.Fatalf("oversized QR error = %v", err)
	}
}
