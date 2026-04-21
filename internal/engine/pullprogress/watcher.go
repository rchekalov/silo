// SPDX-License-Identifier: Apache-2.0

// Package pullprogress shows "something is happening" feedback during a
// long OCI image pull by tailing the filesystem. Apple Containerization
// doesn't currently expose a progress hook through our bridge, so instead
// we observe the two places bytes land:
//
//   - ~/.silo/images/       — OCI blobs (content-addressable storage)
//   - ~/.silo/containers/<pull-id>/rootfs.ext4 — unpack destination
//
// We compute a baseline for the shared images dir before the pull and
// report only the delta, so a user reinstalling python (layers already
// cached) won't see the progress climb past what they're actually
// downloading.
//
// Sparse files matter: the rootfs.ext4 is created as a 2 GiB sparse file
// from byte zero, so FileInfo.Size() lies about disk usage. We use
// Stat_t.Blocks * 512 for the real figure.
//
// This is deliberately approximate. We don't know the expected total, so
// no percentage or ETA. See silo-private/docs/pull-progress-bridge.md
// for the proper Swift-bridge design that replaces this once users ask
// for a real progress bar.
package pullprogress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

// Options configures a progress watcher.
type Options struct {
	// Reference is the image being pulled, e.g. "docker.io/library/python:3.14-slim".
	// Used only for the status line.
	Reference string
	// ImagesDir is the shared OCI blob store (~/.silo/images). Delta against
	// baseline measured at Start().
	ImagesDir string
	// ContainerDir is the container-specific dir (~/.silo/containers/<id>).
	// Disk usage reported absolutely.
	ContainerDir string
	// Out is where progress lines are written. Typically os.Stderr.
	Out io.Writer
	// IsTTY controls whether we redraw in place with \r (true) or print
	// a new line on each update (false). If nil, auto-detects based on Out.
	IsTTY *bool
	// TickInterval controls how often we sample the filesystem. Zero uses
	// the default (500ms TTY, 5s non-TTY).
	TickInterval time.Duration
}

// Watcher is a running progress goroutine. Stop blocks until the goroutine
// exits and the final line is flushed.
type Watcher struct {
	stop    chan struct{}
	done    chan struct{}
	lastLen atomic.Int32
}

// Start kicks off a watcher. The caller must call Stop() before the
// process exits; `defer w.Stop()` is the typical idiom.
func Start(opts Options) *Watcher {
	w := &Watcher{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go w.run(opts)
	return w
}

// Stop signals the watcher to exit and waits for it to finalise the
// progress line.
func (w *Watcher) Stop() {
	select {
	case <-w.stop:
		// already stopped
	default:
		close(w.stop)
	}
	<-w.done
}

func (w *Watcher) run(opts Options) {
	defer close(w.done)

	isTTY := resolveTTY(opts)
	interval := opts.TickInterval
	if interval <= 0 {
		if isTTY {
			interval = 500 * time.Millisecond
		} else {
			interval = 5 * time.Second
		}
	}

	// Baseline so we only report bytes this pull added to images/.
	baseline := duBytes(opts.ImagesDir)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Emit one line immediately so the user sees activity even if the pull
	// finishes before the first tick.
	w.emit(opts, isTTY, baseline, 0, 0, true)

	var lastImages, lastContainer int64
	for {
		select {
		case <-w.stop:
			// Final flush: newline after the \r-rewritten line so subsequent
			// output starts on a fresh row.
			if isTTY {
				fmt.Fprintln(opts.Out)
			}
			return
		case <-ticker.C:
			imgNow := duBytes(opts.ImagesDir)
			ctrNow := duBytes(opts.ContainerDir)
			// On non-TTY only print when bytes moved, to keep logs tidy.
			if !isTTY && imgNow == lastImages && ctrNow == lastContainer {
				continue
			}
			lastImages, lastContainer = imgNow, ctrNow
			w.emit(opts, isTTY, baseline, imgNow, ctrNow, false)
		}
	}
}

// emit formats and writes one progress line. imgBase is the baseline of
// imagesDir captured before the pull.
func (w *Watcher) emit(opts Options, isTTY bool, imgBase, imgNow, ctrNow int64, first bool) {
	if first && imgBase == 0 {
		// First line at baseline: no numbers yet, just show intent.
		line := fmt.Sprintf("Pulling %s...", opts.Reference)
		w.write(opts.Out, isTTY, line)
		return
	}
	imgDelta := imgNow - imgBase
	if imgDelta < 0 {
		imgDelta = 0
	}
	line := fmt.Sprintf(
		"Pulling %s… downloaded %s, unpacking %s",
		opts.Reference, humanBytes(imgDelta), humanBytes(ctrNow),
	)
	w.write(opts.Out, isTTY, line)
}

func (w *Watcher) write(out io.Writer, isTTY bool, line string) {
	if isTTY {
		// \r + pad to cover any previous longer line.
		prev := int(w.lastLen.Load())
		pad := prev - len(line)
		if pad > 0 {
			fmt.Fprintf(out, "\r%s%s", line, spaces(pad))
		} else {
			fmt.Fprintf(out, "\r%s", line)
		}
		w.lastLen.Store(int32(len(line)))
		return
	}
	fmt.Fprintln(out, line)
}

// duBytes returns the total on-disk bytes of everything under root,
// accounting for sparse files via Stat_t.Blocks. Returns 0 if root is
// missing. Errors mid-walk are silently skipped — this is a UX helper,
// not a correctness-critical path.
func duBytes(root string) int64 {
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Permission denied, path vanished mid-walk, etc.: skip.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			total += int64(sys.Blocks) * 512
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func resolveTTY(opts Options) bool {
	if opts.IsTTY != nil {
		return *opts.IsTTY
	}
	// The runtime-detection branch lives in term_detect.go so tests can
	// force a value via Options.IsTTY without dragging in x/term.
	return isTerminal(opts.Out)
}

// isTerminal is overridden in tests via the exported variable below so we
// don't need a real TTY to exercise the TTY branch.
var isTerminal = defaultIsTerminal

func humanBytes(n int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.0f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// spaces returns n ASCII spaces. Used for line-padding on TTY repaints.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = ' '
	}
	return string(buf)
}

