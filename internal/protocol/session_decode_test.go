package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeLiveSessionMessageBoundaries(t *testing.T) {
	lease := strings.Repeat("A", 43)
	payloads := map[MessageType]any{
		SessionAttach:   EmptyPayload{},
		ScreenResync:    ScreenResyncPayload{Reason: InterruptedSnapshot, LastRevision: stringPointer("9007199254740993")},
		SnapshotApplied: SnapshotAppliedPayload{SnapshotID: testSnapshotID, Revision: "1"},
		ControlAcquire:  EmptyPayload{},
		ControlRelease:  LeasePayload{Lease: lease},
		Heartbeat:       HeartbeatPayload{},
		InputKey:        InputKeyPayload{Lease: lease, InputSequence: "1", Key: Enter, Modifiers: Modifiers{}},
		InputText:       InputTextPayload{Lease: lease, InputSequence: "1", Text: "世界 👩‍💻 é \ufffd"},
		InputPaste:      InputPastePayload{Lease: lease, InputSequence: "1", Text: "one\r\ntwo\tthree"},
		InputMouse:      InputMousePayload{Lease: lease, InputSequence: "1", Kind: Wheel, Button: WheelUp, Column: 399, Row: 199, Modifiers: Modifiers{}},
		TerminalResize:  TerminalResizePayload{Lease: lease, Columns: 400, Rows: 200},
		HistoryRead:     HistoryReadPayload{HistoryEpoch: "1", HistoryRevision: "18446744073709551615", Offset: 4999, Limit: 200},
		SessionDetach:   EmptyPayload{},
	}
	for messageType, payload := range payloads {
		t.Run(string(messageType), func(t *testing.T) {
			valid := encodeTestMessage(t, messageType, payload, true)
			if _, err := DecodeClient(valid); err != nil {
				t.Fatalf("valid session message: %v", err)
			}
			if _, err := DecodeHost(valid); err == nil {
				t.Fatal("client action accepted in host direction")
			}
			for _, field := range []string{"requestId", "sessionId", "generation"} {
				for _, mutation := range []string{"missing", "malformed", "null", "case-shadow"} {
					t.Run(field+"/"+mutation, func(t *testing.T) {
						var object map[string]any
						if err := json.Unmarshal(valid, &object); err != nil {
							t.Fatal(err)
						}
						switch mutation {
						case "missing":
							delete(object, field)
						case "malformed":
							object[field] = "not-a-uuid"
						case "null":
							object[field] = nil
						case "case-shadow":
							object[strings.ToUpper(field)] = testRequestID
						}
						message, err := json.Marshal(object)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := DecodeClient(message); err == nil {
							t.Fatal("invalid session identity was accepted")
						}
					})
				}
			}
			for _, rawPayload := range []json.RawMessage{[]byte(`null`), []byte(`[]`), []byte(`{"raw":"\\u001b[6n"}`)} {
				if _, err := DecodeClient(encodeTestMessage(t, messageType, rawPayload, true)); err == nil {
					t.Fatalf("wrong payload shape %s was accepted", rawPayload)
				}
			}
		})
	}
}

func TestDecodeClientRejectsUnknownMouseButtons(t *testing.T) {
	for _, kind := range []Kind{Press, Release, Move, Wheel} {
		for _, button := range []Button{"", "invalid", "LEFT", "wheel-diagonal"} {
			t.Run(string(kind)+"/"+string(button), func(t *testing.T) {
				payload := InputMousePayload{
					Lease: strings.Repeat("A", 43), InputSequence: "1",
					Kind: kind, Button: button, Column: 0, Row: 0,
				}
				if _, err := DecodeClient(encodeTestMessage(t, InputMouse, payload, true)); err == nil {
					t.Fatal("unknown mouse button was accepted")
				}
			})
		}
	}
}

func TestMouseKindsAndButtonsMatchCanonicalSemantics(t *testing.T) {
	for _, kind := range []Kind{Press, Release, Move, Wheel, "invalid"} {
		for _, button := range []Button{ButtonNone, Left, Middle, Right, Backward, Forward, WheelUp, WheelDown, WheelLeft, WheelRight} {
			isWheel := strings.HasPrefix(string(button), "wheel-")
			want := kind == Wheel && isWheel || (kind == Press && button != ButtonNone || kind == Release || kind == Move) && !isWheel
			if got := ValidateMouseInput(kind, button, 0, 0) == nil; got != want {
				t.Errorf("kind %q button %q valid = %v, want %v", kind, button, got, want)
			}
		}
	}
}

func TestDecodeClientRejectsMalformedUTF8BeforeReplacement(t *testing.T) {
	for _, messageType := range []MessageType{InputText, InputPaste} {
		for name, invalid := range map[string][]byte{
			"invalid byte":       {0xff},
			"truncated sequence": {0xe4, 0xb8},
			"overlong sequence":  {0xc0, 0xaf},
			"encoded surrogate":  {0xed, 0xa0, 0x80},
		} {
			t.Run(string(messageType)+"/"+name, func(t *testing.T) {
				valid := encodeTestMessage(t, messageType, InputTextPayload{
					Lease: strings.Repeat("A", 43), InputSequence: "1", Text: "PLACEHOLDER",
				}, true)
				message := bytes.Replace(valid, []byte("PLACEHOLDER"), invalid, 1)
				if _, err := DecodeClient(message); err == nil {
					t.Fatal("malformed UTF-8 was accepted and replaced by encoding/json")
				}
			})
		}
	}
}

func TestDecimalCounterCanonicalBoundaries(t *testing.T) {
	for _, counter := range []string{"0", "1", "9007199254740993", "18446744073709551615"} {
		if _, err := parseCounter(counter); err != nil {
			t.Errorf("valid counter %q: %v", counter, err)
		}
	}
	for _, counter := range []string{"", "+1", "-1", " 1", "1 ", "01", "0x1", "1e1", "1.0", "18446744073709551616"} {
		if _, err := parseCounter(counter); err == nil {
			t.Errorf("noncanonical counter %q was accepted", counter)
		}
	}
}
