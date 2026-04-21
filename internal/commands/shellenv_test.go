// SPDX-License-Identifier: Apache-2.0

package commands

import "testing"

func TestShellenvFor(t *testing.T) {
	posix := `export PATH="$HOME/.silo/bin:$PATH"`

	tests := []struct {
		shell string
		want  string
	}{
		// Idempotent, fish-idiomatic path manipulation (fish 3.2+).
		{"fish", `fish_add_path --global --prepend "$HOME/.silo/bin"`},

		// csh/tcsh: no `export`; setenv is the equivalent. Trailing `;`
		// lets users combine with `rehash` on the same line if desired.
		{"csh", `setenv PATH "$HOME/.silo/bin:$PATH";`},
		{"tcsh", `setenv PATH "$HOME/.silo/bin:$PATH";`},

		// PowerShell: platform-correct separator via IO.Path.
		{"pwsh", `$env:PATH = "$HOME/.silo/bin" + [IO.Path]::PathSeparator + $env:PATH`},
		{"powershell", `$env:PATH = "$HOME/.silo/bin" + [IO.Path]::PathSeparator + $env:PATH`},

		// POSIX path for bash/zsh/sh/dash/ksh and any unknown shell —
		// if someone's on a POSIX-compatible niche shell they get the
		// right thing; if they're on something exotic, they can still
		// read the output and adapt.
		{"bash", posix},
		{"zsh", posix},
		{"sh", posix},
		{"dash", posix},
		{"ksh", posix},
		{"", posix},
		{"unknown-shell", posix},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			if got := shellenvFor(tc.shell); got != tc.want {
				t.Errorf("shellenvFor(%q) = %q, want %q", tc.shell, got, tc.want)
			}
		})
	}
}
