// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func TestApplySetupResourceFloorsRaises(t *testing.T) {
	def := config.ToolDefinition{CPUs: 2, MemoryMB: 512, RootfsSizeMB: 2048}
	applySetupResourceFloors(&def)
	if def.CPUs != 4 {
		t.Fatalf("cpus: %d", def.CPUs)
	}
	if def.MemoryMB != 4096 {
		t.Fatalf("memory: %d", def.MemoryMB)
	}
	if def.RootfsSizeMB != 4096 {
		t.Fatalf("rootfs: %d", def.RootfsSizeMB)
	}
}

func TestApplySetupResourceFloorsKeepsHigher(t *testing.T) {
	def := config.ToolDefinition{CPUs: 8, MemoryMB: 8192, RootfsSizeMB: 16384}
	applySetupResourceFloors(&def)
	if def.CPUs != 8 || def.MemoryMB != 8192 || def.RootfsSizeMB != 16384 {
		t.Fatalf("floors raised already-higher values: %+v", def)
	}
}
