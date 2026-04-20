// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// InterruptStage distinguishes the first vs. second SIGINT so callers can
// implement "first press → graceful, second press → force exit" without
// re-implementing the counter in every caller.
type InterruptStage int

const (
	// StageFirst is the first SIGINT — caller should start graceful shutdown.
	StageFirst InterruptStage = 1
	// StageSecond is the second SIGINT — caller should force-terminate.
	StageSecond InterruptStage = 2
)

// HandleInterrupts installs a SIGINT watcher. It calls onFirst() on the first
// signal and onSecond() on the second. Returns a cancel func that uninstalls
// the handler and restores default SIGINT disposition.
//
// onFirst and onSecond are invoked from a background goroutine. Callers must
// ensure their bodies are safe to run concurrently with the main thread.
func HandleInterrupts(onFirst, onSecond func()) (cancel func()) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT)

	var (
		wg      sync.WaitGroup
		stopCh  = make(chan struct{})
		stopped sync.Once
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		seen := 0
		for {
			select {
			case <-sigs:
				seen++
				switch seen {
				case int(StageFirst):
					if onFirst != nil {
						onFirst()
					}
				case int(StageSecond):
					if onSecond != nil {
						onSecond()
					}
					return
				}
			case <-stopCh:
				return
			}
		}
	}()

	return func() {
		stopped.Do(func() {
			close(stopCh)
			signal.Stop(sigs)
			wg.Wait()
		})
	}
}
