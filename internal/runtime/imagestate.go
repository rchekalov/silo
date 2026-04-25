// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// ImageStateEntry is the per-reference metadata stored in ~/.silo/state.json
// by Apple Containerization's image store. Only the digest is load-bearing
// for callers; size and media type are kept for parity with the on-disk format.
type ImageStateEntry struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

// LoadImageState parses ~/.silo/state.json and returns the map of
// `image-reference` -> entry. Returns (nil, nil) if the file does not exist
// (e.g. fresh install with no images pulled yet).
func LoadImageState() (map[string]ImageStateEntry, error) {
	raw, err := os.ReadFile(ImageState())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", ImageState(), err)
	}
	out := make(map[string]ImageStateEntry)
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ImageState(), err)
	}
	return out, nil
}
