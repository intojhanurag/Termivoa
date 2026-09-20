package broker

import (
	"errors"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func terminalKey(action InputAction) (uv.Key, error) {
	code, ok := terminalKeyCode(action.Key)
	if !ok {
		return uv.Key{}, fmt.Errorf("broker: unsupported key %q", action.Key)
	}
	key := uv.Key{Code: code, Mod: terminalModifiers(action.Modifiers)}
	if action.Key == protocol.Character {
		if action.Character == nil {
			return uv.Key{}, errors.New("broker: character key is missing character")
		}
		runes := []rune(*action.Character)
		if len(runes) != 1 {
			return uv.Key{}, errors.New("broker: character key must contain one rune")
		}
		key.Code = runes[0]
	}
	return key, nil
}

func terminalKeyCode(name protocol.KeyName) (rune, bool) {
	keys := map[protocol.KeyName]rune{
		protocol.ArrowDown: uv.KeyDown, protocol.ArrowLeft: uv.KeyLeft,
		protocol.ArrowRight: uv.KeyRight, protocol.ArrowUp: uv.KeyUp,
		protocol.Backspace: uv.KeyBackspace, protocol.Delete: uv.KeyDelete,
		protocol.End: uv.KeyEnd, protocol.Enter: uv.KeyEnter,
		protocol.Escape: uv.KeyEscape, protocol.Home: uv.KeyHome,
		protocol.Insert: uv.KeyInsert, protocol.PageDown: uv.KeyPgDown,
		protocol.PageUp: uv.KeyPgUp, protocol.Space: uv.KeySpace,
		protocol.Tab: uv.KeyTab, protocol.Keypad0: uv.KeyKp0,
		protocol.Keypad1: uv.KeyKp1, protocol.Keypad2: uv.KeyKp2,
		protocol.Keypad3: uv.KeyKp3, protocol.Keypad4: uv.KeyKp4,
		protocol.Keypad5: uv.KeyKp5, protocol.Keypad6: uv.KeyKp6,
		protocol.Keypad7: uv.KeyKp7, protocol.Keypad8: uv.KeyKp8,
		protocol.Keypad9: uv.KeyKp9, protocol.KeypadComma: uv.KeyKpComma,
		protocol.KeypadDecimal: uv.KeyKpDecimal, protocol.KeypadDivide: uv.KeyKpDivide,
		protocol.KeypadEnter: uv.KeyKpEnter, protocol.KeypadEqual: uv.KeyKpEqual,
		protocol.KeypadMinus: uv.KeyKpMinus, protocol.KeypadMultiply: uv.KeyKpMultiply,
		protocol.KeypadPlus: uv.KeyKpPlus, protocol.F1: uv.KeyF1,
		protocol.F2: uv.KeyF2, protocol.F3: uv.KeyF3, protocol.F4: uv.KeyF4,
		protocol.F5: uv.KeyF5, protocol.F6: uv.KeyF6, protocol.F7: uv.KeyF7,
		protocol.F8: uv.KeyF8, protocol.F9: uv.KeyF9, protocol.F10: uv.KeyF10,
		protocol.F11: uv.KeyF11, protocol.F12: uv.KeyF12,
		protocol.Character: 0,
	}
	code, ok := keys[name]
	return code, ok
}

func terminalModifiers(modifiers protocol.Modifiers) uv.KeyMod {
	var result uv.KeyMod
	if modifiers.Shift {
		result |= uv.ModShift
	}
	if modifiers.Alt {
		result |= uv.ModAlt
	}
	if modifiers.Ctrl {
		result |= uv.ModCtrl
	}
	if modifiers.Meta {
		result |= uv.ModMeta
	}
	return result
}

func terminalMouse(action InputAction) (uv.MouseEvent, error) {
	button, ok := terminalMouseButton(action.Button)
	if !ok {
		return nil, fmt.Errorf("broker: unsupported mouse button %q", action.Button)
	}
	mouse := uv.Mouse{
		X: int(action.Column), Y: int(action.Row), Button: button,
		Mod: terminalModifiers(action.Modifiers),
	}
	switch action.MouseKind {
	case protocol.Press:
		return uv.MouseClickEvent(mouse), nil
	case protocol.Release:
		return uv.MouseReleaseEvent(mouse), nil
	case protocol.Move:
		return uv.MouseMotionEvent(mouse), nil
	case protocol.Wheel:
		return uv.MouseWheelEvent(mouse), nil
	default:
		return nil, fmt.Errorf("broker: unsupported mouse kind %q", action.MouseKind)
	}
}

func terminalMouseButton(button protocol.Button) (uv.MouseButton, bool) {
	buttons := map[protocol.Button]uv.MouseButton{
		protocol.ButtonNone: uv.MouseNone, protocol.Left: uv.MouseLeft,
		protocol.Middle: uv.MouseMiddle, protocol.Right: uv.MouseRight,
		protocol.WheelUp: uv.MouseWheelUp, protocol.WheelDown: uv.MouseWheelDown,
		protocol.WheelLeft: uv.MouseWheelLeft, protocol.WheelRight: uv.MouseWheelRight,
		protocol.Backward: uv.MouseBackward, protocol.Forward: uv.MouseForward,
	}
	buttonValue, ok := buttons[button]
	return buttonValue, ok
}
