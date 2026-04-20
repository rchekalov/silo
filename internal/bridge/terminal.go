// SPDX-License-Identifier: Apache-2.0

package bridge

/*
#include "silo_bridge.h"
*/
import "C"

import "errors"

// TerminalSize asks Apple Containerization for the current terminal geometry.
// Returns cols, rows, and an error if no terminal is attached.
func TerminalSize() (cols, rows uint16, err error) {
	var c, r C.uint16_t
	rc := C.sb_terminal_get_size(&c, &r)
	if rc != 0 {
		return 0, 0, errors.New("bridge: terminal size unavailable")
	}
	return uint16(c), uint16(r), nil
}
