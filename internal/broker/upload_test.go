package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)
	testJPEG = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{2}, 32)...)
)

func TestDetectImageUsesMagicBytesOnly(t *testing.T) {
	for data, want := range map[string]protocol.HTTPUploadContentType{
		string(testJPEG):              protocol.ImageJPEG,
		string(testPNG):               protocol.ImagePNG,
		"GIF87a....":                  protocol.ImageGIF,
		"GIF89a....":                  protocol.ImageGIF,
		"RIFF\x10\x00\x00\x00WEBPVP8": protocol.ImageWebp,
	} {
		if got, _, ok := detectImage([]byte(data)); !ok || got != want {
			t.Errorf("%q = %q, %v; want %q", data[:6], got, ok, want)
		}
	}
	for _, data := range []string{
		"", "\xff\xd8", "\x89PNG\r\n", "RIFF\x10\x00\x00\x00WAVEfmt ", "RIFFWEBP",
		`<svg xmlns="http://www.w3.org/2000/svg"/>`, "<html>png</html>", "#!/bin/sh\necho hi",
	} {
		if kind, _, ok := detectImage([]byte(data)); ok {
			t.Errorf("%q accepted as %q", data, kind)
		}
	}
}

func TestSaveUploadWritesPrivateFileAndIgnoresFolder(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 9, 27, 14, 30, 12, 0, time.Local)
	result, err := saveUpload(project, testPNG, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.ValidateUploadResponse(result); err != nil || result.ContentType != protocol.ImagePNG ||
		result.Size != int64(len(testPNG)) || !strings.HasPrefix(result.Path, ".termivoa/uploads/20260927-143012-") {
		t.Fatalf("result = %+v, %v", result, err)
	}
	path := filepath.Join(project, filepath.FromSlash(result.Path))
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, testPNG) {
		t.Fatalf("saved file = %q, %v", data, err)
	}
	if ignore, err := os.ReadFile(filepath.Join(project, ".termivoa", ".gitignore")); err != nil || string(ignore) != "*\n" {
		t.Fatalf(".gitignore = %q, %v", ignore, err)
	}
	if runtime.GOOS != "windows" {
		for name, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700, filepath.Join(project, ".termivoa"): 0o700} {
			if info, err := os.Stat(name); err != nil || info.Mode().Perm() != want {
				t.Errorf("%s mode = %v, %v; want %v", name, info.Mode().Perm(), err, want)
			}
		}
	}
	// An existing .gitignore is left alone.
	if err := os.WriteFile(filepath.Join(project, ".termivoa", ".gitignore"), []byte("uploads/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveUpload(project, testJPEG, now); err != nil {
		t.Fatal(err)
	}
	if ignore, _ := os.ReadFile(filepath.Join(project, ".termivoa", ".gitignore")); string(ignore) != "uploads/\n" {
		t.Fatalf(".gitignore overwritten: %q", ignore)
	}
	if _, err := saveUpload(project, []byte("<svg/>"), now); !errors.Is(err, errUploadType) {
		t.Fatalf("svg = %v", err)
	}
}

func TestSaveUploadRefusesLinkedOrFileFolders(t *testing.T) {
	now := time.Now()
	outside := t.TempDir()
	cases := map[string]func(t *testing.T, project string){
		".termivoa symlink": func(t *testing.T, project string) {
			if err := os.Symlink(outside, filepath.Join(project, ".termivoa")); err != nil {
				t.Skip(err)
			}
		},
		".termivoa symlink inside project": func(t *testing.T, project string) {
			if err := os.Mkdir(filepath.Join(project, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", filepath.Join(project, ".termivoa")); err != nil {
				t.Skip(err)
			}
		},
		"uploads symlink": func(t *testing.T, project string) {
			if err := os.Mkdir(filepath.Join(project, ".termivoa"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(project, ".termivoa", "uploads")); err != nil {
				t.Skip(err)
			}
		},
		".termivoa file": func(t *testing.T, project string) {
			if err := os.WriteFile(filepath.Join(project, ".termivoa"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			setup(t, project)
			if _, err := saveUpload(project, testPNG, now); !errors.Is(err, errUploadFolder) {
				t.Fatalf("err = %v, want errUploadFolder", err)
			}
		})
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the project: %v", entries)
	}
}

func TestSaveUploadNeverOverwrites(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 9, 27, 14, 30, 12, 0, time.Local)
	suffixes := []string{"deadbeef", "deadbeef", "cafebabe"}
	previous := uploadSuffix
	uploadSuffix = func() (string, error) {
		next := suffixes[0]
		suffixes = suffixes[1:]
		return next, nil
	}
	t.Cleanup(func() { uploadSuffix = previous })
	first, err := saveUpload(project, testPNG, now)
	if err != nil || first.Path != ".termivoa/uploads/20260927-143012-deadbeef.png" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	// The same name comes up again: O_EXCL refuses it and a new name is drawn.
	second, err := saveUpload(project, append(append([]byte(nil), testPNG...), 9), now)
	if err != nil || second.Path != ".termivoa/uploads/20260927-143012-cafebabe.png" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if data, _ := os.ReadFile(filepath.Join(project, ".termivoa", "uploads", "20260927-143012-deadbeef.png")); !bytes.Equal(data, testPNG) {
		t.Fatal("first upload was overwritten")
	}
	uploadSuffix = func() (string, error) { return "deadbeef", nil }
	if _, err := saveUpload(project, testPNG, now); err == nil {
		t.Fatal("collisions on every attempt still saved a file")
	}
}

func TestUploadLimitsPerDevice(t *testing.T) {
	var limits uploadLimits
	now := time.Now()
	release, err := limits.acquire("phone", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limits.acquire("phone", now); !errors.Is(err, errUploadLimit) {
		t.Fatalf("second concurrent upload = %v", err)
	}
	release()
	for range uploadsPerMinute - 1 {
		release, err := limits.acquire("phone", now)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, err := limits.acquire("phone", now.Add(59*time.Second)); !errors.Is(err, errUploadLimit) {
		t.Fatalf("upload over the per-minute limit = %v", err)
	}
	if release, err := limits.acquire("tablet", now); err != nil {
		t.Fatalf("other device limited: %v", err)
	} else {
		release()
	}
	if release, err := limits.acquire("phone", now.Add(time.Minute)); err != nil {
		t.Fatalf("next minute = %v", err)
	} else {
		release()
	}
	for index := range maxActiveUploads {
		if _, err := limits.acquire(string(rune('a'+index)), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := limits.acquire("z", now.Add(time.Minute)); !errors.Is(err, errUploadLimit) {
		t.Fatalf("upload over the total limit = %v", err)
	}
}

// uploadProject points the fixture project at a temporary folder.
func uploadProject(t *testing.T, fixture *managementFixture) string {
	t.Helper()
	ctx := context.Background()
	project, err := fixture.persistence.GetProject(ctx, rpcProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.CanonicalPath, project.UpdatedAt = t.TempDir(), time.Now().UTC()
	if err := fixture.persistence.PutProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	return project.CanonicalPath
}

func uploadChunk(data []byte) protocol.GatewayRPCUploadChunkPayload {
	return protocol.GatewayRPCUploadChunkPayload{Data: base64.StdEncoding.EncodeToString(data)}
}

func TestGatewayUploadIsControllerOnlyAndChunked(t *testing.T) {
	fixture, session, _, service, _ := newAuthStreamFixture(t)
	directory := uploadProject(t, fixture)
	phone := pairDevice(t, service, "Pocket")
	tablet := pairDevice(t, service, "Tablet")
	viewer := attachPresenceViewer(t, session, "8ccccccc-cccc-4ccc-8ccc-ccccccccccc1", phone.DeviceID)
	other := attachPresenceViewer(t, session, "8ccccccc-cccc-4ccc-8ccc-ccccccccccc2", tablet.DeviceID)
	begin := func(credential, generation string, size int) protocol.GatewayRPCUploadBeginPayload {
		return protocol.GatewayRPCUploadBeginPayload{Credential: credential, SessionID: rpcSessionID, Generation: generation, Size: int64(size)}
	}
	image := append(append([]byte(nil), testPNG...), bytes.Repeat([]byte{7}, protocol.MaxUploadChunkBytes+10)...)

	// Nobody controls the session yet.
	client := startRPCTestClient(t, fixture.server, protocol.Gateway)
	if code := gatewayErrorCode(t, client.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, len(image)))); code != protocol.NotController {
		t.Fatalf("uncontrolled = %s", code)
	}
	if _, err := session.AcquireControl(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		payload protocol.GatewayRPCUploadBeginPayload
		code    protocol.ErrorCode
	}{
		"other device":   {begin(tablet.Credential, rpcSessionGen, len(image)), protocol.NotController},
		"old generation": {begin(phone.Credential, rpcSessionID, len(image)), protocol.GenerationChanged},
		"unknown device": {begin(strings.Repeat("A", 43), rpcSessionGen, len(image)), protocol.Unauthorized},
	} {
		client := startRPCTestClient(t, fixture.server, protocol.Gateway)
		if code := gatewayErrorCode(t, client.gateway(t, protocol.GatewayUploadBegin, test.payload)); code != test.code {
			t.Errorf("%s = %s, want %s", name, code, test.code)
		}
	}
	// Chunks and commits only follow a begin on the same connection.
	stray := startRPCTestClient(t, fixture.server, protocol.Gateway)
	if code := gatewayErrorCode(t, stray.gateway(t, protocol.GatewayUploadChunk, uploadChunk(testPNG))); code != protocol.InvalidRequest {
		t.Fatalf("stray chunk = %s", code)
	}

	upload := startRPCTestClient(t, fixture.server, protocol.Gateway)
	if response := upload.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, len(image))); response.Type != protocol.GatewayUploadBeginResult {
		t.Fatalf("begin = %s", response.Payload)
	}
	for start := 0; start < len(image); start += protocol.MaxUploadChunkBytes {
		chunk := image[start:min(start+protocol.MaxUploadChunkBytes, len(image))]
		if response := upload.gateway(t, protocol.GatewayUploadChunk, uploadChunk(chunk)); response.Type != protocol.GatewayUploadChunkResult {
			t.Fatalf("chunk = %s", response.Payload)
		}
	}
	result, err := protocol.DecodeRPCPayload[protocol.HTTPUploadResponse](
		upload.gateway(t, protocol.GatewayUploadCommit, credentialPayload(phone.Credential)))
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(result.Path))); err != nil || !bytes.Equal(saved, image) {
		t.Fatalf("saved %d bytes, %v", len(saved), err)
	}
	// The connection ends after commit.
	if _, err := upload.exchange(t, string(protocol.GatewayUploadChunk), uploadChunk(testPNG)); err == nil {
		t.Fatal("upload connection stayed open after commit")
	}

	// More bytes than declared.
	over := startRPCTestClient(t, fixture.server, protocol.Gateway)
	over.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, 8))
	if code := gatewayErrorCode(t, over.gateway(t, protocol.GatewayUploadChunk, uploadChunk(testPNG))); code != protocol.InvalidRequest {
		t.Fatalf("oversized chunk = %s", code)
	}
	// Fewer bytes than declared.
	short := startRPCTestClient(t, fixture.server, protocol.Gateway)
	short.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, len(testPNG)+1))
	short.gateway(t, protocol.GatewayUploadChunk, uploadChunk(testPNG))
	if code := gatewayErrorCode(t, short.gateway(t, protocol.GatewayUploadCommit, credentialPayload(phone.Credential))); code != protocol.InvalidRequest {
		t.Fatalf("short upload = %s", code)
	}
	// Not an image.
	text := startRPCTestClient(t, fixture.server, protocol.Gateway)
	text.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, 6))
	text.gateway(t, protocol.GatewayUploadChunk, uploadChunk([]byte("<svg/>")))
	if code := gatewayErrorCode(t, text.gateway(t, protocol.GatewayUploadCommit, credentialPayload(phone.Credential))); code != protocol.InvalidRequest {
		t.Fatalf("svg upload = %s", code)
	}
	// Control moves to the tablet before commit: nothing is written.
	moved := startRPCTestClient(t, fixture.server, protocol.Gateway)
	moved.gateway(t, protocol.GatewayUploadBegin, begin(phone.Credential, rpcSessionGen, len(testPNG)))
	moved.gateway(t, protocol.GatewayUploadChunk, uploadChunk(testPNG))
	if _, err := session.AcquireControl(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if code := gatewayErrorCode(t, moved.gateway(t, protocol.GatewayUploadCommit, credentialPayload(phone.Credential))); code != protocol.NotController {
		t.Fatalf("commit after takeover = %s", code)
	}
	if entries, _ := os.ReadDir(filepath.Join(directory, ".termivoa", "uploads")); len(entries) != 1 {
		t.Fatalf("uploads = %v, want only the committed one", entries)
	}
}
