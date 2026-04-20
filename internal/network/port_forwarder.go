// SPDX-License-Identifier: Apache-2.0

// Package network handles host<->container networking helpers.
package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/rchekalov/silo/internal/config"
)

// PortForwarder accepts TCP connections on localhost:host and relays them to
// vm_ip:guest. One accept-loop goroutine per mapping.
type PortForwarder struct {
	listeners []net.Listener
	stopOnce  sync.Once
	wg        sync.WaitGroup
	cancel    context.CancelFunc
}

// StartPortForwarder binds every mapping (fail-fast on AddrInUse) then spawns
// accept loops that relay to vmIP:guest.
func StartPortForwarder(mappings []config.PortMapping, vmIP string) (*PortForwarder, error) {
	if len(mappings) == 0 {
		return &PortForwarder{}, nil
	}
	listeners := make([]net.Listener, 0, len(mappings))
	guests := make([]uint16, 0, len(mappings))
	for _, m := range mappings {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.Host))
		if err != nil {
			for _, prev := range listeners {
				_ = prev.Close()
			}
			return nil, fmt.Errorf("port %d: %w", m.Host, err)
		}
		listeners = append(listeners, l)
		guests = append(guests, m.Guest)
		fmt.Fprintf(os.Stderr, "Forwarding localhost:%d -> %s:%d\n", m.Host, vmIP, m.Guest)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pf := &PortForwarder{listeners: listeners, cancel: cancel}
	for i, l := range listeners {
		pf.wg.Add(1)
		go pf.acceptLoop(ctx, l, fmt.Sprintf("%s:%d", vmIP, guests[i]))
	}
	return pf, nil
}

// Stop closes all listeners and waits for outstanding accept loops.
func (p *PortForwarder) Stop() {
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
		for _, l := range p.listeners {
			_ = l.Close()
		}
		p.wg.Wait()
	})
}

func (p *PortForwarder) acceptLoop(ctx context.Context, l net.Listener, target string) {
	defer p.wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(in net.Conn) {
			defer in.Close()
			out, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "port forward to %s: %v\n", target, err)
				return
			}
			defer out.Close()
			// Bidirectional copy: two goroutines closing each other on EOF.
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(out, in); done <- struct{}{} }()
			go func() { _, _ = io.Copy(in, out); done <- struct{}{} }()
			<-done
		}(conn)
	}
}
