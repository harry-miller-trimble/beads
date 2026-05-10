package doltserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestProbeListener_NoListenerReturnsFalse confirms the probe returns
// false (not an error) when nothing is listening on the target port.
// This is the "PID file present but server crashed" case the GH#3687
// diagnostic relies on.
func TestProbeListener_NoListenerReturnsFalse(t *testing.T) {
	port := pickFreePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	alive, err := ProbeListener(ctx, "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ProbeListener returned unexpected error: %v", err)
	}
	if alive {
		t.Fatalf("ProbeListener returned alive=true on free port %d", port)
	}
}

// TestProbeListener_NonMySQLListenerReturnsTrue confirms that ANY
// listener on the target port (not just dolt) makes the probe report
// "alive". This is intentional: identity verification is out of scope
// for the diagnostic, and "something is there but I can't manage it"
// is the right operator-facing message.
func TestProbeListener_NonMySQLListenerReturnsTrue(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept and immediately close — simulates a non-MySQL server
	// that closes the connection before the handshake completes.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	alive, err := ProbeListener(ctx, "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ProbeListener returned unexpected error: %v", err)
	}
	if !alive {
		t.Fatalf("ProbeListener returned alive=false for active TCP listener on port %d", port)
	}
}

// TestProbeListener_InvalidPortRejected guards against caller bugs
// that would silently succeed (or panic) on out-of-range ports.
func TestProbeListener_InvalidPortRejected(t *testing.T) {
	ctx := context.Background()
	for _, port := range []int{0, -1, 70000} {
		alive, err := ProbeListener(ctx, "127.0.0.1", port)
		if err == nil {
			t.Errorf("port %d: expected error, got nil (alive=%v)", port, alive)
		}
		if alive {
			t.Errorf("port %d: expected alive=false on invalid port", port)
		}
	}
}

// TestProbeListener_RespectsContextCancel ensures a long timeout
// can't trap the CLI when the operator interrupts.
func TestProbeListener_RespectsContextCancel(t *testing.T) {
	port := pickFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	start := time.Now()
	alive, err := ProbeListener(ctx, "127.0.0.1", port)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("cancelled probe took %v, expected fast return", elapsed)
	}
	// Either propagated the cancel error or returned false quickly —
	// both are acceptable; what matters is no hang.
	if alive {
		t.Errorf("alive=true on cancelled context (err=%v)", err)
	}
}

// TestPIDFileMissingDiagnostic_ContainsActionableHints verifies the
// diagnostic message includes the host:port, PID path, and a
// platform-appropriate cleanup command.
func TestPIDFileMissingDiagnostic_ContainsActionableHints(t *testing.T) {
	msg := PIDFileMissingDiagnostic("127.0.0.1", 3308, "/home/user/.beads/dolt-server.pid")

	mustContain := []string{
		"127.0.0.1:3308",
		".beads/dolt-server.pid",
		"bd dolt killall --force-port 3308",
	}
	for _, want := range mustContain {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\n--- full message ---\n%s", want, msg)
		}
	}

	// Platform-specific cleanup command.
	if runtime.GOOS == "windows" {
		if !strings.Contains(msg, "Get-NetTCPConnection") {
			t.Errorf("diagnostic missing PowerShell hint on windows: %s", msg)
		}
	} else {
		if !strings.Contains(msg, "lsof -ti :3308") {
			t.Errorf("diagnostic missing lsof hint on unix: %s", msg)
		}
	}
}

// TestPIDFileMissingDiagnostic_HandlesEmptyPidPath ensures the
// diagnostic still renders cleanly when the caller has no path to
// surface (e.g. PID-path resolution itself failed).
func TestPIDFileMissingDiagnostic_HandlesEmptyPidPath(t *testing.T) {
	msg := PIDFileMissingDiagnostic("127.0.0.1", 3308, "")
	if strings.Contains(msg, "()") {
		t.Errorf("diagnostic rendered empty parens: %s", msg)
	}
	if !strings.Contains(msg, "is missing or stale") {
		t.Errorf("diagnostic missing core sentence: %s", msg)
	}
}

// TestPIDFileMissingDiagnostic_ShortensHomePath confirms ~ expansion
// for readability.
func TestPIDFileMissingDiagnostic_ShortensHomePath(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	msg := PIDFileMissingDiagnostic("127.0.0.1", 3308, "/home/me/.beads/dolt-server.pid")
	if !strings.Contains(msg, "~/.beads/dolt-server.pid") {
		t.Errorf("expected ~ expansion in diagnostic, got: %s", msg)
	}
}

// TestIsPortInUseErr_MatchesReclaimMessages locks in the matcher
// against the exact error strings emitted by reclaimPort. If those
// messages change, this test will fail and remind the maintainer to
// update isPortInUseErr in lockstep.
func TestIsPortInUseErr_MatchesReclaimMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"non-dolt holder", fmt.Errorf("port 3308 is in use by a non-dolt process (PID 999)"), true},
		{"other-project dolt", fmt.Errorf("port 3308 is in use by another project's dolt server (PID 999)"), true},
		{"unidentifiable", fmt.Errorf("port 3308 is busy but cannot identify the process."), true},
		{"wrapped", fmt.Errorf("cannot start dolt server: %w", fmt.Errorf("port 3308 is in use by a non-dolt process (PID 1)")), true},
		{"unrelated", fmt.Errorf("dolt binary not found"), false},
		{"port mention only", fmt.Errorf("port closed"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPortInUseErr(tc.err); got != tc.want {
				t.Errorf("isPortInUseErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestAugmentPortInUseError_AppendsDiagnosticWhenListenerPresent
// verifies that EnsureRunning's port-in-use error gets the
// missing-PID-file diagnostic appended when ProbeListener confirms
// something is on the port. Uses a real net.Listener so the probe
// actually has something to talk to.
func TestAugmentPortInUseError_AppendsDiagnosticWhenListenerPresent(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "0")
	beadsDir := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go acceptAndDrop(ln)
	port := ln.Addr().(*net.TCPAddr).Port
	t.Setenv("BEADS_DOLT_SERVER_PORT", fmt.Sprintf("%d", port))

	original := fmt.Errorf("port %d is in use by another project's dolt server (PID 99999)", port)
	got := augmentPortInUseError(beadsDir, original)
	if got == nil {
		t.Fatalf("augmentPortInUseError returned nil")
	}
	if !errors.Is(got, original) {
		t.Errorf("augmented error doesn't wrap original via errors.Is")
	}
	msg := got.Error()
	if !strings.Contains(msg, "bd dolt killall --force-port") {
		t.Errorf("diagnostic missing from augmented error:\n%s", msg)
	}
}

// TestAugmentPortInUseError_NoChangeWhenNoListener verifies the
// transient-conflict path: if the probe says nothing is there, we
// return the original error unchanged so we don't show a misleading
// "kill the listener" hint when there's nothing to kill.
func TestAugmentPortInUseError_NoChangeWhenNoListener(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "0")
	beadsDir := t.TempDir()
	port := pickFreePort(t)
	t.Setenv("BEADS_DOLT_SERVER_PORT", fmt.Sprintf("%d", port))

	original := fmt.Errorf("port %d is in use by a non-dolt process (PID 1)", port)
	got := augmentPortInUseError(beadsDir, original)
	if got == nil || got.Error() != original.Error() {
		t.Errorf("expected unchanged error, got: %v", got)
	}
}

// TestAugmentPortInUseError_PassesThroughUnrelated guards against
// over-augmentation: errors that aren't port-in-use should never
// pick up the diagnostic.
func TestAugmentPortInUseError_PassesThroughUnrelated(t *testing.T) {
	beadsDir := t.TempDir()
	original := fmt.Errorf("dolt binary not found")
	got := augmentPortInUseError(beadsDir, original)
	if got == nil || got.Error() != original.Error() {
		t.Errorf("expected unchanged error, got: %v", got)
	}
}

// acceptAndDrop accepts and immediately closes incoming connections.
// Used by tests to simulate "something on the port that isn't dolt".
func acceptAndDrop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}

// pickFreePort asks the OS for a free TCP port and returns it after
// closing the listener. There is a TOCTOU window before the test uses
// it; flaky-test risk is bounded because the test only relies on
// "nothing listening" being temporarily true.
func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
