//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestManagementCommandsCancelSaturatedOutput(t *testing.T) {
	for _, command := range []string{"list", "terminate"} {
		t.Run(command, func(t *testing.T) {
			runtimeDir, client := startCommandBroker(t)
			args := []string{command, "--dev", "--runtime-dir", runtimeDir}
			var input *os.File
			var ref localclient.SessionRef
			if command == "terminate" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				operationID := cli.NewOperationID()
				created, err := client.Create(ctx, operationID, commandProjectID, commandProfileID)
				if err != nil {
					t.Fatal(err)
				}
				ref = localclient.SessionRef{ID: created.Session.ID, Generation: created.Session.Generation}
				args = append(args, ref.ID)
				input = confirmationPipe(t, "yes\n")
			}
			reader, output, filled := saturatedManagementPipe(t)
			beforeFlags := managementOutputFlags(t, output)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- runWithInput(ctx, args, input, output) }()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("canceled command returned %v", err)
				}
			case <-time.After(time.Second):
				// Release and join even when running against the old blocking
				// implementation, rather than leaking the regression goroutine.
				_ = reader.Close()
				select {
				case <-result:
				case <-time.After(time.Second):
					t.Fatal("command failed to join after releasing output")
				}
				t.Fatal("command cancellation remained blocked on full output pipe")
			}
			if flags := managementOutputFlags(t, output); flags != beforeFlags {
				t.Fatalf("caller output flags changed: %#x -> %#x", beforeFlags, flags)
			}
			if input != nil {
				var remaining [4]byte
				if _, err := io.ReadFull(input, remaining[:]); err != nil || string(remaining[:]) != "yes\n" {
					t.Fatalf("blocked prompt consumed confirmation: %q, %v", remaining, err)
				}
				statusCtx, statusCancel := context.WithTimeout(context.Background(), time.Second)
				defer statusCancel()
				status, err := client.Status(statusCtx, ref)
				if err != nil || status.State != protocol.Running {
					t.Fatalf("blocked prompt terminated session: %#v, %v", status, err)
				}
			}
			if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.CopyN(io.Discard, reader, int64(filled)); err != nil {
				t.Fatal(err)
			}
			if _, err := output.Write([]byte("open")); err != nil {
				t.Fatalf("caller output was closed: %v", err)
			}
			var marker [4]byte
			if _, err := io.ReadFull(reader, marker[:]); err != nil || string(marker[:]) != "open" {
				t.Fatalf("caller output marker = %q, %v", marker, err)
			}
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
			if trailing, err := io.ReadAll(reader); err != nil || len(trailing) != 0 {
				t.Fatalf("output duplicate leaked or wrote late: %q, %v", trailing, err)
			}
		})
	}
}

func saturatedManagementPipe(t *testing.T) (*os.File, *os.File, int) {
	t.Helper()
	reader, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = output.Close() })
	raw, err := output.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	filled := 0
	var fillErr error
	if err := raw.Control(func(fd uintptr) {
		// os.Pipe starts nonblocking internally. Do not call Fd or change
		// caller flags: fill the real pipe without entering Go's poller.
		if flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0); err != nil || flags&unix.O_NONBLOCK == 0 {
			fillErr = errors.New("fixture output pipe is not nonblocking")
			return
		}
		for _, chunk := range [][]byte{make([]byte, 1024), {0}} {
			for filled < 1<<20 {
				n, err := unix.Write(int(fd), chunk)
				if n > 0 {
					filled += n
				}
				if errors.Is(err, unix.EAGAIN) {
					break
				}
				if err != nil || n == 0 {
					fillErr = errors.Join(err, io.ErrShortWrite)
					return
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if fillErr != nil || filled == 0 || filled >= 1<<20 {
		t.Fatalf("could not saturate fixture pipe: %d bytes, %v", filled, fillErr)
	}
	return reader, output, filled
}

func managementOutputFlags(t *testing.T, file *os.File) int {
	t.Helper()
	raw, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var flagErr error
	if err := raw.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
		t.Fatal(err)
	}
	if flagErr != nil {
		t.Fatal(flagErr)
	}
	return flags
}
