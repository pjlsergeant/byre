package main

import (
	"bytes"
	"strings"
	"testing"
)

// The whole reason byre recovers at all: Go's runtime exits a panic with 2,
// which is byre's USAGE code, so an unhandled crash reads to a script as "you
// typed the flags wrong". DELIVER.md promises script-trustworthy exit codes
// and PRINCIPLES.md lists them among the contracts byre owns, so the crash
// code must not collide with any of them.
func TestExitPanicCollidesWithNoReservedCode(t *testing.T) {
	for _, reserved := range []struct {
		code int
		what string
	}{
		{0, "success"},
		{1, "byre failure (fatal)"},
		{2, "usage error"},
		{3, "ExitRefused"},
	} {
		if exitPanic == reserved.code {
			t.Fatalf("exitPanic = %d, which is already %s", exitPanic, reserved.what)
		}
	}
}

func TestPanicReportNamesByreAndKeepsTheStack(t *testing.T) {
	var b bytes.Buffer
	stack := []byte("goroutine 1 [running]:\nmain.thing(...)\n\t/workspace/cmd/byre/main.go:42\n")
	panicReport(&b, "nil map write", stack)

	got := b.String()
	// It must say whose bug this is -- the user did nothing wrong, and that
	// is the only thing the report adds over a bare panic.
	if !strings.Contains(got, "bug in byre") {
		t.Errorf("report does not attribute the crash to byre:\n%s", got)
	}
	// The panic value and the stack ride through untouched: hiding either
	// would trade a real bug report for a friendly message.
	if !strings.Contains(got, "nil map write") {
		t.Errorf("report drops the panic value:\n%s", got)
	}
	if !strings.Contains(got, string(stack)) {
		t.Errorf("report does not reproduce the stack verbatim:\n%s", got)
	}
}
