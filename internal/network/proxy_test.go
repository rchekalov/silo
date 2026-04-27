// SPDX-License-Identifier: Apache-2.0

package network

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func TestMatchDomainExact(t *testing.T) {
	if !MatchDomain("pypi.org", "pypi.org") {
		t.Fatal("exact match")
	}
	if MatchDomain("evil.com", "pypi.org") {
		t.Fatal("unrelated should not match")
	}
}

func TestMatchDomainWildcard(t *testing.T) {
	if !MatchDomain("api.github.com", "*.github.com") {
		t.Fatal("subdomain")
	}
	if !MatchDomain("github.com", "*.github.com") {
		t.Fatal("apex with wildcard")
	}
	if MatchDomain("fakegithub.com", "*.github.com") {
		t.Fatal("suffix-only shouldn't match different TLD")
	}
}

func TestIsAllowedWithAllowlist(t *testing.T) {
	p := &HTTPProxy{rule: config.ProxyConfig{Allow: []string{"*.github.com", "pypi.org"}}}
	if !p.IsAllowed("api.github.com") {
		t.Fatal("api.github.com should be allowed")
	}
	if !p.IsAllowed("pypi.org") {
		t.Fatal("pypi.org should be allowed")
	}
	if p.IsAllowed("evil.com") {
		t.Fatal("evil.com should be denied (allowlist exhaustive)")
	}
}

func TestIsAllowedDefaultDeny(t *testing.T) {
	// Empty rule denies everything: this is the deny-by-default contract.
	p := &HTTPProxy{rule: config.ProxyConfig{}}
	if p.IsAllowed("pypi.org") {
		t.Fatal("empty rule must deny pypi.org")
	}
	if p.IsAllowed("anything.example") {
		t.Fatal("empty rule must deny everything")
	}
}

func TestIsAllowedStarCatchAll(t *testing.T) {
	// allow:["*"] is the explicit opt-in to open internet.
	p := &HTTPProxy{rule: config.ProxyConfig{Allow: []string{"*"}}}
	if !p.IsAllowed("anything.example") {
		t.Fatal("allow:[*] must permit any host")
	}
	if !p.IsAllowed("pypi.org") {
		t.Fatal("allow:[*] must permit pypi.org")
	}
}

func TestIsAllowedStarWithDenyHole(t *testing.T) {
	// Deny takes precedence over catch-all so projects can punch holes.
	p := &HTTPProxy{rule: config.ProxyConfig{Allow: []string{"*"}, Deny: []string{"evil.com"}}}
	if p.IsAllowed("evil.com") {
		t.Fatal("deny must override allow:*")
	}
	if !p.IsAllowed("pypi.org") {
		t.Fatal("non-denied still allowed by *")
	}
}

func TestIsAllowedWithDenylistOnly(t *testing.T) {
	// Deny without Allow still denies everything (deny-by-default).
	p := &HTTPProxy{rule: config.ProxyConfig{Deny: []string{"evil.com"}}}
	if p.IsAllowed("evil.com") {
		t.Fatal("evil.com denied")
	}
	if p.IsAllowed("pypi.org") {
		t.Fatal("no allow ⇒ everything denied; deny list alone does not unlock the rest")
	}
}

func TestIsAllowedDenyStar(t *testing.T) {
	p := &HTTPProxy{rule: config.ProxyConfig{Allow: []string{"pypi.org"}, Deny: []string{"*"}}}
	if !p.IsAllowed("pypi.org") {
		t.Fatal("pypi.org allowed")
	}
	if p.IsAllowed("anything.else") {
		t.Fatal("catch-all deny")
	}
}

// Integration test: drive an upstream httptest server through the proxy.
func TestHTTPProxyForwardsAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "upstream ok")
	}))
	defer upstream.Close()

	// Host is "127.0.0.1" so add that to the allowlist.
	p, err := StartHTTPProxy(config.ProxyConfig{Allow: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	upURL := upstream.URL // "http://127.0.0.1:PORT"
	host := strings.TrimPrefix(upURL, "http://")
	req := "GET " + upURL + " HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "upstream ok") {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestHTTPProxyDeniesForbidden(t *testing.T) {
	p, err := StartHTTPProxy(config.ProxyConfig{Allow: []string{"pypi.org"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := "GET http://evil.com/ HTTP/1.1\r\nHost: evil.com\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "403 Forbidden") {
		t.Fatalf("expected 403, got: %s", body)
	}
}

// Make strconv noise-free even if other tests don't use it.
var _ = strconv.Itoa
