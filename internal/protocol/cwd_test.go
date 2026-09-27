package protocol

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var validWorkingDirectories = []string{
	"/", "/Users/me/src/app", "/tmp/with space", "/home/me/東京", `C:\Users\me`, "c:/work", `\\server\share\dir`,
	"/" + strings.Repeat("a", MaxWorkingDirectoryBytes-1),
}

var invalidWorkingDirectories = []string{
	"", "relative/dir", "~/src", "./x", "C:relative", "/a/../etc", "/a/./b", `C:\a\..\b`,
	"/a\nb", "/a\x1b]0;x\x07", "/a\x7f", "/a\u009b", "/a\tb", " /a", "/a ", "/a\x00",
	"/" + strings.Repeat("a", MaxWorkingDirectoryBytes), "/\xff",
}

func TestValidateWorkingDirectory(t *testing.T) {
	for _, value := range validWorkingDirectories {
		if err := ValidateWorkingDirectory(value); err != nil {
			t.Errorf("%q rejected: %v", value, err)
		}
	}
	for _, value := range invalidWorkingDirectories {
		if ValidateWorkingDirectory(value) == nil {
			t.Errorf("%q accepted", value)
		}
	}
}

func TestWorkingDirectoryOnSessionStateAndRecord(t *testing.T) {
	cwd := "/Users/me/src/app"
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Running, Cwd: &cwd}, true)); err != nil {
		t.Fatalf("running cwd: %v", err)
	}
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Exiting, Cwd: &cwd}, true)); err == nil {
		t.Fatal("cwd accepted while not running")
	}
	bad := "relative"
	if _, err := DecodeHost(encodeTestMessage(t, SessionState, SessionStatePayload{State: Running, Cwd: &bad}, true)); err == nil {
		t.Fatal("relative cwd accepted on session.state")
	}

	sessionID, generation := brokerRPCSessionID, brokerRPCGeneration
	decode := func(mutate func(*BrokerRPCSessionRecord)) error {
		record := validBrokerRPCSessionRecord()
		mutate(&record)
		message := encodeBrokerRPCTestMessage(t, BrokerRPCEnvelope{
			Version: Version, Type: SessionStatusResult, RequestID: brokerRPCRequestID,
			SessionID: &sessionID, Generation: &generation,
		}, BrokerRPCSessionResultPayload{Session: record})
		_, err := DecodeBrokerRPCResponse(message)
		return err
	}
	custom := true
	if err := decode(func(record *BrokerRPCSessionRecord) { record.Cwd, record.TitleIsCustom = &cwd, &custom }); err != nil {
		t.Fatalf("record with cwd and titleIsCustom rejected: %v", err)
	}
	for _, value := range invalidWorkingDirectories {
		if !utf8.ValidString(value) {
			continue // JSON encoding already replaces invalid UTF-8.
		}
		if decode(func(record *BrokerRPCSessionRecord) { record.Cwd = &value }) == nil {
			t.Errorf("record cwd %q accepted", value)
		}
	}
	if decode(func(record *BrokerRPCSessionRecord) {
		record.State = Exited
		record.EndedAt = brokerRPCStringPointer("2026-09-20T10:00:02Z")
		record.UpdatedAt = "2026-09-20T10:00:02Z"
		record.Cwd = &cwd
	}) == nil {
		t.Fatal("exited record with cwd accepted")
	}
}
