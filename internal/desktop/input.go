package desktop

import (
	"context"
	"errors"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Transport sends structured terminal input. The eventual IPC transport owns
// lease, generation, and sequence assignment; the desktop decoder never writes
// terminal bytes or terminal-query responses directly.
type Transport interface {
	Send(context.Context, InputAction) error
}

// AdmittedTransport pins decoded input to one uninterrupted ready/controller
// epoch. Implementations recheck admission immediately before transport writes.
type AdmittedTransport interface {
	Transport
	AdmissionEpoch() uint64
	SendAdmitted(context.Context, InputAction, uint64) error
}

type eventAdmissionTransport struct {
	AdmittedTransport
	epoch uint64
}

func (transport eventAdmissionTransport) Send(ctx context.Context, action InputAction) error {
	if transport.epoch == 0 || transport.epoch != transport.AdmissionEpoch() {
		return nil
	}
	return transport.SendAdmitted(ctx, action, transport.epoch)
}

// InputAction is one validated input intent sent by the desktop client.
type InputAction interface {
	desktopInputAction()
}

type TextAction struct{ Text string }
type PasteAction struct{ Text string }
type KeyAction struct {
	Key       protocol.KeyName
	Character string
	Modifiers protocol.Modifiers
}
type MouseAction struct {
	Kind      protocol.Kind
	Button    protocol.Button
	Column    int64
	Row       int64
	Modifiers protocol.Modifiers
}

func (TextAction) desktopInputAction()  {}
func (PasteAction) desktopInputAction() {}
func (KeyAction) desktopInputAction()   {}
func (MouseAction) desktopInputAction() {}

func sendValidated(ctx context.Context, transport Transport, action InputAction) error {
	var err error
	switch action := action.(type) {
	case TextAction:
		err = protocol.ValidateTextInput(action.Text)
	case PasteAction:
		err = protocol.ValidatePasteInput(action.Text)
	case KeyAction:
		var character *string
		if action.Character != "" {
			character = &action.Character
		}
		err = protocol.ValidateKeyInput(action.Key, character, action.Modifiers)
	case MouseAction:
		err = protocol.ValidateMouseInput(action.Kind, action.Button, action.Column, action.Row)
	default:
		err = errors.New("unsupported desktop input action")
	}
	if err != nil {
		return fmt.Errorf("desktop: invalid input: %w", err)
	}
	return transport.Send(ctx, action)
}

// InputController maps Ultraviolet events into shared protocol input actions.
// Ctrl+] detaches locally. Ctrl+V quotes the next Ctrl+] so the child can
// receive a literal detach character.
type InputController struct {
	quoteNext  bool
	quoteEpoch uint64
	pasting    bool
	pasteEpoch uint64
}

// Handle returns detached=true when the local attachment should exit.
func (controller *InputController) Handle(ctx context.Context, event uv.Event, transport Transport) (detached bool, err error) {
	if transport == nil {
		return false, errors.New("desktop input transport must not be nil")
	}
	epoch, current := uint64(1), uint64(1)
	if admitted, ok := transport.(AdmittedTransport); ok {
		current = admitted.AdmissionEpoch()
		epoch = current
		if tagged, ok := event.(uv.InputEpochEvent); ok {
			event, epoch = tagged.Event, tagged.Epoch
		}
		transport = eventAdmissionTransport{AdmittedTransport: admitted, epoch: epoch}
	} else if tagged, ok := event.(uv.InputEpochEvent); ok {
		// Provenance cannot be represented by a legacy transport. Detach is
		// still local, but no tagged child action is silently downgraded.
		event, epoch, current = tagged.Event, 0, 0
	}
	if controller.quoteNext && (controller.quoteEpoch != current || epoch != current || current == 0) {
		controller.quoteNext = false
	}
	switch event := event.(type) {
	case uv.PasteStartEvent:
		controller.pasting, controller.pasteEpoch = true, epoch
		return false, nil
	case uv.PasteEndEvent:
		controller.pasting, controller.pasteEpoch = false, 0
		return false, nil
	case uv.KeyReleaseEvent:
		return false, nil
	case uv.KeyPressEvent:
		key := event.Key()
		if isCtrlKey(key, 'v') && !controller.quoteNext {
			controller.quoteNext = epoch != 0 && epoch == current
			controller.quoteEpoch = epoch
			return false, nil
		}
		if isCtrlKey(key, ']') {
			if !controller.quoteNext {
				return true, nil
			}
			controller.quoteNext = false
			return false, sendValidated(ctx, transport, KeyAction{
				Key:       protocol.Character,
				Character: "]",
				Modifiers: protocol.Modifiers{Ctrl: true},
			})
		}
		if epoch == 0 || epoch != current {
			return false, nil
		}
		if controller.quoteNext {
			// Ctrl+V is only a local quote action for the reserved detach key.
			// For any other key, preserve both user actions in order.
			controller.quoteNext = false
			if err := sendValidated(ctx, transport, KeyAction{
				Key:       protocol.Character,
				Character: "v",
				Modifiers: protocol.Modifiers{Ctrl: true},
			}); err != nil {
				return false, err
			}
		}
		action, ok := keyAction(key)
		if !ok {
			return false, nil
		}
		return false, sendValidated(ctx, transport, action)
	case uv.PasteEvent:
		if epoch == 0 || epoch != current || (controller.pasting && controller.pasteEpoch != epoch) {
			return false, nil
		}
		return false, sendValidated(ctx, transport, PasteAction{Text: event.Content})
	case uv.MouseEvent:
		if epoch == 0 || epoch != current {
			return false, nil
		}
		action, ok := mouseAction(event)
		if !ok {
			return false, nil
		}
		return false, sendValidated(ctx, transport, action)
	default:
		// Outer-terminal query replies, capability reports, focus events, and
		// unknown sequences are intentionally never relayed to the child.
		return false, nil
	}
}

func isCtrlKey(key uv.Key, code rune) bool {
	return key.Code == code && key.Mod == uv.ModCtrl
}

func keyAction(key uv.Key) (InputAction, bool) {
	modifiers := modifiers(key.Mod)
	if key.Text != "" && !modifiers.Ctrl && !modifiers.Alt && !modifiers.Meta {
		return TextAction{Text: key.Text}, true
	}
	if name, ok := namedKey(key.Code); ok {
		return KeyAction{Key: name, Modifiers: modifiers}, true
	}
	if key.Code >= 0 && key.Code <= uv.KeyExtended {
		character := string(key.Code)
		if key.Text != "" {
			character = key.Text
		}
		return KeyAction{Key: protocol.Character, Character: character, Modifiers: modifiers}, true
	}
	return nil, false
}

func namedKey(code rune) (protocol.KeyName, bool) {
	keys := map[rune]protocol.KeyName{
		uv.KeyEnter:      protocol.Enter,
		uv.KeyBackspace:  protocol.Backspace,
		uv.KeyEscape:     protocol.Escape,
		uv.KeyTab:        protocol.Tab,
		uv.KeySpace:      protocol.Space,
		uv.KeyUp:         protocol.ArrowUp,
		uv.KeyDown:       protocol.ArrowDown,
		uv.KeyLeft:       protocol.ArrowLeft,
		uv.KeyRight:      protocol.ArrowRight,
		uv.KeyHome:       protocol.Home,
		uv.KeyEnd:        protocol.End,
		uv.KeyInsert:     protocol.Insert,
		uv.KeyDelete:     protocol.Delete,
		uv.KeyPgUp:       protocol.PageUp,
		uv.KeyPgDown:     protocol.PageDown,
		uv.KeyKpEnter:    protocol.KeypadEnter,
		uv.KeyKpEqual:    protocol.KeypadEqual,
		uv.KeyKpMultiply: protocol.KeypadMultiply,
		uv.KeyKpPlus:     protocol.KeypadPlus,
		uv.KeyKpComma:    protocol.KeypadComma,
		uv.KeyKpMinus:    protocol.KeypadMinus,
		uv.KeyKpDecimal:  protocol.KeypadDecimal,
		uv.KeyKpDivide:   protocol.KeypadDivide,
	}
	for offset := rune(0); offset < 12; offset++ {
		keys[uv.KeyF1+offset] = protocol.KeyName(fmt.Sprintf("f%d", offset+1))
	}
	for offset := rune(0); offset < 10; offset++ {
		keys[uv.KeyKp0+offset] = protocol.KeyName(fmt.Sprintf("keypad-%d", offset))
	}
	value, ok := keys[code]
	return value, ok
}

func modifiers(mod uv.KeyMod) protocol.Modifiers {
	return protocol.Modifiers{
		Shift: mod.Contains(uv.ModShift),
		Alt:   mod.Contains(uv.ModAlt),
		Ctrl:  mod.Contains(uv.ModCtrl),
		Meta:  mod.Contains(uv.ModMeta) || mod.Contains(uv.ModSuper),
	}
}

func mouseAction(event uv.MouseEvent) (MouseAction, bool) {
	mouse := event.Mouse()
	button, ok := mouseButton(mouse.Button)
	if !ok || mouse.X < 0 || mouse.Y < 0 || mouse.X >= protocol.MaxColumns || mouse.Y >= protocol.MaxRows {
		return MouseAction{}, false
	}
	kind := protocol.Press
	switch event.(type) {
	case uv.MouseReleaseEvent:
		kind = protocol.Release
	case uv.MouseMotionEvent:
		kind = protocol.Move
	case uv.MouseWheelEvent:
		kind = protocol.Wheel
	}
	return MouseAction{
		Kind:      kind,
		Button:    button,
		Column:    int64(mouse.X),
		Row:       int64(mouse.Y),
		Modifiers: modifiers(mouse.Mod),
	}, true
}

func mouseButton(button uv.MouseButton) (protocol.Button, bool) {
	switch button {
	case uv.MouseNone:
		return protocol.ButtonNone, true
	case uv.MouseLeft:
		return protocol.Left, true
	case uv.MouseMiddle:
		return protocol.Middle, true
	case uv.MouseRight:
		return protocol.Right, true
	case uv.MouseWheelUp:
		return protocol.WheelUp, true
	case uv.MouseWheelDown:
		return protocol.WheelDown, true
	case uv.MouseWheelLeft:
		return protocol.WheelLeft, true
	case uv.MouseWheelRight:
		return protocol.WheelRight, true
	case uv.MouseBackward:
		return protocol.Backward, true
	case uv.MouseForward:
		return protocol.Forward, true
	default:
		return "", false
	}
}
