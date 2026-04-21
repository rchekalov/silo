// SPDX-License-Identifier: Apache-2.0

package pullprogress

import (
	"io"
	"os"

	"golang.org/x/term"
)

// defaultIsTerminal returns true iff Out is a *os.File and its fd is a TTY.
// Kept in a separate file so tests can stub `isTerminal` without dragging
// x/term into their build.
func defaultIsTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
