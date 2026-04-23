// SPDX-License-Identifier: Apache-2.0

package lsp

import (
	"fmt"
	"strings"
)

// Proxy rewrites paths and file:// URIs in LSP JSON bodies between host and guest.
type Proxy struct {
	hostPrefix     string
	guestPrefix    string
	hostURIPrefix  string
	guestURIPrefix string
}

// NewProxy configures the rewrite mapping. URIs use the same prefixes,
// percent-encoded (e.g., spaces → %20).
func NewProxy(hostPrefix, guestPrefix string) *Proxy {
	return &Proxy{
		hostPrefix:     hostPrefix,
		guestPrefix:    guestPrefix,
		hostURIPrefix:  "file://" + PercentEncodePath(hostPrefix),
		guestURIPrefix: "file://" + PercentEncodePath(guestPrefix),
	}
}

// RewriteInbound replaces host paths with guest paths in IDE→VM messages.
func (p *Proxy) RewriteInbound(body []byte) []byte {
	return rewrite(body, p.hostPrefix, p.guestPrefix, p.hostURIPrefix, p.guestURIPrefix)
}

// RewriteOutbound replaces guest paths with host paths in VM→IDE messages.
func (p *Proxy) RewriteOutbound(body []byte) []byte {
	return rewrite(body, p.guestPrefix, p.hostPrefix, p.guestURIPrefix, p.hostURIPrefix)
}

func rewrite(body []byte, from, to, fromURI, toURI string) []byte {
	if len(body) == 0 {
		return body
	}
	s := string(body)
	// More-specific URI form first, then raw path.
	s = strings.ReplaceAll(s, fromURI, toURI)
	s = strings.ReplaceAll(s, from, to)
	return []byte(s)
}

// PercentEncodePath encodes non-unreserved bytes as %XX, preserving path separators.
func PercentEncodePath(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		if isUnreserved(c) || c == '/' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
	case c >= 'a' && c <= 'z':
	case c >= '0' && c <= '9':
	case c == '-', c == '_', c == '.', c == '~':
	default:
		return false
	}
	return true
}
