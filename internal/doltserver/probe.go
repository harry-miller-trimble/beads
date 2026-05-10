package doltserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/steveyegge/beads/internal/storage/doltutil"
)

// defaultProbeTimeout is the per-attempt deadline for ProbeListener.
// Kept short so a stuck server doesn't block CLI exit; the probe is on
// the operator-facing path and runs at most once per command invocation.
const defaultProbeTimeout = 2 * time.Second

// ProbeListener does a quick SQL ping against host:port and reports
// whether *something* is responding. It is a LIVENESS check only — it
// does not (and intentionally cannot) prove the listener is dolt.
//
// Behavior:
//   - PingContext succeeds → (true, nil): a MySQL-protocol-speaking
//     listener answered.
//   - Auth, TLS, or protocol-negotiation error → (true, nil): something
//     is listening on the port even if we can't shake hands cleanly.
//     The caller's diagnostic message is correct in either case.
//   - Connection refused / no route / DNS no-such-host → (false, nil):
//     no listener.
//   - Anything else (DSN build error, unexpected I/O failure) →
//     (false, err).
//
// This is the only identity-detection primitive used by the GH#3687
// missing-PID-file diagnostic flow. It deliberately does NOT inspect
// the running process, prove ownership, or attempt port→PID lookup —
// those concerns belong to the optional `bd dolt killall --force-port`
// escape hatch.
func ProbeListener(ctx context.Context, host string, port int) (bool, error) {
	if port <= 0 || port > 65535 {
		return false, fmt.Errorf("invalid port %d", port)
	}

	dsn := doltutil.ServerDSN{
		Host:    host,
		Port:    port,
		User:    "root",
		Timeout: defaultProbeTimeout,
		TLS:     false,
	}.String()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return false, fmt.Errorf("opening probe connection: %w", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		// Caller cancelled — propagate as a real error so it doesn't
		// look like "no listener".
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		// Connection refused / no listener / network unreachable →
		// nothing is there.
		if isNoListenerErr(err) {
			return false, nil
		}
		// Auth, TLS, or protocol error → something IS there.
		// (The DSN above uses passwordless root, which is dolt's
		// default; auth failures here mean we hit a different
		// MySQL-flavored server, which is still "something".)
		return true, nil
	}
	return true, nil
}

// isNoListenerErr reports whether err matches the platform's
// "nothing listening on that port" signature. We classify only
// definite "no listener" errors as `false`; everything else is
// treated as "listener present, identity unverifiable" so the
// diagnostic still fires and the operator gets actionable output.
func isNoListenerErr(err error) bool {
	if err == nil {
		return false
	}
	// MySQL driver wraps net errors in mysql.ErrInvalidConn or
	// returns the underlying *net.OpError directly depending on
	// driver version. Match either.
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		// Connection refused / no route / DNS NXDOMAIN are all
		// terminal "no listener" signals.
		if errors.Is(netErr.Err, syscall.ECONNREFUSED) {
			return true
		}
		// String-match fallback for cross-platform error variants
		// the syscall constant doesn't catch.
		msg := strings.ToLower(netErr.Err.Error())
		switch {
		case strings.Contains(msg, "connection refused"),
			strings.Contains(msg, "no such host"),
			strings.Contains(msg, "network is unreachable"),
			strings.Contains(msg, "no route to host"):
			return true
		}
		return false
	}
	// Some driver versions return a plain net.Error wrapped in
	// the MySQL driver's invalid-connection error. Last-resort
	// string match keeps the function simple without a dependency
	// on private driver types.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "no route to host"):
		return true
	}
	return false
}

// PIDFileMissingDiagnostic builds the multi-line message shown to the
// operator when the PID file is missing/stale but ProbeListener
// reported a listener on the configured port. The message is purely
// informational; the caller decides exit code per command.
//
// pidPath should be the canonical PID-file path the operator would
// inspect (e.g. ~/.beads/shared-server/dolt-server.pid).
func PIDFileMissingDiagnostic(host string, port int, pidPath string) string {
	hostPort := fmt.Sprintf("%s:%d", host, port)

	var b strings.Builder
	fmt.Fprintf(&b, "A dolt sql-server is responding on %s, but bd's PID file\n", hostPort)
	if pidPath != "" {
		fmt.Fprintf(&b, "(%s) is missing or stale.\n\n", expandHomeForDisplay(pidPath))
	} else {
		b.WriteString("is missing or stale.\n\n")
	}
	b.WriteString("bd cannot safely identify or stop this server because it can't prove\n")
	b.WriteString("the running process belongs to bd. To clean up:\n\n")

	if runtime.GOOS == "windows" {
		fmt.Fprintf(&b,
			"    Get-Process -Id (Get-NetTCPConnection -LocalPort %d).OwningProcess | Stop-Process\n",
			port)
		fmt.Fprintf(&b,
			"    bd dolt killall --force-port %d        # equivalent bd command\n\n",
			port)
	} else {
		fmt.Fprintf(&b, "    lsof -ti :%d | xargs kill            # one-liner\n", port)
		fmt.Fprintf(&b, "    bd dolt killall --force-port %d       # equivalent bd command\n\n", port)
	}

	b.WriteString("After the port is free, run `bd dolt start` to restart under bd's\ncontrol.")
	return b.String()
}

// PIDPath returns the canonical PID-file path for the given beads
// directory, accounting for shared-server-mode redirection. Exported
// so the CLI can surface the exact path to operators in diagnostic
// output without re-implementing the resolution rules.
//
// beadsDir may be either a project-local .beads/ path or the
// pre-resolved server dir; both cases are handled idempotently
// because resolveServerDir is a no-op when called on a path that's
// already the shared-server dir.
func PIDPath(beadsDir string) string {
	return pidPath(resolveServerDir(beadsDir))
}

// expandHomeForDisplay returns a path with $HOME replaced by ~ for
// shorter operator-facing display. Best-effort: returns the input
// unchanged if HOME isn't set or the path doesn't have it as prefix.
func expandHomeForDisplay(p string) string {
	home := os.Getenv("HOME")
	if home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

