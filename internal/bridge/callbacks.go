// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include <stdint.h>
#include "silo_bridge.h"
*/
import "C"

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"
)

// callResult carries the outcome of one FFI call back from Swift.
// Exactly one of (err) / (handle, err) / (code, err) / (size, err) is populated,
// depending on which callback trampoline fired. See the comments on
// siloSimpleCallback etc.
type callResult struct {
	handle unsafe.Pointer
	code   int32
	size   uint64
	err    error
}

var (
	callsMu sync.Mutex
	callsID atomic.Uint64
	calls   = map[uint64]chan callResult{}
)

// registerCall allocates a channel keyed by a fresh id. The id is passed to
// Swift as the opaque `void* context`. When Swift fires the callback, the
// trampoline in this file looks up the channel by id and forwards the result.
//
// Returns (id, ch). The caller MUST receive from ch exactly once.
func registerCall() (uint64, chan callResult) {
	id := callsID.Add(1)
	ch := make(chan callResult, 1)
	callsMu.Lock()
	calls[id] = ch
	callsMu.Unlock()
	return id, ch
}

func takeCall(id uint64) chan callResult {
	callsMu.Lock()
	ch, ok := calls[id]
	delete(calls, id)
	callsMu.Unlock()
	if !ok {
		return nil
	}
	return ch
}

// cStringToError converts a nullable Swift-owned C string into a Go error.
// The caller frees the C string via sb_free_string.
func cStringToError(cerr *C.char) error {
	if cerr == nil {
		return nil
	}
	msg := C.GoString(cerr)
	C.sb_free_string(cerr)
	if msg == "" {
		return errors.New("bridge: empty error from Swift")
	}
	return errors.New(msg)
}

//export siloSimpleCallback
func siloSimpleCallback(ctx unsafe.Pointer, cerr *C.char) {
	id := uint64(uintptr(ctx))
	ch := takeCall(id)
	if ch == nil {
		if cerr != nil {
			C.sb_free_string(cerr)
		}
		return
	}
	ch <- callResult{err: cStringToError(cerr)}
}

//export siloHandleCallback
func siloHandleCallback(ctx unsafe.Pointer, handle unsafe.Pointer, cerr *C.char) {
	id := uint64(uintptr(ctx))
	ch := takeCall(id)
	if ch == nil {
		if cerr != nil {
			C.sb_free_string(cerr)
		}
		return
	}
	ch <- callResult{handle: handle, err: cStringToError(cerr)}
}

//export siloExitCallback
func siloExitCallback(ctx unsafe.Pointer, code C.int32_t, cerr *C.char) {
	id := uint64(uintptr(ctx))
	ch := takeCall(id)
	if ch == nil {
		if cerr != nil {
			C.sb_free_string(cerr)
		}
		return
	}
	ch <- callResult{code: int32(code), err: cStringToError(cerr)}
}

//export siloSizeCallback
func siloSizeCallback(ctx unsafe.Pointer, size C.uint64_t, cerr *C.char) {
	id := uint64(uintptr(ctx))
	ch := takeCall(id)
	if ch == nil {
		if cerr != nil {
			C.sb_free_string(cerr)
		}
		return
	}
	ch <- callResult{size: uint64(size), err: cStringToError(cerr)}
}
