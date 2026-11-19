package cli

import "strings"

// sidecarRepairHint is appended to any message that means "the llama-server
// binary could not be executed by the OS", which is what a stub, a truncated
// download or a binary for another CPU architecture looks like from Go.
const sidecarRepairHint = "the llama-server at PATH looks corrupt or for another CPU architecture; run `mem init --force`"

// osExecFailureSignatures are the platform error strings that indicate the
// binary itself is unusable, as opposed to being absent or failing to start its
// model.
var osExecFailureSignatures = []string{
	"is not compatible",          // Windows: "This version of %1 is not compatible"
	"not a valid win32",          // Windows
	"%1 is not a valid",          // Windows
	"exec format error",          // unix: wrong architecture
	"bad cpu type",               // macOS: wrong architecture
	"cannot execute binary",      // unix
	"is not a valid application", // macOS
}

// withSidecarHint returns msg unchanged unless it looks like an OS-level
// execution failure of the sidecar binary, in which case it appends the repair
// hint once.
func withSidecarHint(msg string) string {
	if msg == "" || strings.Contains(msg, "mem init --force") {
		return msg
	}
	lower := strings.ToLower(msg)
	for _, sig := range osExecFailureSignatures {
		if strings.Contains(lower, sig) {
			return msg + "; " + sidecarRepairHint
		}
	}
	return msg
}

// withSidecarHintAll applies withSidecarHint to a list, preserving order.
func withSidecarHintAll(msgs []string) []string {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = withSidecarHint(m)
	}
	return out
}
