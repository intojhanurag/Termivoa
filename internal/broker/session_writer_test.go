package broker

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSendWhenRoomWaitsForTheBudget(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writer := newSessionWriter(server)
	if err := writer.reserve(MaxViewerQueueBytes - 10); err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() { sent <- writer.SendWhenRoom(context.Background(), []byte(`{"x":1}`+strings.Repeat(" ", 100))) }()
	go func() { _, _ = io.Copy(io.Discard, client) }()
	select {
	case err := <-sent:
		t.Fatalf("sent over a full budget: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	writer.release(MaxViewerQueueBytes - 10)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = writer.reserve(MaxViewerQueueBytes)
	if err := writer.SendWhenRoom(ctx, []byte("{}")); err == nil {
		t.Fatal("a full budget must wait until the deadline")
	}
}
