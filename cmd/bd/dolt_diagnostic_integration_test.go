//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDiagnostic_StopAndStatusSurfaceMissingPIDDiagnostic is the
// end-to-end integration test for GH#3687: when the PID file is gone
// but the dolt sql-server is still listening on the configured port,
// `bd dolt status` and `bd dolt stop` must surface the
// missing-PID-file diagnostic with a copy-pasteable cleanup hint
// instead of the misleading "not running" message that older builds
// produced.
//
// The test also exercises the `bd dolt killall --force-port <N>`
// escape hatch, which is the only place the codebase converts a
// liveness probe into a kill decision.
func TestDiagnostic_StopAndStatusSurfaceMissingPIDDiagnostic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow integration test in short mode")
	}
	if runtime.GOOS == windowsOS {
		t.Skip("repro uses POSIX kill semantics; Windows path covered by unit tests")
	}
	// The test process itself doesn't need to be in server mode —
	// only the spawned bd binary (which we pass --server to). We
	// gate on isEmbeddedMode() ONLY for tests that share state with
	// the parent; this one runs everything via subprocess and a
	// throwaway tmpDir.
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt binary not in PATH; required for managed-server integration test")
	}

	bdBinary := buildLifecycleTestBinary(t)
	tmpDir := t.TempDir()
	if err := runCommandInDir(tmpDir, "git", "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	_ = runCommandInDir(tmpDir, "git", "config", "user.email", "test@example.com")
	_ = runCommandInDir(tmpDir, "git", "config", "user.name", "Test User")

	env := append(os.Environ(),
		"BEADS_TEST_MODE=",
		"GT_ROOT=",
		"BEADS_DOLT_AUTO_START=",
		"BEADS_DOLT_SERVER_PORT=",
		"BEADS_DOLT_PORT=",
		"BEADS_DOLT_SHARED_SERVER=",
	)

	initOut, err := runBDExecWithBinary(t, bdBinary, tmpDir, env, "init", "--backend", "dolt", "--server", "--prefix", "diag", "--quiet")
	if err != nil {
		lower := strings.ToLower(initOut)
		if strings.Contains(lower, "not supported") || strings.Contains(lower, "not available") {
			t.Skipf("dolt backend not available: %s", initOut)
		}
		t.Fatalf("bd init failed: %v\n%s", err, initOut)
	}

	startOut, err := runBDExecWithBinary(t, bdBinary, tmpDir, env, "dolt", "start")
	if err != nil {
		t.Fatalf("bd dolt start: %v\n%s", err, startOut)
	}

	pidPath := filepath.Join(tmpDir, ".beads", "dolt-server.pid")
	portPath := filepath.Join(tmpDir, ".beads", "dolt-server.port")
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	portBytes, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	port := strings.TrimSpace(string(portBytes))

	// Always clean up the orphan server even if the test fails partway.
	t.Cleanup(func() {
		if proc, perr := os.FindProcess(pid); perr == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	})

	// Simulate GH#3687: PID file vanishes (e.g. accidental rm,
	// crashed wrapper, partial cleanup) while the server keeps
	// running.
	if err := os.Remove(pidPath); err != nil {
		t.Fatalf("remove pid file: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("server died unexpectedly during setup: %v", err)
	}

	// `bd dolt status` should now surface the diagnostic.
	statusOut, _ := runBDExecWithBinary(t, bdBinary, tmpDir, env, "dolt", "status")
	wantStatus := []string{
		"Dolt server: not running",
		"bd dolt killall --force-port " + port,
		"missing or stale",
	}
	for _, want := range wantStatus {
		if !strings.Contains(statusOut, want) {
			t.Fatalf("`bd dolt status` missing %q\n--- output ---\n%s", want, statusOut)
		}
	}

	// `bd dolt stop` should surface the diagnostic AND exit non-zero
	// so callers (e.g. CI scripts) detect the failure to act.
	stopOut, stopErr := runBDExecWithBinary(t, bdBinary, tmpDir, env, "dolt", "stop")
	if stopErr == nil {
		t.Fatalf("`bd dolt stop` returned 0 in GH#3687 case; expected non-zero\n--- output ---\n%s", stopOut)
	}
	if !strings.Contains(stopOut, "bd dolt killall --force-port "+port) {
		t.Fatalf("`bd dolt stop` missing diagnostic\n--- output ---\n%s", stopOut)
	}

	// The `--force-port` escape hatch should clean up the orphan.
	killOut, killErr := runBDExecWithBinary(t, bdBinary, tmpDir, env, "dolt", "killall", "--force-port", port)
	if killErr != nil {
		t.Fatalf("`bd dolt killall --force-port %s`: %v\n%s", port, killErr, killOut)
	}
	if !strings.Contains(killOut, "Killed dolt sql-server on port "+port) {
		t.Fatalf("killall output missing confirmation\n--- output ---\n%s", killOut)
	}

	// Verify the process is actually gone (poll briefly to allow
	// SIGTERM to land).
	if processStillAlive(pid) {
		t.Fatalf("PID %d still alive after killall --force-port", pid)
	}

	// Final status should be a clean "not running" with NO diagnostic.
	finalOut, _ := runBDExecWithBinary(t, bdBinary, tmpDir, env, "dolt", "status")
	if !strings.Contains(finalOut, "not running") {
		t.Fatalf("expected clean 'not running' after killall, got:\n%s", finalOut)
	}
	if strings.Contains(finalOut, "bd dolt killall --force-port") {
		t.Fatalf("diagnostic still appearing after cleanup; orphan not killed\n--- output ---\n%s", finalOut)
	}
}

// processStillAlive returns true if signal-0 succeeds against pid
// after a brief grace period for SIGTERM to land.
func processStillAlive(pid int) bool {
	for range 10 {
		if err := syscall.Kill(pid, 0); err != nil {
			return false
		}
		// 50ms * 10 = 500ms total grace, plenty for a clean
		// dolt sql-server shutdown.
		time.Sleep(50 * time.Millisecond)
	}
	return true
}
