// SPDX-License-Identifier: Apache-2.0

package config

import "time"

// CacheConfig is the top-level cache policy knob exposed through siloconf.
// Everything is optional; zero fields mean "no limit".
//
//	cache:
//	  rootfs:
//	    maxSizeMB: 8192
//	    maxAgeDays: 60
//	  tools:
//	    maxSizeMB: 4096
//	    maxAgeDays: 30
//	    perMount:
//	      python/pip: 512
//	      rust/cargo: 2048
type CacheConfig struct {
	Rootfs *CachePolicy     `yaml:"rootfs,omitempty"`
	Tools  *ToolCachePolicy `yaml:"tools,omitempty"`
}

// CachePolicy is a size+age eviction policy.
type CachePolicy struct {
	MaxSizeMB  uint64 `yaml:"maxSizeMB,omitempty"`
	MaxAgeDays uint64 `yaml:"maxAgeDays,omitempty"`
}

// ToolCachePolicy applies to per-tool package caches, with optional per-mount
// overrides keyed on "tool/subdir" (e.g. "python/pip", "rust/cargo").
type ToolCachePolicy struct {
	CachePolicy `yaml:",inline"`
	PerMount    map[string]uint64 `yaml:"perMount,omitempty"` // "<tool>/<subdir>": maxSizeMB
}

// EffectiveRootfsPolicy returns the rootfs cache policy with defaults applied.
// Defaults are generous but bounded — intended to keep a 10-tool user's cache
// under 8 GB and evict anything untouched for two months.
func (c *CacheConfig) EffectiveRootfsPolicy() CachePolicy {
	if c == nil || c.Rootfs == nil {
		return CachePolicy{MaxSizeMB: 8192, MaxAgeDays: 60}
	}
	p := *c.Rootfs
	// Explicit 0 means "no cap" — only fill in defaults when the field is absent
	// from YAML. yaml.v3 can't distinguish zero-from-missing without custom
	// tags, so we treat zero as disabled and require users to set it.
	return p
}

// EffectiveToolsPolicy returns the tool-cache policy with defaults applied.
func (c *CacheConfig) EffectiveToolsPolicy() ToolCachePolicy {
	if c == nil || c.Tools == nil {
		return ToolCachePolicy{CachePolicy: CachePolicy{MaxSizeMB: 4096, MaxAgeDays: 30}}
	}
	return *c.Tools
}

// MaxAge returns the age cutoff as a duration, or 0 if unset.
func (p CachePolicy) MaxAge() time.Duration {
	if p.MaxAgeDays == 0 {
		return 0
	}
	return time.Duration(p.MaxAgeDays) * 24 * time.Hour
}

// MaxSizeBytes returns the size cap in bytes, or 0 if unset.
func (p CachePolicy) MaxSizeBytes() uint64 {
	return p.MaxSizeMB * 1024 * 1024
}
