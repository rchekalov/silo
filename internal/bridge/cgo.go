// SPDX-License-Identifier: Apache-2.0

// Package bridge binds the Silo Go runtime to libSiloBridge.dylib (a Swift
// dynamic library built from swift-bridge/ that wraps Apple Containerization).
//
// Linking: the Go build must set CGO_LDFLAGS=-L<dir> -lSiloBridge -Wl,-rpath,<rpath>
// so the resulting binary can locate the dylib at link- and run-time.
// See the Makefile's go-bin / go-bin-release targets.
//
// All C declarations, forward declarations for Go-exported callbacks, and the
// thin `silo_*` shim wrappers live in silo_bridge.h so every .go file in this
// package sees the same C symbols. Do not duplicate them in per-file preambles.
package bridge

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -lSiloBridge

#include <stdlib.h>
#include <string.h>
#include "silo_bridge.h"
*/
import "C"
