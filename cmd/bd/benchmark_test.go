package main

import (
	"io"
	"os/exec"
	"testing"
)

// BenchmarkColdStart measures wall-clock time for the full bd process to
// start up and print version. This captures the real cold-start cost
// including init(), NewRootCmd(), and all cobra registration.
// SLO target: cold-start overhead with no plugins under +50ms vs baseline.
func BenchmarkColdStart(b *testing.B) {
	// Find the bd binary — either built test binary or the system one.
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		b.Skip("bd binary not in PATH; build with 'go build -tags gms_pure_go ./cmd/bd/' first")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(bdPath, "--version")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHelpRendering measures cobra help-text rendering time.
// SLO target: zero degradation vs baseline.
func BenchmarkHelpRendering(b *testing.B) {
	// Use subprocess to avoid cobra global state contamination.
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		b.Skip("bd binary not in PATH")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(bdPath, "--help")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
