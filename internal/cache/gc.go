// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"time"
)

// GCPolicy is a size + age eviction policy. Zero fields disable the
// corresponding axis — a policy with both zero is a no-op.
type GCPolicy struct {
	// MaxTotalBytes caps the aggregate on-disk size of the cache. Oldest-first
	// eviction runs until the total is ≤ this value.
	MaxTotalBytes uint64
	// MaxAge is the age cutoff for an entry's LastUsed time. Older entries are
	// evicted before any size-based eviction runs.
	MaxAge time.Duration
}

// GCResult is the summary returned by Rootfs.GC.
type GCResult struct {
	Evicted     []Entry
	FreedBytes  uint64
	TotalBefore uint64
	TotalAfter  uint64
}

// AutoGC runs GC if the cache is over cap, returning a flag for whether it
// actually did anything. Use from passive hooks that shouldn't be slow when
// the cache is well under cap — the fast path is just a directory scan.
func (c *Rootfs) AutoGC(policy GCPolicy) (GCResult, bool, error) {
	// Cheap check: if no size policy, fall through to the age pass unconditionally.
	if policy.MaxTotalBytes > 0 {
		total, err := c.TotalDiskSize()
		if err != nil {
			return GCResult{}, false, err
		}
		if total <= policy.MaxTotalBytes && policy.MaxAge == 0 {
			return GCResult{TotalBefore: total, TotalAfter: total}, false, nil
		}
	}
	res, err := c.GC(policy)
	return res, len(res.Evicted) > 0, err
}

// GC applies the policy and returns the summary. Safe to call with a zero
// policy — returns (result-with-no-evictions, nil).
func (c *Rootfs) GC(policy GCPolicy) (GCResult, error) {
	entries, err := c.Entries()
	if err != nil {
		return GCResult{}, err
	}
	var total uint64
	for _, e := range entries {
		total += e.EffectiveDiskSize()
	}
	result := GCResult{TotalBefore: total, TotalAfter: total}

	now := time.Now()

	// Age eviction first: frees up entries regardless of total size pressure.
	if policy.MaxAge > 0 {
		cutoff := now.Add(-policy.MaxAge)
		kept := entries[:0]
		for _, e := range entries {
			if !e.LastUsed.IsZero() && e.LastUsed.Before(cutoff) {
				if err := c.RemoveByDigest(e.Digest, 0); err == nil {
					result.Evicted = append(result.Evicted, e)
					result.FreedBytes += e.EffectiveDiskSize()
					result.TotalAfter -= e.EffectiveDiskSize()
				}
				continue
			}
			kept = append(kept, e)
		}
		entries = kept
	}

	// Size eviction: oldest-first until we're under the cap.
	if policy.MaxTotalBytes > 0 {
		for result.TotalAfter > policy.MaxTotalBytes && len(entries) > 0 {
			victim := entries[0]
			entries = entries[1:]
			if rmErr := c.RemoveByDigest(victim.Digest, 0); rmErr != nil {
				break
			}
			result.Evicted = append(result.Evicted, victim)
			result.FreedBytes += victim.EffectiveDiskSize()
			result.TotalAfter -= victim.EffectiveDiskSize()
		}
	}

	return result, nil //nolint:nilerr // remove failures abort size eviction but aren't fatal to GC
}
