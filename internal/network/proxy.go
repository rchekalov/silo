// SPDX-License-Identifier: Apache-2.0

package network

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/rchekalov/silo/internal/config"
)

// HTTPProxy is a forward HTTP/HTTPS proxy with a domain allowlist. It listens
// on all interfaces on a dynamic port and forwards connections whose
// Host/target domain matches the allowlist; everything else returns 403 (HTTP)
// or connection close (CONNECT/HTTPS).
//
// Port of Sources/SiloCore/Network/NetworkProxy.swift from the main branch.
type HTTPProxy struct {
	listener net.Listener
	port     uint16
	rule     config.ProxyConfig
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// StartHTTPProxy binds 0.0.0.0:0, records the chosen port, and starts the
// accept loop. Returns immediately.
func StartHTTPProxy(rule config.ProxyConfig) (*HTTPProxy, error) {
	// Bind on all interfaces so the guest can dial host.silo.internal
	// (the VMNet host-facing IP) — loopback is not bridged into the VM.
	// This exposes the proxy to the host's LAN, but it's allowlist-gated:
	// an unauthenticated LAN client can only reach the same destinations
	// the project already opted into, not arbitrary internet.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("network proxy: listen: %w", err)
	}
	p := &HTTPProxy{
		listener: l,
		port:     uint16(l.Addr().(*net.TCPAddr).Port),
		rule:     rule,
	}
	p.wg.Add(1)
	go p.acceptLoop()
	return p, nil
}

// Port returns the allocated listener port.
func (p *HTTPProxy) Port() uint16 { return p.port }

// Stop closes the listener and drains outstanding goroutines.
func (p *HTTPProxy) Stop() {
	p.stopOnce.Do(func() {
		_ = p.listener.Close()
		p.wg.Wait()
	})
}

func (p *HTTPProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

// handle reads the first request line to decide CONNECT vs plain HTTP, then
// dispatches.
func (p *HTTPProxy) handle(inbound net.Conn) {
	defer inbound.Close()
	reader := bufio.NewReader(inbound)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	trimmed := strings.TrimRight(firstLine, "\r\n")
	parts := strings.SplitN(trimmed, " ", 3)
	if len(parts) < 2 {
		writeHTTPStatus(inbound, "400 Bad Request")
		return
	}
	method, target := parts[0], parts[1]
	switch method {
	case "CONNECT":
		p.handleConnect(inbound, reader, target)
	default:
		p.handleHTTP(inbound, reader, firstLine, method, target)
	}
}

// handleConnect implements the HTTPS CONNECT tunnel (TLS passthrough).
func (p *HTTPProxy) handleConnect(inbound net.Conn, reader *bufio.Reader, target string) {
	// Consume the rest of the headers (until \r\n\r\n).
	if err := drainHeaders(reader); err != nil {
		return
	}
	host, port := parseHostPort(target, 443)
	if !p.IsAllowed(host) {
		logDenied(host)
		writeHTTPStatus(inbound, "403 Forbidden")
		return
	}
	out, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		writeHTTPStatus(inbound, "502 Bad Gateway")
		return
	}
	defer out.Close()
	if _, err := inbound.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	// If reader buffered extra bytes (TLS ClientHello can land in the same syscall), flush them.
	if buffered := reader.Buffered(); buffered > 0 {
		b, _ := reader.Peek(buffered)
		_, _ = out.Write(b)
		_, _ = reader.Discard(buffered)
	}
	relayBoth(inbound, out)
}

// handleHTTP forwards plain HTTP by extracting Host and reconstructing the request.
func (p *HTTPProxy) handleHTTP(inbound net.Conn, reader *bufio.Reader, firstLine, method, target string) {
	headers, hostHeader, err := readHeadersFindHost(reader)
	if err != nil {
		return
	}
	// hostPort carries host[:port]; host is the matching-only form (no port).
	// Absolute-form target ("GET http://host[:port]/path") wins over the Host header.
	var hostPort string
	if strings.HasPrefix(target, "http://") {
		rest := strings.TrimPrefix(target, "http://")
		if i := strings.Index(rest, "/"); i >= 0 {
			hostPort = rest[:i]
		} else {
			hostPort = rest
		}
	} else if hostHeader != "" {
		hostPort = hostHeader
	}
	host := hostPort
	if c := strings.Index(host, ":"); c >= 0 {
		host = host[:c]
	}
	if host == "" || !p.IsAllowed(host) {
		logDenied(defaultIfEmpty(host, "unknown"))
		writeHTTPStatus(inbound, "403 Forbidden")
		return
	}
	targetHost, targetPort := parseHostPort(hostPort, 80)
	out, err := net.Dial("tcp", net.JoinHostPort(targetHost, fmt.Sprint(targetPort)))
	if err != nil {
		writeHTTPStatus(inbound, "502 Bad Gateway")
		return
	}
	defer out.Close()

	// Forward the request: first line, headers, blank, then body (relayed).
	if _, err := out.Write([]byte(firstLine)); err != nil {
		return
	}
	if _, err := out.Write([]byte(headers)); err != nil {
		return
	}
	if _, err := out.Write([]byte("\r\n")); err != nil {
		return
	}
	// Flush any buffered body bytes.
	if buffered := reader.Buffered(); buffered > 0 {
		b, _ := reader.Peek(buffered)
		_, _ = out.Write(b)
		_, _ = reader.Discard(buffered)
	}
	relayBoth(inbound, out)
	_ = method // referenced for future per-method logic
}

// IsAllowed returns true if `domain` is permitted by the proxy rule.
// An explicit match in Allow wins; "*" in Deny denies everything else;
// otherwise (non-catch-all deny list) the default is allow.
func (p *HTTPProxy) IsAllowed(domain string) bool {
	d := strings.ToLower(domain)
	for _, pattern := range p.rule.Allow {
		if MatchDomain(d, strings.ToLower(pattern)) {
			return true
		}
	}
	for _, pattern := range p.rule.Deny {
		if pattern == "*" {
			return false
		}
		if MatchDomain(d, strings.ToLower(pattern)) {
			return false
		}
	}
	// If an allowlist exists but didn't match and there's no catch-all deny,
	// treat the allowlist as exhaustive: deny.
	if len(p.rule.Allow) > 0 {
		return false
	}
	return true
}

// MatchDomain supports leading wildcard patterns: "*.github.com" matches
// "api.github.com" and the apex "github.com".
func MatchDomain(domain, pattern string) bool {
	if pattern == domain {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]            // ".github.com"
		apex := pattern[2:]               // "github.com"
		return strings.HasSuffix(domain, suffix) || domain == apex
	}
	return false
}

// --- helpers ----------------------------------------------------------------

func drainHeaders(r *bufio.Reader) error {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			return nil
		}
	}
}

// readHeadersFindHost returns the raw header block (including trailing CRLF per
// header but NOT the blank separator) plus the Host header value.
func readHeadersFindHost(r *bufio.Reader) (string, string, error) {
	var b strings.Builder
	var host string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		if line == "\r\n" || line == "\n" {
			return b.String(), host, nil
		}
		b.WriteString(line)
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			// Preserve port: "Host: example.com:8080" → "example.com:8080".
			host = strings.TrimSpace(line[5:])
		}
	}
}

func parseHostPort(target string, defaultPort uint16) (string, uint16) {
	if c := strings.Index(target, ":"); c >= 0 {
		var p uint16
		if _, err := fmt.Sscanf(target[c+1:], "%d", &p); err == nil && p > 0 {
			return target[:c], p
		}
	}
	return target, defaultPort
}

func writeHTTPStatus(w io.Writer, status string) {
	fmt.Fprintf(w, "HTTP/1.1 %s\r\nConnection: close\r\n\r\n", status)
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func logDenied(domain string) {
	fmt.Fprintf(os.Stderr, "[silo] proxy: denied %s\n", domain)
}

// relayBoth copies in both directions until either side EOFs or errors.
func relayBoth(a, b io.ReadWriter) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}
