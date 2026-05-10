package doltserver

import (
	"net"
	"strings"
	"testing"
)

// TestKillPort_NoListenerReturnsNoOp verifies that KillPort is a
// no-op when nothing is listening on the target port. The function
// should NOT return an error here — the operator's intent ("free
// this port") is already satisfied.
func TestKillPort_NoListenerReturnsNoOp(t *testing.T) {
	beadsDir := t.TempDir()
	port := pickFreePort(t)

	res, err := KillPort(beadsDir, "127.0.0.1", port)
	if err != nil {
		t.Fatalf("KillPort returned unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("KillPort returned nil result on empty port")
	}
	if res.Killed {
		t.Errorf("KillPort reported killed=true on empty port")
	}
	if res.PID != 0 {
		t.Errorf("KillPort returned PID %d on empty port; want 0", res.PID)
	}
	if res.Reason != "no listener on port" {
		t.Errorf("unexpected reason %q", res.Reason)
	}
}

// TestKillPort_NonDoltListenerRefused locks in the safety contract:
// KillPort must NEVER kill a non-dolt listener, even when the
// operator explicitly asked for that port. This is the "operator
// typo'd 3306 instead of 3308" footgun guard.
//
// Because the test goroutine itself is the listener, the
// "refuses to kill self" guard may fire before the "not a dolt
// process" guard — either is a valid refusal. The contract being
// asserted is that SOME guard fires and the process is NOT killed,
// not which specific message wins the race.
func TestKillPort_NonDoltListenerRefused(t *testing.T) {
	beadsDir := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go acceptAndDrop(ln)
	port := ln.Addr().(*net.TCPAddr).Port

	res, err := KillPort(beadsDir, "127.0.0.1", port)
	if err == nil {
		t.Fatalf("KillPort accepted non-dolt listener; expected refusal")
	}
	if res != nil && res.Killed {
		t.Errorf("KillPort marked killed=true while returning error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not a dolt sql-server") &&
		!strings.Contains(msg, "refusing to kill the current bd process") {
		t.Errorf("error message missing safety hint (got neither dolt-check nor self-check): %v", err)
	}
}

// TestKillPort_RejectsInvalidPort guards the input validation path.
func TestKillPort_RejectsInvalidPort(t *testing.T) {
	beadsDir := t.TempDir()
	for _, port := range []int{0, -1, 70000} {
		_, err := KillPort(beadsDir, "127.0.0.1", port)
		if err == nil {
			t.Errorf("port %d: expected error, got nil", port)
		}
	}
}
