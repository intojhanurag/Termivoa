// Command launcher is the Phone Terminal.app bundle executable. It runs the
// bundled `pterm launch`, which starts or checks the per-user broker and
// gateway and opens the local page, and shows an alert if that fails.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		alert(err.Error())
	}
	output, err := exec.Command(filepath.Join(filepath.Dir(executable), "pterm"), "launch").CombinedOutput()
	if err != nil {
		alert(strings.TrimSpace(string(output) + "\n" + err.Error()))
	}
}

// alert passes the message as an argument, never as AppleScript source.
func alert(message string) {
	_ = exec.Command("osascript",
		"-e", "on run argv",
		"-e", `display alert "Phone Terminal could not start" message (item 1 of argv) as critical`,
		"-e", "end run", message).Run()
	os.Exit(1)
}
