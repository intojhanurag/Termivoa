package ptyhost

import (
	"bufio"
	"bytes"
)

// parseLsofDirectory returns the name of the first "cwd" file in
// `lsof -a -d cwd -p <pid> -Fn` output ("p<pid>", "fcwd", "n<path>" lines),
// or "" when there is none. Validation is the caller's job.
func parseLsofDirectory(output []byte) string {
	cwd := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
		case line[0] == 'f':
			cwd = line == "fcwd"
		case line[0] == 'n' && cwd:
			return line[1:]
		}
	}
	return ""
}
