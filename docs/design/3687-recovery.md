# Plan: Honest diagnostic for missing/stale dolt PID file (GH#3687)

> **Note**: An earlier iteration of this plan went through 7 rounds of
> council review and grew to ~1100 LOC trying to safely *auto-recover*
> the missing PID file (port→PID lookup, ownership predicate, atomic
> persistence, TOCTOU defenses, etc.). On reflection, the framing was
> wrong: the user's actual pain is that bd lies about server state,
> not that bd refuses to silently fix it. This plan is the
> right-sized fix; the heavyweight design lives in git history if
> we ever need it.

## Problem

In bd's shared-server mode, when the PID file at
`~/.beads/shared-server/dolt-server.pid` is missing, stale, or
unreadable (deleted by user, lost during a bd version upgrade, race
during shutdown, fs cleanup), bd loses the ability to recognize a
`dolt sql-server` it itself spawned:

| Command | Behavior with missing PID file | What user wants |
|---|---|---|
| `bd list` | works (connects via well-known port) | (works) |
| `bd dolt status` | "not running" — **lies** | "running, but PID file missing; here's how to clean up" |
| `bd dolt stop` | "Error: dolt server is not running" — **lies** | clear error + how to clean up |
| `bd dolt killall` | "No orphan dolt servers found" — by design refuses without PID file | (works as designed) |
| `bd dolt start` | "port in use" — true but unhelpful | clear error + how to clean up |

The user's only recovery is `lsof -ti :3308 | xargs kill`.
Reproduction confirmed on `main` at 7fb454cd7.

## Approach: tell the truth, don't auto-fix

The bug is "bd lies about state." The fix is "bd tells the truth."

bd does NOT need to:
- Auto-adopt a running server it can't prove ownership of
- Run platform-specific port→PID lookups
- Walk `/proc` or invoke `lsof` / `KERN_PROCARGS2`
- Atomically rewrite PID files with TOCTOU defenses
- Re-validate process identity before signaling

bd DOES need to:
- Detect "is *something* on the configured port?" via SQL probe
- Tell the user what's happening and what to do
- Provide one optional convenience command to do the cleanup in
  one shot when the operator explicitly wants it

The user is already running `lsof | kill` today. The fix is making
`bd dolt status/stop/start` say so instead of pretending nothing is
there.

## Boundary check

This is a beads-side fix. Same as before: bd invented shared-server
mode and the PID-file convention; recovering from that convention's
edge cases is bd's job, not the dolt driver's. No `dolthub/driver`
changes; no engine introspection.

## Goals

1. `bd dolt status`, `bd dolt stop`, `bd dolt start`, and
   `EnsureRunning` give an accurate, actionable diagnostic when the
   shared-server PID file is missing/stale but a `dolt sql-server`
   is responding on the configured port.
2. One optional escape-hatch command (`bd dolt killall --force-port
   <N>`) for operators who want bd to do the `lsof | kill` work.
3. No silent process adoption. No silent process killing. Bd never
   signals a process it didn't spawn in this invocation unless the
   operator explicitly authorizes it via `--force-port`.

## Non-goals (explicitly NOT this PR)

- Auto-recovery / auto-adoption of running servers
- Auto-rewrite of the missing PID file
- Cross-platform process identity inspection
- 5-signal ownership predicate (exe + cmdline + data-dir + port + UID)
- Atomic temp+rename PID file persistence with `renameat2`/`renamex_np`
- Per-platform port→PID lookup (`/proc`, `lsof`, `Get-NetTCPConnection`)
- `KERN_PROCARGS2` argv retrieval on Darwin
- TOCTOU-safe `StopFromRecovery` revalidation

The earlier 1100-LOC design that included all of the above is
preserved in git history (commit prior to this rewrite). If a future
need arises for safe auto-recovery (e.g., bd is run from a long-lived
daemon that can't tolerate operator intervention), that design is the
starting point.

## Design

Three small additions, ~250 LOC total.

### Layer 1: SQL liveness probe (`internal/doltserver/probe.go`)

```go
// ProbeListener does a quick SQL ping against host:port. Returns
// true if anything responds (a dolt sql-server, another MySQL
// listener, etc.), false on connection-refused / no-route /
// no-listener.
//
// This is a LIVENESS check only. It does not prove the listener is
// dolt; that's intentionally out of scope for this PR. The caller
// uses this signal only to choose between two diagnostic messages
// ("looks like nothing is there" vs "something is there but bd
// can't manage it").
func ProbeListener(ctx context.Context, host string, port int) (bool, error)
```

Implementation:

- `database/sql` + `mysql` driver, hardened DSN (`allowAllFiles=false`,
  `interpolateParams=false`, `tls=false`, all timeouts pinned to 2s).
- `PingContext` with a 2-second timeout.
- Connection-refused / no-route → `(false, nil)`.
- Successful ping OR auth/TLS/protocol error → `(true, nil)` (something
  IS there; we just can't tell what).
- DSN build error / context cancellation → `(false, err)`.

That's the entire identity layer. No `/proc`, no `lsof`, no
`KERN_PROCARGS2`, no ownership predicate.

### Layer 2: Diagnostic helper (`internal/doltserver/diagnostic.go`)

```go
// PIDFileMissingDiagnostic builds the multi-line diagnostic shown
// to the operator when the PID file is missing/stale but the SQL
// probe found something on the configured port.
//
// Includes a copy-pasteable cleanup command appropriate for the
// host OS and a pointer to `bd dolt killall --force-port` for the
// one-shot path.
func PIDFileMissingDiagnostic(host string, port int, pidPath string) string
```

Sample output (POSIX host):

```
A dolt sql-server is responding on 127.0.0.1:3308, but bd's PID file
(/Users/you/.beads/shared-server/dolt-server.pid) is missing or stale.

bd cannot safely identify or stop this server because it can't prove
the running process belongs to bd. To clean up:

    lsof -ti :3308 | xargs kill            # one-liner
    bd dolt killall --force-port 3308       # equivalent bd command

After the port is free, run `bd dolt start` to restart under bd's
control.
```

Windows variant uses
`Get-Process -Id (Get-NetTCPConnection -LocalPort 3308).OwningProcess | Stop-Process`.

### Layer 3: CLI integration (~30 LOC across `cmd/bd/dolt.go`)

When the existing `IsRunning` path returns "not running" in shared-
server mode (or when `Start()` returns a port-in-use class error):

1. `ProbeListener(ctx, host, port)`
2. If true: print `PIDFileMissingDiagnostic` to stderr; choose exit
   code per command:
   - `status`: exit 0 (something IS running; we're just being honest
     that it's not bd-managed)
   - `stop`: exit 1
   - `start`: exit 1
3. If false: existing behavior (truly nothing there).

JSON output mode adds a structured field (additive, doesn't change
existing fields):

```json
{
  "running": false,
  "shared_server_listener_detected": true,
  "diagnostic": "A dolt sql-server is responding on..."
}
```

The remote-host `runExternalDoltStatus` gate is left untouched.

### Layer 4: `EnsureRunning` wrapping (~10 LOC)

When `EnsureRunning` / `EnsureRunningDetailed` / `EnsureRunningGated`
hit their existing port-in-use error path in shared-server mode, run
the same probe + diagnostic and wrap the returned error so background
callers also surface the actionable message instead of bare "port in
use".

`KillStaleServers` is unchanged: still a no-op when no PID file
exists. No new caller; the safety contract is preserved.

### Layer 5 (optional, separate command): `bd dolt killall --force-port <N>`

```go
// In cmd/bd/dolt_killall.go (existing file gets a new flag).
//
// --force-port <N> tells bd to kill whatever process is listening
// on TCP port N, regardless of whether bd has a PID file for it.
// This is the one-shot equivalent of `lsof -ti :N | xargs kill`.
//
// Safety: requires the explicit flag (no defaulting from config).
// Prints a confirmation line: "Killing process(es) listening on
// port N: <pids>" before signaling. Refuses if the port is bound
// only by an LISTEN socket of bd's CURRENT user (POSIX UID check)
// to avoid clobbering another user's server.
```

Implementation:

- POSIX: `exec.Command("lsof", "-ti", ":"+strconv.Itoa(port))`
  argument-array, integer-typed port. Parse PIDs. For each PID,
  validate `/proc/<pid>/status`'s Uid line matches `os.Getuid()`
  (Linux) or `ps -p <pid> -o ruid=` (Darwin). If matches, SIGTERM,
  wait 2s, SIGKILL if needed.
- Windows: PowerShell equivalent.
- If `lsof` / equivalent isn't on PATH: clear error message
  ("install lsof or kill the process manually with PID from
  Get-NetTCPConnection").
- No interface abstractions, no per-platform IdentityResolver
  package — this is one CLI command, ~50 LOC POSIX + ~30 Windows.

This is the only place we touch process inspection at all, and only
when the operator passes `--force-port`. Risk surface is contained
to this command's flag-gated path.

## Test plan (~110 LOC)

Unit tests:

- `TestProbeListener_NoListener`: nothing on the port → `(false, nil)`.
- `TestProbeListener_LiveDolt`: spawn a real dolt sql-server on an
  ephemeral port → `(true, nil)`. Skip if `dolt` not on PATH.
- `TestProbeListener_NonDoltMySQL`: stub a hand-rolled listener that
  closes immediately → `(true, nil)` (auth/protocol error class is
  still "something there").
- `TestPIDFileMissingDiagnostic`: golden output matches per OS.

Integration tests (`cmd/bd/dolt_diagnostic_test.go`, build-tagged
`//go:build integration && !windows`):

- `TestBdDoltStatus_PIDFileMissingDiagnostic`: shared-server fixture,
  start, delete PID file, run `bd dolt status` → exit 0, stderr
  contains "responding on port", "PID file is missing".
- `TestBdDoltStop_PIDFileMissingDiagnostic`: same setup, `bd dolt
  stop` → exit 1, same diagnostic text.
- `TestBdDoltStart_PIDFileMissingDiagnostic`: same setup, `bd dolt
  start` → exit 1, diagnostic preferred over default "port in use".
- `TestBdDoltKillallForcePort`: same setup, `bd dolt killall
  --force-port <N>` → exit 0, port becomes free within 3s, server
  is killed.
- `TestBdDoltKillallForcePort_RefusesOtherUserPort`: skip unless test
  runner can spawn a listener as a different user; assert the UID
  check refuses.
- `TestKillStaleServers_NoPIDFileStillNoOp`: regression preservation.

`scripts/repro-3687.sh`: automates the manual repro (init shared-
server fixture, start, delete PID file, run `bd dolt status` —
expects the new diagnostic post-fix, the false "not running" pre-
fix). Exits 0 on post-fix expected behavior, 1 otherwise.

## Acceptance criteria

In the missing-PID-file scenario (shared-server, server running):

- [ ] `bd dolt status` exits 0; stderr contains the diagnostic; JSON
      includes `"shared_server_listener_detected": true` and
      `"diagnostic": ...`.
- [ ] `bd dolt stop` exits 1; stderr contains the diagnostic; the
      server is NOT signaled (verified by PID still alive after the
      command returns).
- [ ] `bd dolt start` exits 1; stderr contains the diagnostic
      (preferred over the default port-in-use message).
- [ ] `bd dolt killall --force-port <N>` exits 0; port is free
      within 3s; running process was actually signaled.
- [ ] `bd dolt killall` (without `--force-port`) is unchanged: still
      a no-op when no PID file exists.
- [ ] `EnsureRunning` and siblings, in shared-server mode hitting
      port-in-use, return a wrapped error containing the diagnostic.
- [ ] `scripts/repro-3687.sh` passes post-fix.
- [ ] No new external dependencies. No `dolthub/driver` changes.
- [ ] All existing tests pass.

## CHANGELOG

Under `[Unreleased]` → `### Fixed`:

> `bd dolt status`, `bd dolt stop`, and `bd dolt start` now surface
> an accurate diagnostic when the dolt sql-server PID file is
> missing or stale but a server is still responding on the
> configured port (a state previously misreported as "not running").
> A new `bd dolt killall --force-port <N>` flag provides a one-shot
> equivalent of `lsof -ti :N | xargs kill` for operators who want
> bd to do the cleanup. Primarily affects shared-server mode.
> (GH#3687)

## Estimated effort

- Implementation: ~150 LOC (probe + diagnostic + CLI wiring +
  `--force-port`)
- Tests: ~110 LOC
- Docs/CHANGELOG: minor

Total: **~260 LOC** vs ~1100 LOC in the auto-recovery design.

## What we give up vs the auto-recovery design

| Capability | Auto-recovery (~1100 LOC) | Honest diagnostic (~260 LOC) |
|---|---|---|
| Missing PID file → bd self-heals | yes | no — operator runs one command |
| `bd dolt stop` works without operator action | yes | no — exit 1 with instructions |
| `bd dolt start` adopts existing server | yes | no — exit 1 with instructions |
| Internal `EnsureRunning` self-heals | yes | no — surfaces actionable error |
| Multi-platform port→PID lookup | yes | only inside `--force-port` |
| Process identity inspection | yes | only inside `--force-port`, UID-only |

The honest diagnostic costs the operator one extra command
(`bd dolt killall --force-port <N>`, or the equivalent `lsof | kill`)
when this rare state occurs. The auto-recovery saves them that
command at the cost of ~840 LOC of platform-specific process
inspection, atomic file persistence, and TOCTOU race defenses that
have to be maintained forever.

For a bug that the upstream report describes as "annoying, had to
manually kill," the operator-action tradeoff is acceptable and the
LOC savings + simpler security surface are clearly worth it.
