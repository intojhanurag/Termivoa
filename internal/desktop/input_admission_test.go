package desktop

import (
	"context"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

type admittedRecorder struct {
	recordingTransport
	epoch uint64
}

func (transport *admittedRecorder) AdmissionEpoch() uint64 { return transport.epoch }
func (transport *admittedRecorder) SendAdmitted(ctx context.Context, action InputAction, epoch uint64) error {
	if epoch == 0 || epoch != transport.epoch {
		return nil
	}
	return transport.Send(ctx, action)
}

func TestInputAdmissionRejectsQueuedEventsAndExpiredQuote(t *testing.T) {
	ctx := context.Background()
	transport := &admittedRecorder{epoch: 1}
	controller := InputController{}
	_, err := controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}, Epoch: 1}, transport)
	if err != nil {
		t.Fatal(err)
	}
	transport.epoch = 3 // Ready -> Syncing -> Ready, same lease but new epoch.
	_, err = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: 'x', Text: "stale"}, Epoch: 1}, transport)
	if err != nil {
		t.Fatal(err)
	}
	detached, err := controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}, Epoch: 3}, transport)
	if err != nil || !detached || len(transport.actions) != 0 {
		t.Fatalf("expired quote/queued input crossed epoch: detach=%v actions=%#v err=%v", detached, transport.actions, err)
	}
	transport.epoch = 0
	_, _ = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl}}, transport)
	detached, err = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: ']', Mod: uv.ModCtrl}}, transport)
	if !detached || err != nil {
		t.Fatalf("disabled input lost local detach: %v %v", detached, err)
	}
}

func TestInputAdmissionRejectsPasteAcrossReadinessCycle(t *testing.T) {
	transport := &admittedRecorder{epoch: 1}
	controller := InputController{}
	ctx := context.Background()
	_, _ = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.PasteStartEvent{}, Epoch: 1}, transport)
	transport.epoch = 3
	_, _ = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.PasteEvent{Content: "old paste"}, Epoch: 1}, transport)
	_, _ = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.PasteEndEvent{}, Epoch: 1}, transport)
	_, _ = controller.Handle(ctx, uv.InputEpochEvent{Event: uv.KeyPressEvent{Code: 'z', Text: "z"}, Epoch: 3}, transport)
	if len(transport.actions) != 1 || transport.actions[0] != (TextAction{Text: "z"}) {
		t.Fatalf("stale paste or lost fresh input: %#v", transport.actions)
	}
}
