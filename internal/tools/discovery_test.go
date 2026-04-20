// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"testing"
)

func TestParseDiscoveredLinesFiltersBlocklist(t *testing.T) {
	input := []byte(`/usr/bin/node
/usr/bin/ls
/usr/local/bin/npm
/usr/bin/bash
/usr/bin/python3
/usr/bin/sh
/usr/bin/find
`)
	got := parseDiscoveredLines(input)
	want := []string{"node", "npm", "python3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], v)
		}
	}
}

func TestParseDiscoveredLinesDedup(t *testing.T) {
	input := []byte(`/usr/bin/node
/usr/local/bin/node
/usr/local/sbin/node
`)
	got := parseDiscoveredLines(input)
	if len(got) != 1 || got[0] != "node" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDiscoveredLinesEmpty(t *testing.T) {
	if got := parseDiscoveredLines([]byte{}); got != nil {
		t.Fatalf("got %+v", got)
	}
	if got := parseDiscoveredLines([]byte("\n\n  \n")); got != nil {
		t.Fatalf("got %+v", got)
	}
}
