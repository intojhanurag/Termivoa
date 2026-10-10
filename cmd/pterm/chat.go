package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/transcript"
)

// chatConfig is the production Chat view folder (docs/chat-view.md): Claude
// Code's ~/.claude. Nil on Windows and without a home: Chat is unsupported.
func chatConfig() *transcript.Config {
	home, err := os.UserHomeDir()
	if runtime.GOOS == "windows" || err != nil || !filepath.IsAbs(home) {
		return nil
	}
	return &transcript.Config{
		Claude:     filepath.Join(home, ".claude"),
		CustomRoot: os.Getenv("CLAUDE_CONFIG_DIR") != "",
		Dir:        ptyhost.ProcessDirectory,
	}
}

const chatOnPrompt = `Chat view lets every paired phone read the coding agent's own transcript
of a session while it has Chat open: your prompts, the agent's answers, tool
calls with short output previews and edit hunks. That can include files the
agent read outside the project and secrets that were pasted (a fixed mask
hides common key shapes, but not all). Nothing is stored; any phone can turn
it off in Settings.

Turn Chat view on for this computer? [y/N] `

// runChat is pterm chat on|off|status. Only this command turns Chat on, and
// only after a y/N answer at the computer (review S1).
func runChat(ctx context.Context, client *localclient.AdminClient, action string, input *os.File, output io.Writer) error {
	var (
		status protocol.AdminRPCChatStatusPayload
		err    error
	)
	switch action {
	case "chat on":
		if err := confirmLifecycle(ctx, input, output, chatOnPrompt); err != nil {
			return err
		}
		status, err = client.ChatSet(ctx, true)
	case "chat off":
		status, err = client.ChatSet(ctx, false)
	default:
		status, err = client.ChatStatus(ctx)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, chatLine(status))
	return err
}

func chatLine(status protocol.AdminRPCChatStatusPayload) string {
	switch {
	case !status.Supported:
		return "Chat view: not supported on this computer"
	case !status.Enabled:
		return "Chat view: off (turn it on with pterm chat on)"
	}
	line := fmt.Sprintf("Chat view: on, %d of 8 chats open", status.Tails)
	if len(status.Versions) > 0 {
		line += fmt.Sprintf(", Claude Code %s seen, %d%% of lines unknown", strings.Join(status.Versions, ", "), status.UnknownPercent)
	}
	return line
}

// inspectChat prints the Chat line of pterm doctor; an unknown share over
// 20% is a warning (the format changed). It never counts a problem.
func inspectChat(ctx context.Context, env lifecycleEnv, report hostReport, output io.Writer) {
	if !report.broker || report.listErr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout+2*time.Second)
	defer cancel()
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		return
	}
	defer admin.Close()
	status, err := admin.ChatStatus(ctx)
	if err != nil {
		return
	}
	if status.UnknownPercent > 20 {
		fmt.Fprintf(output, "  ! %s; the agent's transcript format changed, so Chat shows the terminal instead\n", chatLine(status))
		return
	}
	fmt.Fprintf(output, "  %s\n", chatLine(status))
}
