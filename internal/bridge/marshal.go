// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include <stdlib.h>
#include <string.h>
#include "silo_bridge.h"
*/
import "C"

import (
	"unsafe"
)

// cMemory tracks every C allocation made while building a C-side struct tree so
// we can free them all in one call after the Swift function returns (or
// after the async callback fires and we've copied the handle).
type cMemory struct {
	ptrs []unsafe.Pointer
}

func (m *cMemory) cString(s string) *C.char {
	p := C.CString(s)
	m.ptrs = append(m.ptrs, unsafe.Pointer(p))
	return p
}

// cStringArray allocates a NULL-terminated `const char* const*` array.
// Returns nil for an empty input.
func (m *cMemory) cStringArray(strs []string) **C.char {
	if len(strs) == 0 {
		return nil
	}
	size := C.size_t(unsafe.Sizeof(uintptr(0))) * C.size_t(len(strs)+1)
	arr := C.malloc(size)
	m.ptrs = append(m.ptrs, arr)
	// Write each element.
	elemSize := unsafe.Sizeof(uintptr(0))
	for i, s := range strs {
		slot := unsafe.Add(arr, uintptr(i)*elemSize)
		*(**C.char)(slot) = m.cString(s)
	}
	// NULL terminator.
	terminator := unsafe.Add(arr, uintptr(len(strs))*elemSize)
	*(**C.char)(terminator) = nil
	return (**C.char)(arr)
}

// cMount writes a single SBMount into out (must be valid C memory).
func (m *cMemory) cMount(spec MountSpec, out *C.SBMount) {
	out.source = m.cString(spec.Source)
	out.destination = m.cString(spec.Destination)
	out._type = m.cString(spec.Type)
	if spec.Format != "" {
		out.format = m.cString(spec.Format)
	} else {
		out.format = nil
	}
	out.options = (**C.char)(unsafe.Pointer(m.cStringArray(spec.Options)))
}

// cMountArray allocates an SBMount[count] array and populates it.
func (m *cMemory) cMountArray(specs []MountSpec) (*C.SBMount, C.uint32_t) {
	if len(specs) == 0 {
		return nil, 0
	}
	size := C.size_t(unsafe.Sizeof(C.SBMount{})) * C.size_t(len(specs))
	arr := C.malloc(size)
	m.ptrs = append(m.ptrs, arr)
	C.memset(arr, 0, size)
	elemSize := unsafe.Sizeof(C.SBMount{})
	for i, s := range specs {
		slot := (*C.SBMount)(unsafe.Add(arr, uintptr(i)*elemSize))
		m.cMount(s, slot)
	}
	return (*C.SBMount)(arr), C.uint32_t(len(specs))
}

// cHostEntryArray allocates an SBHostEntry[count] array.
func (m *cMemory) cHostEntryArray(entries []HostEntry) (*C.SBHostEntry, C.uint32_t) {
	if len(entries) == 0 {
		return nil, 0
	}
	size := C.size_t(unsafe.Sizeof(C.SBHostEntry{})) * C.size_t(len(entries))
	arr := C.malloc(size)
	m.ptrs = append(m.ptrs, arr)
	C.memset(arr, 0, size)
	elemSize := unsafe.Sizeof(C.SBHostEntry{})
	for i, e := range entries {
		slot := (*C.SBHostEntry)(unsafe.Add(arr, uintptr(i)*elemSize))
		slot.ip_address = m.cString(e.IPAddress)
		slot.hostnames = (**C.char)(unsafe.Pointer(m.cStringArray(e.Hostnames)))
	}
	return (*C.SBHostEntry)(arr), C.uint32_t(len(entries))
}

// free releases every allocation tracked on m. Safe to call multiple times.
func (m *cMemory) free() {
	for _, p := range m.ptrs {
		C.free(p)
	}
	m.ptrs = nil
}

// buildContainerConfig allocates an SBContainerConfig in C memory and populates it.
// The returned cMemory must be freed after the Swift call completes. Use the
// cleanup arg via defer.
func buildContainerConfig(cfg ContainerConfig) (*C.SBContainerConfig, *cMemory) {
	m := &cMemory{}
	out := (*C.SBContainerConfig)(C.malloc(C.size_t(unsafe.Sizeof(C.SBContainerConfig{}))))
	m.ptrs = append(m.ptrs, unsafe.Pointer(out))
	C.memset(unsafe.Pointer(out), 0, C.size_t(unsafe.Sizeof(C.SBContainerConfig{})))
	out.cpus = C.int32_t(cfg.CPUs)
	out.memory_bytes = C.uint64_t(cfg.MemoryBytes)
	out.arguments = (**C.char)(unsafe.Pointer(m.cStringArray(cfg.Arguments)))
	if cfg.WorkingDirectory != "" {
		out.working_directory = m.cString(cfg.WorkingDirectory)
	}
	out.env_vars = (**C.char)(unsafe.Pointer(m.cStringArray(cfg.EnvVars)))
	mountsPtr, mountCount := m.cMountArray(cfg.Mounts)
	out.mounts = mountsPtr
	out.mount_count = mountCount
	out.stdin_fd = C.int32_t(cfg.StdinFD)
	out.stdout_fd = C.int32_t(cfg.StdoutFD)
	out.stderr_fd = C.int32_t(cfg.StderrFD)
	out.use_terminal = C.bool(cfg.UseTerminal)
	out.enable_networking = C.bool(cfg.EnableNetworking)
	out.dns_nameservers = (**C.char)(unsafe.Pointer(m.cStringArray(cfg.DNSNameservers)))
	hostsPtr, hostCount := m.cHostEntryArray(cfg.HostEntries)
	out.host_entries = hostsPtr
	out.host_entry_count = hostCount
	out.auto_inject_host_silo = C.bool(cfg.AutoInjectHost)
	return out, m
}

// buildExecConfig allocates an SBExecConfig in C memory.
func buildExecConfig(cfg ExecConfig) (*C.SBExecConfig, *cMemory) {
	m := &cMemory{}
	out := (*C.SBExecConfig)(C.malloc(C.size_t(unsafe.Sizeof(C.SBExecConfig{}))))
	m.ptrs = append(m.ptrs, unsafe.Pointer(out))
	C.memset(unsafe.Pointer(out), 0, C.size_t(unsafe.Sizeof(C.SBExecConfig{})))
	out.arguments = (**C.char)(unsafe.Pointer(m.cStringArray(cfg.Arguments)))
	if cfg.WorkingDirectory != "" {
		out.working_directory = m.cString(cfg.WorkingDirectory)
	}
	out.stdin_fd = C.int32_t(cfg.StdinFD)
	out.stdout_fd = C.int32_t(cfg.StdoutFD)
	out.stderr_fd = C.int32_t(cfg.StderrFD)
	out.use_terminal = C.bool(cfg.UseTerminal)
	return out, m
}

// buildMount allocates an SBMount for rootfs in C memory.
func buildMount(spec MountSpec) (*C.SBMount, *cMemory) {
	m := &cMemory{}
	out := (*C.SBMount)(C.malloc(C.size_t(unsafe.Sizeof(C.SBMount{}))))
	m.ptrs = append(m.ptrs, unsafe.Pointer(out))
	C.memset(unsafe.Pointer(out), 0, C.size_t(unsafe.Sizeof(C.SBMount{})))
	m.cMount(spec, out)
	return out, m
}
