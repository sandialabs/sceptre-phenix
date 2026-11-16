package soh

import "phenix/util/mm"

// errUnsafeC2Arg tells that a check value cannot reach the VM unchanged
// through `cc exec`, so the check is not sent.
var errUnsafeC2Arg = mm.ErrUnsafeC2Arg

// ccExecCommand joins args into the command text for `cc exec`, as
// [mm.CCExecCommand] does.
func ccExecCommand(args ...string) (string, error) {
	return mm.CCExecCommand(args...) //nolint:wrapcheck // the error says which value is unsafe
}

const alnumChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// psQuote quotes s as a PowerShell single-quoted string, as
// [mm.PowerShellQuote] does.
func psQuote(s string) string {
	return mm.PowerShellQuote(s)
}

// listenerPunct are the characters other than letters and digits allowed in
// each part of a listener value. The ss filter language has no quotes, so
// other characters are refused.
const listenerPunct = "-_.%*/"
