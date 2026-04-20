// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include "silo_bridge.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// Process wraps a libSiloBridge ProcessBox / LinuxProcess (an exec'd process).
type Process struct {
	mu     sync.Mutex
	handle C.SBProcessHandle
}

func newProcess(h C.SBProcessHandle) *Process {
	p := &Process{handle: h}
	runtime.SetFinalizer(p, func(p *Process) { p.Close() })
	return p
}

// Close releases the Swift ProcessBox.
func (p *Process) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle != nil {
		C.sb_free_process(p.handle)
		p.handle = nil
	}
	runtime.SetFinalizer(p, nil)
}

// Start spawns the exec'd process.
func (p *Process) Start() error {
	id, ch := registerCall()
	C.silo_process_start(p.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("process.start: %w", r.err)
	}
	return nil
}

// Wait blocks until the process exits and returns the exit code.
func (p *Process) Wait() (int32, error) {
	id, ch := registerCall()
	C.silo_process_wait(p.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return -1, fmt.Errorf("process.wait: %w", r.err)
	}
	return r.code, nil
}
