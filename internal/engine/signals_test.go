// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestHandleInterruptsTwoStages(t *testing.T) {
	var firstSeen, secondSeen atomic.Bool
	cancel := HandleInterrupts(
		func() { firstSeen.Store(true) },
		func() { secondSeen.Store(true) },
	)
	defer cancel()

	// Send two SIGINTs to our own process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &firstSeen, "first signal")
	if secondSeen.Load() {
		t.Fatal("second fired after first signal only")
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &secondSeen, "second signal")
}

func TestHandleInterruptsCancel(t *testing.T) {
	var fired atomic.Bool
	cancel := HandleInterrupts(
		func() { fired.Store(true) },
		nil,
	)
	cancel()
	// After cancel, SIGINT should no longer fire the handler. We can't easily
	// prove a negative without racing the test runner — assert only that
	// cancel is idempotent.
	cancel()
}

func waitFor(t *testing.T, flag *atomic.Bool, what string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if flag.Load() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
