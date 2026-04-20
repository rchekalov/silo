// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include <stdlib.h>
#include "silo_bridge.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// Container wraps a libSiloBridge ContainerBox / LinuxContainer.
type Container struct {
	mu     sync.Mutex
	handle C.SBContainerHandle
}

func newContainer(h C.SBContainerHandle) *Container {
	c := &Container{handle: h}
	runtime.SetFinalizer(c, func(c *Container) { c.Close() })
	return c
}

// Close releases the Swift ContainerBox. Safe to call multiple times.
func (c *Container) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle != nil {
		C.sb_free_container(c.handle)
		c.handle = nil
	}
	runtime.SetFinalizer(c, nil)
}

// Create materialises the container (filesystem, VM settings).
func (c *Container) Create() error {
	id, ch := registerCall()
	C.silo_container_create(c.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("container.create: %w", r.err)
	}
	return nil
}

// Start boots the container.
func (c *Container) Start() error {
	id, ch := registerCall()
	C.silo_container_start(c.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("container.start: %w", r.err)
	}
	return nil
}

// Stop requests a graceful shutdown.
func (c *Container) Stop() error {
	id, ch := registerCall()
	C.silo_container_stop(c.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("container.stop: %w", r.err)
	}
	return nil
}

// Wait blocks until the main container process exits. Returns the exit code.
func (c *Container) Wait() (int32, error) {
	id, ch := registerCall()
	C.silo_container_wait(c.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return -1, fmt.Errorf("container.wait: %w", r.err)
	}
	return r.code, nil
}

// Resize sets the PTY dimensions.
func (c *Container) Resize(cols, rows uint16) error {
	id, ch := registerCall()
	C.silo_container_resize(c.handle, C.uint16_t(cols), C.uint16_t(rows), unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("container.resize: %w", r.err)
	}
	return nil
}

// VMIP returns the container's IPv4 address if known.
func (c *Container) VMIP() string {
	p := C.sb_container_get_vm_ip(c.handle)
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.sb_free_string(p)
	return s
}

// GatewayIP returns the VM's gateway IP if known.
func (c *Container) GatewayIP() string {
	p := C.sb_container_get_gateway_ip(c.handle)
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.sb_free_string(p)
	return s
}

// Exec launches an extra process inside the running container.
func (c *Container) Exec(processID string, cfg ExecConfig) (*Process, error) {
	cpid := C.CString(processID)
	defer C.free(unsafe.Pointer(cpid))

	cconfig, owner := buildExecConfig(cfg)
	defer owner.free()

	id, ch := registerCall()
	C.silo_container_exec(c.handle, cpid, cconfig, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return nil, fmt.Errorf("container.exec: %w", r.err)
	}
	return newProcess(C.SBProcessHandle(r.handle)), nil
}
