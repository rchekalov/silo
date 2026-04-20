// SPDX-License-Identifier: Apache-2.0

// Package errs defines sentinel errors for Silo. Callers match with errors.Is.
// Wrap these with fmt.Errorf("...: %w", ErrXxx, ...) when adding context.
package errs

import (
	"errors"
	"fmt"
)

var (
	ErrToolNotFound        = errors.New("tool not found")
	ErrToolAlreadyInstalled = errors.New("tool already installed")
	ErrToolNotInstalled    = errors.New("tool not installed")
	ErrConfig              = errors.New("configuration error")
	ErrRuntime             = errors.New("runtime error")
	ErrContainer           = errors.New("container error")
	ErrPathNotFound        = errors.New("path not found")
)

// ToolNotFoundError returns a descriptive wrap of ErrToolNotFound for `name`.
func ToolNotFoundError(name string) error {
	return fmt.Errorf("%w: %q (run 'silo list --available' to see available tools)", ErrToolNotFound, name)
}

// ToolAlreadyInstalledError returns a descriptive wrap of ErrToolAlreadyInstalled.
func ToolAlreadyInstalledError(name string) error {
	return fmt.Errorf("%w: %q", ErrToolAlreadyInstalled, name)
}

// ToolNotInstalledError wraps ErrToolNotInstalled with the tool name.
func ToolNotInstalledError(name string) error {
	return fmt.Errorf("%w: %q (run 'silo install %s' first)", ErrToolNotInstalled, name, name)
}

// Configf wraps ErrConfig with a formatted message.
func Configf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...))
}

// Runtimef wraps ErrRuntime with a formatted message.
func Runtimef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRuntime, fmt.Sprintf(format, args...))
}

// Containerf wraps ErrContainer with a formatted message.
func Containerf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrContainer, fmt.Sprintf(format, args...))
}
