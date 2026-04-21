// SPDX-License-Identifier: Apache-2.0

package pullprogress

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for concurrent access. The watcher
// writes from its goroutine while the test reads from the main one.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestWatcherReportsGrowingDownload walks the watcher through a real pull
// scenario: files appear in both imagesDir and containerDir over time, and
// the watcher should emit progress lines that reflect that growth. Uses
// the non-TTY path so output is one line per update.
func TestWatcherReportsGrowingDownload(t *testing.T) {
	home := t.TempDir()
	imagesDir := filepath.Join(home, "images")
	containerDir := filepath.Join(home, "containers", "pull-123")
	mustMkdir(t, imagesDir)
	mustMkdir(t, containerDir)

	// Pre-seed images/ to establish a realistic baseline — a previously
	// installed tool's layers. The watcher must not count these.
	mustWrite(t, filepath.Join(imagesDir, "seed.bin"), 512*1024) // 512 KiB

	out := &syncBuffer{}
	notTTY := false

	w := Start(Options{
		Reference:    "docker.io/library/python:3.14-slim",
		ImagesDir:    imagesDir,
		ContainerDir: containerDir,
		Out:          out,
		IsTTY:        &notTTY,
		TickInterval: 30 * time.Millisecond,
	})

	// Simulate a growing download: add 2 MiB of OCI layer, then 3 MiB of
	// rootfs. Sleep long enough for the watcher to pick both up.
	time.Sleep(60 * time.Millisecond)
	mustWrite(t, filepath.Join(imagesDir, "layer1.bin"), 2*1024*1024)
	time.Sleep(60 * time.Millisecond)
	mustWrite(t, filepath.Join(containerDir, "rootfs.ext4"), 3*1024*1024)
	time.Sleep(60 * time.Millisecond)

	w.Stop()

	got := out.String()
	if !strings.Contains(got, "docker.io/library/python:3.14-slim") {
		t.Errorf("output should mention the pulled reference:\n%s", got)
	}
	// At least one line should show the 2 MiB downloaded delta (not the
	// baseline seed, which is pre-pull).
	if !strings.Contains(got, "downloaded 2 MiB") {
		t.Errorf("expected 'downloaded 2 MiB' in output, got:\n%s", got)
	}
	if !strings.Contains(got, "unpacking 3 MiB") {
		t.Errorf("expected 'unpacking 3 MiB' in output, got:\n%s", got)
	}
}

// TestWatcherTTYRewritesInPlace verifies the TTY branch emits carriage
// returns and repaints without newlines between ticks (the user sees the
// line update in place).
func TestWatcherTTYRewritesInPlace(t *testing.T) {
	home := t.TempDir()
	imagesDir := filepath.Join(home, "images")
	containerDir := filepath.Join(home, "containers", "pull-abc")
	mustMkdir(t, imagesDir)
	mustMkdir(t, containerDir)

	out := &syncBuffer{}
	isTTY := true

	w := Start(Options{
		Reference:    "alpine:latest",
		ImagesDir:    imagesDir,
		ContainerDir: containerDir,
		Out:          out,
		IsTTY:        &isTTY,
		TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(40 * time.Millisecond)
	mustWrite(t, filepath.Join(imagesDir, "blob1.bin"), 1024*1024)
	time.Sleep(40 * time.Millisecond)
	w.Stop()

	got := out.String()
	if !strings.Contains(got, "\r") {
		t.Errorf("TTY output should contain \\r for in-place repaint, got: %q", got)
	}
	// Final Stop flushes a newline so later stderr writes start on a fresh line.
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("Stop() should finalise with a newline, got: %q", got)
	}
}

// TestWatcherStopIsIdempotent guards against Stop() being called twice —
// a defer-based caller might double-close by mistake.
func TestWatcherStopIsIdempotent(t *testing.T) {
	home := t.TempDir()
	notTTY := false
	w := Start(Options{
		Reference:    "alpine:latest",
		ImagesDir:    home,
		ContainerDir: home,
		Out:          &syncBuffer{},
		IsTTY:        &notTTY,
		TickInterval: 10 * time.Millisecond,
	})
	w.Stop()
	w.Stop() // must not panic or hang
}

// TestWatcherHandlesMissingDirs: happy path is obvious; the edge case is
// that ImagesDir / ContainerDir may not exist yet at Start (the container
// dir is created by Containerization during the pull). Must not panic or
// emit garbage.
func TestWatcherHandlesMissingDirs(t *testing.T) {
	home := t.TempDir()
	out := &syncBuffer{}
	notTTY := false
	w := Start(Options{
		Reference:    "alpine:latest",
		ImagesDir:    filepath.Join(home, "images-does-not-exist"),
		ContainerDir: filepath.Join(home, "containers", "pull-nope"),
		Out:          out,
		IsTTY:        &notTTY,
		TickInterval: 20 * time.Millisecond,
	})
	time.Sleep(40 * time.Millisecond)
	w.Stop()
	got := out.String()
	if !strings.Contains(got, "alpine:latest") {
		t.Errorf("should still emit initial status line even with missing dirs, got: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2 KiB"},
		{1024 * 1024, "1 MiB"},
		{3 * 1024 * 1024, "3 MiB"},
		{int64(1.5 * 1024 * 1024 * 1024), "1.5 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- helpers ---

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string, size int) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, size)
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
}
