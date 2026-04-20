// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include <stdlib.h>
#include "silo_bridge.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// Manager wraps a libSiloBridge ManagerBox / Apple ContainerManager.
// A Manager owns the Linux kernel image, initfs, and image store for one ~/.silo root.
type Manager struct {
	mu     sync.Mutex
	handle C.SBManagerHandle
}

// NewManager creates a Manager. enableVmnet enables the macOS 26+ VmnetNetwork
// path; on older macOS it's silently ignored.
func NewManager(kernelPath, initfsPath, rootPath string, enableVmnet bool) (*Manager, error) {
	if kernelPath == "" || initfsPath == "" || rootPath == "" {
		return nil, errors.New("bridge: kernel/initfs/root path must all be set")
	}
	ckernel := C.CString(kernelPath)
	cinitfs := C.CString(initfsPath)
	croot := C.CString(rootPath)
	defer C.free(unsafe.Pointer(ckernel))
	defer C.free(unsafe.Pointer(cinitfs))
	defer C.free(unsafe.Pointer(croot))

	var handle C.SBManagerHandle
	var cerr *C.char
	C.sb_manager_create(ckernel, cinitfs, croot, C.bool(enableVmnet), &handle, &cerr)
	if cerr != nil {
		msg := C.GoString(cerr)
		C.sb_free_string(cerr)
		return nil, fmt.Errorf("bridge: manager_create: %s", msg)
	}
	if handle == nil {
		return nil, errors.New("bridge: manager_create returned nil handle with no error")
	}
	m := &Manager{handle: handle}
	runtime.SetFinalizer(m, func(m *Manager) { m.Close() })
	return m, nil
}

// Close releases the underlying Swift ManagerBox. Safe to call multiple times.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle != nil {
		C.sb_free_manager(m.handle)
		m.handle = nil
	}
	runtime.SetFinalizer(m, nil)
}

// CreateContainerFromRef creates a fresh container by pulling/unpacking image `reference`.
func (m *Manager) CreateContainerFromRef(
	containerID, reference string, rootfsSizeBytes uint64, cfg ContainerConfig,
) (*Container, error) {
	cid := C.CString(containerID)
	cref := C.CString(reference)
	defer C.free(unsafe.Pointer(cid))
	defer C.free(unsafe.Pointer(cref))

	cconfig, owner := buildContainerConfig(cfg)
	defer owner.free()

	id, ch := registerCall()
	C.silo_create_container_from_ref(
		m.handle, cid, cref, C.uint64_t(rootfsSizeBytes), cconfig, unsafe.Pointer(uintptr(id)),
	)
	r := <-ch
	if r.err != nil {
		return nil, fmt.Errorf("create_container_from_ref: %w", r.err)
	}
	return newContainer(C.SBContainerHandle(r.handle)), nil
}

// CreateContainerFromImage creates a container using a pre-loaded Image + rootfs Mount.
func (m *Manager) CreateContainerFromImage(
	containerID string, image *Image, rootfs MountSpec, cfg ContainerConfig,
) (*Container, error) {
	cid := C.CString(containerID)
	defer C.free(unsafe.Pointer(cid))

	cconfig, cfgOwner := buildContainerConfig(cfg)
	defer cfgOwner.free()
	cmount, mountOwner := buildMount(rootfs)
	defer mountOwner.free()

	id, ch := registerCall()
	C.silo_create_container_from_image(
		m.handle, cid, image.handle, cmount, cconfig, unsafe.Pointer(uintptr(id)),
	)
	r := <-ch
	if r.err != nil {
		return nil, fmt.Errorf("create_container_from_image: %w", r.err)
	}
	return newContainer(C.SBContainerHandle(r.handle)), nil
}

// Delete forgets a container by id on the Swift side (ignores errors).
func (m *Manager) Delete(containerID string) {
	cid := C.CString(containerID)
	defer C.free(unsafe.Pointer(cid))
	C.sb_manager_delete(m.handle, cid)
}

// ImageGet retrieves an image from the local store, optionally pulling it.
func (m *Manager) ImageGet(reference string, pull bool) (*Image, error) {
	cref := C.CString(reference)
	defer C.free(unsafe.Pointer(cref))

	id, ch := registerCall()
	C.silo_image_store_get(m.handle, cref, C.bool(pull), unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return nil, fmt.Errorf("image_store_get(%q): %w", reference, r.err)
	}
	return newImage(C.SBImageHandle(r.handle)), nil
}

// ImagePull pulls an image into the local store (no handle returned).
func (m *Manager) ImagePull(reference string) error {
	cref := C.CString(reference)
	defer C.free(unsafe.Pointer(cref))

	id, ch := registerCall()
	C.silo_image_store_pull(m.handle, cref, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("image_store_pull(%q): %w", reference, r.err)
	}
	return nil
}

// ImageDelete removes the image identified by `reference` from the local OCI
// store. If `cleanupOrphans` is true, unreferenced content blobs are also
// garbage-collected inline (implies an ImageStoreCleanupOrphans pass).
func (m *Manager) ImageDelete(reference string, cleanupOrphans bool) error {
	cref := C.CString(reference)
	defer C.free(unsafe.Pointer(cref))

	id, ch := registerCall()
	C.silo_image_store_delete(m.handle, cref, C.bool(cleanupOrphans), unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return fmt.Errorf("image_store_delete(%q): %w", reference, r.err)
	}
	return nil
}

// ImageStoreCleanupOrphans GCs content-store blobs not referenced by any
// image. Returns freed bytes on success.
func (m *Manager) ImageStoreCleanupOrphans() (uint64, error) {
	id, ch := registerCall()
	C.silo_image_store_cleanup_orphans(m.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return 0, fmt.Errorf("image_store_cleanup_orphans: %w", r.err)
	}
	return r.size, nil
}

// ImageStoreOrphansSize reports the total size of blobs that would be GC'd
// by ImageStoreCleanupOrphans.
func (m *Manager) ImageStoreOrphansSize() (uint64, error) {
	id, ch := registerCall()
	C.silo_image_store_orphans_size(m.handle, unsafe.Pointer(uintptr(id)))
	r := <-ch
	if r.err != nil {
		return 0, fmt.Errorf("image_store_orphans_size: %w", r.err)
	}
	return r.size, nil
}
