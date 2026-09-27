package desktop

import (
	"context"
	"errors"
	"reflect"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type recordingTransport struct {
	actions []InputAction
	err     error
}

func (transport *recordingTransport) Send(_ context.Context, action InputAction) error {
	transport.actions = append(transport.actions, action)
	return transport.err
}

func TestInputControllerMapsStructuredEvents(t *testing.T) {
	t.Parallel()
	controller := &InputController{}
	transport := &recordingTransport{}
	events := []uv.Event{
		uv.KeyPressEvent{Code: 'a', Text: "a"},
		uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl},
		uv.KeyPressEvent{Code: uv.KeyUp, Mod: uv.ModShift},
		uv.PasteEvent{Content: "one\ntwo"},
		uv.MouseWheelEvent(uv.Mouse{X: 3, Y: 4, Button: uv.MouseWheelDown, Mod: uv.ModAlt}),
		uv.CursorPositionEvent{X: 10, Y: 10}, // Outer reply: ignored.
	}
	for _, event := range events {
		detached, err := controller.Handle(context.Background(), event, transport)
		if err != nil || detached {
			t.Fatalf("handle %T = detached:%v err:%v", event, detached, err)
		}
	}
	want := []InputAction{
		TextAction{Text: "a"},
		KeyAction{Key: protocol.Character, Character: "c", Modifiers: protocol.Modifiers{Ctrl: true}},
		KeyAction{Key: protocol.ArrowUp, Modifiers: protocol.Modifiers{Shift: true}},
		PasteAction{Text: "one\ntwo"},
		MouseAction{Kind: protocol.Wheel, Button: protocol.WheelDown, Column: 3, Row: 4, Modifiers: protocol.Modifiers{Alt: true}},
	}
	if !reflect.DeepEqual(transport.actions, want) {
		t.Fatalf("actions = %#v, want %#v", transport.actions, want)
	}
}

func TestInputControllerDetachAndLiteralDetach(t *testing.T) {
	t.Parallel()
	detach := uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}
	quote := uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}

	controller := &InputController{}
	transport := &recordingTransport{}
	detached, err := controller.Handle(context.Background(), detach, transport)
	if err != nil || !detached || len(transport.actions) != 0 {
		t.Fatalf("detach = %v, actions:%#v err:%v", detached, transport.actions, err)
	}

	controller = &InputController{}
	if detached, err := controller.Handle(context.Background(), quote, transport); err != nil || detached {
		t.Fatalf("quote = detached:%v err:%v", detached, err)
	}
	if detached, err := controller.Handle(context.Background(), detach, transport); err != nil || detached {
		t.Fatalf("literal detach = detached:%v err:%v", detached, err)
	}
	want := KeyAction{Key: protocol.Character, Character: "]", Modifiers: protocol.Modifiers{Ctrl: true}}
	if len(transport.actions) != 1 || !reflect.DeepEqual(transport.actions[0], want) {
		t.Fatalf("literal detach actions = %#v", transport.actions)
	}
}

func TestInputControllerPreservesUnusedQuote(t *testing.T) {
	t.Parallel()
	controller := &InputController{}
	transport := &recordingTransport{}
	_, _ = controller.Handle(context.Background(), uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}, transport)
	_, err := controller.Handle(context.Background(), uv.KeyPressEvent{Code: 'x', Text: "x"}, transport)
	if err != nil {
		t.Fatal(err)
	}
	want := []InputAction{
		KeyAction{Key: protocol.Character, Character: "v", Modifiers: protocol.Modifiers{Ctrl: true}},
		TextAction{Text: "x"},
	}
	if !reflect.DeepEqual(transport.actions, want) {
		t.Fatalf("quoted ordinary key actions = %#v", transport.actions)
	}
}

func TestInputControllerReturnsTransportError(t *testing.T) {
	t.Parallel()
	want := errors.New("offline")
	controller := &InputController{}
	transport := &recordingTransport{err: want}
	_, err := controller.Handle(context.Background(), uv.PasteEvent{Content: "paste"}, transport)
	if !errors.Is(err, want) {
		t.Fatalf("transport error = %v", err)
	}
}

func TestInputControllerRejectsInvalidLocalInput(t *testing.T) {
	t.Parallel()
	controller := &InputController{}
	transport := &recordingTransport{}

	_, err := controller.Handle(context.Background(), uv.PasteEvent{Content: "\x1bunsafe"}, transport)
	if err == nil {
		t.Fatal("control paste unexpectedly accepted")
	}
	if len(transport.actions) != 0 {
		t.Fatalf("invalid action reached transport: %#v", transport.actions)
	}

	_, err = controller.Handle(context.Background(), uv.KeyPressEvent{Code: '界', Mod: uv.ModCtrl}, transport)
	if err == nil {
		t.Fatal("non-ASCII character key unexpectedly accepted")
	}
}

func TestInputControllerEmptyPasteIsNoOp(t *testing.T) {
	t.Parallel()
	controller := &InputController{}
	transport := &recordingTransport{}
	if _, err := controller.Handle(context.Background(), uv.PasteEvent{}, transport); err != nil || len(transport.actions) != 0 {
		t.Fatalf("empty paste = actions:%#v err:%v", transport.actions, err)
	}
}

// Ctrl+V only quotes Ctrl+]. A paste or mouse event after it sends the
// pending Ctrl+V first, so a later Ctrl+] still detaches.
func TestInputControllerQuoteBeforePasteOrMouseSendsCtrlV(t *testing.T) {
	t.Parallel()
	quote := uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}
	ctrlV := KeyAction{Key: protocol.Character, Character: "v", Modifiers: protocol.Modifiers{Ctrl: true}}
	for _, test := range []struct {
		event uv.Event
		want  InputAction
	}{
		{uv.PasteEvent{Content: "p"}, PasteAction{Text: "p"}},
		{uv.MouseClickEvent(uv.Mouse{X: 1, Y: 2, Button: uv.MouseLeft}), MouseAction{Kind: protocol.Press, Button: protocol.Left, Column: 1, Row: 2}},
	} {
		controller := &InputController{}
		transport := &recordingTransport{}
		for _, event := range []uv.Event{quote, test.event} {
			if detached, err := controller.Handle(context.Background(), event, transport); err != nil || detached {
				t.Fatalf("%T = detached:%v err:%v", event, detached, err)
			}
		}
		detached, err := controller.Handle(context.Background(), uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}, transport)
		if err != nil || !detached {
			t.Fatalf("Ctrl+] after %T = detached:%v err:%v", test.event, detached, err)
		}
		if want := []InputAction{ctrlV, test.want}; !reflect.DeepEqual(transport.actions, want) {
			t.Fatalf("actions = %#v, want %#v", transport.actions, want)
		}
	}
}
