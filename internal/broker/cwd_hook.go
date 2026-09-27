package broker

import (
	"slices"
	"strings"

	"github.com/intojhanurag/phone-terminal/internal/store"
)

// powerShellDirectoryHook wraps the session's existing prompt (after the
// profile loaded it) so each prompt also writes OSC 9;9 with the current
// FileSystem location: ESC ] 9 ; 9 ; "<path>" ESC \. It is invisible on
// screen, edits no profile file and lives only in this session. It avoids
// double quotes and newlines so Windows command-line quoting keeps it one
// argument, and runs the original prompt first so it still sees $?.
const powerShellDirectoryHook = `$global:__ptermPrompt = $function:prompt; ` +
	`function global:prompt { ` +
	`$text = if ($global:__ptermPrompt) { (& $global:__ptermPrompt) -join '' } else { 'PS> ' }; ` +
	`$location = $executionContext.SessionState.Path.CurrentLocation; ` +
	`if ($location.Provider.Name -eq 'FileSystem') { ` +
	`$e = [string][char]27; $q = [string][char]34; ` +
	`$e + ']9;9;' + $q + $location.ProviderPath + $q + $e + '\' + $text ` +
	`} else { $text } }`

// powerShellHookSafeArguments are the only arguments a profile may already
// have for the hook to be added; anything else (-Command, -File, …) could
// conflict, so such a session gets no hook and shows its project name.
var powerShellHookSafeArguments = []string{"-nologo", "-noprofile", "-noexit"}

// withDirectoryHook returns the arguments to start a profile with. Only
// Windows PowerShell 5.1 and PowerShell 7 profiles on Windows get the hook
// (-NoExit -Command <hook>); cmd.exe, WSL and every macOS/Linux shell run
// exactly as configured.
func withDirectoryHook(platform store.Platform, executable string, arguments []string) []string {
	arguments = slices.Clone(arguments)
	name := strings.ToLower(executable[strings.LastIndexAny(executable, `\/`)+1:])
	if platform != store.PlatformWindows || name != "powershell.exe" && name != "pwsh.exe" {
		return arguments
	}
	for _, argument := range arguments {
		if !slices.Contains(powerShellHookSafeArguments, strings.ToLower(argument)) {
			return arguments
		}
	}
	arguments = slices.DeleteFunc(arguments, func(argument string) bool { return strings.EqualFold(argument, "-NoExit") })
	return append(arguments, "-NoExit", "-Command", powerShellDirectoryHook)
}
