// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReapStaleContainers(t *testing.T) {
	dir := t.TempDir()

	// Old silo- dir: should be reaped.
	old := filepath.Join(dir, "silo-old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	backdated := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	// Recent silo- dir: should be kept.
	recent := filepath.Join(dir, "silo-recent")
	if err := os.MkdirAll(recent, 0o755); err != nil {
		t.Fatal(err)
	}

	// Non-silo dir: should be ignored even if old.
	outside := filepath.Join(dir, "other")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(outside, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	if err := reapStaleContainers(dir, 30*time.Minute); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expected old silo- dir to be reaped, got err=%v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent dir should be kept: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("non-silo dir should be untouched: %v", err)
	}
}

func TestReapStaleContainersMissingDirIsNoop(t *testing.T) {
	if err := reapStaleContainers(filepath.Join(t.TempDir(), "nope"), time.Minute); err != nil {
		t.Fatal(err)
	}
}
