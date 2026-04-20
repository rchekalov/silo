// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include "silo_bridge.h"
*/
import "C"

import (
	"runtime"
	"sync"
)

// Image wraps a libSiloBridge ImageBox.
type Image struct {
	mu     sync.Mutex
	handle C.SBImageHandle
}

func newImage(h C.SBImageHandle) *Image {
	img := &Image{handle: h}
	runtime.SetFinalizer(img, func(i *Image) { i.Close() })
	return img
}

// Close releases the underlying Swift ImageBox.
func (i *Image) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.handle != nil {
		C.sb_free_image(i.handle)
		i.handle = nil
	}
	runtime.SetFinalizer(i, nil)
}

// Digest returns the content-addressable digest (e.g., "sha256:...").
func (i *Image) Digest() string {
	p := C.sb_image_digest(i.handle)
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.sb_free_string(p)
	return s
}
