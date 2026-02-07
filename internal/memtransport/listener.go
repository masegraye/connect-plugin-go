// Package memtransport provides an in-memory net.Listener and HTTP transport
// using net.Pipe(). This enables ConnectRPC clients and servers to communicate
// within the same process without any TCP, Unix socket, or other OS-level networking.
//
// Usage:
//
//	ln := memtransport.New()
//
//	// Server side
//	srv := &http.Server{Handler: myHandler}
//	go srv.Serve(ln)
//
//	// Client side — uses ln.HTTPClient() which dials through net.Pipe()
//	client := loggerv1connect.NewLoggerClient(ln.HTTPClient(), "http://mem")
//	resp, err := client.Log(ctx, req)
//
//	// Cleanup
//	srv.Shutdown(ctx)
//	ln.Close()
package memtransport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
)

// Listener is an in-memory net.Listener backed by net.Pipe().
// Each call to DialContext creates a net.Pipe pair: one end is returned
// to the dialer (client), the other is handed to Accept (server).
type Listener struct {
	conns  chan net.Conn
	once   sync.Once
	closed chan struct{}
}

// New creates a new in-memory listener ready for use.
func New() *Listener {
	return &Listener{
		conns:  make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}
}

// Accept waits for and returns the next connection (server side of a net.Pipe).
// It blocks until a client calls DialContext or the listener is closed.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-l.conns:
		if !ok {
			return nil, errors.New("memtransport: listener closed")
		}
		return conn, nil
	case <-l.closed:
		return nil, errors.New("memtransport: listener closed")
	}
}

// Close stops the listener. Any blocked Accept calls will return an error.
// Close is safe to call multiple times.
func (l *Listener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

// Addr returns a dummy address for the in-memory listener.
func (l *Listener) Addr() net.Addr {
	return memAddr{}
}

// DialContext creates a new in-memory connection pair via net.Pipe().
// The server end is sent to Accept; the client end is returned to the caller.
// This is intended for use as http.Transport.DialContext.
func (l *Listener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, errors.New("memtransport: listener closed")
	default:
	}

	serverConn, clientConn := net.Pipe()

	select {
	case l.conns <- serverConn:
		return clientConn, nil
	case <-l.closed:
		serverConn.Close()
		clientConn.Close()
		return nil, errors.New("memtransport: listener closed")
	case <-ctx.Done():
		serverConn.Close()
		clientConn.Close()
		return nil, ctx.Err()
	}
}

// Transport returns an *http.Transport configured to dial through this listener.
// Uses HTTP/1.1 (net.Pipe doesn't support TLS, so no h2 negotiation).
// ForceAttemptHTTP2 is disabled and TLS handshake is skipped.
func (l *Listener) Transport() *http.Transport {
	return &http.Transport{
		DialContext:       l.DialContext,
		ForceAttemptHTTP2: false,
		// Skip TLS — this is in-memory, there's nothing to encrypt.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

// HTTPClient returns an *http.Client that dials through this in-memory listener.
// Use this as the connect.HTTPClient argument to ConnectRPC client constructors.
func (l *Listener) HTTPClient() *http.Client {
	return &http.Client{
		Transport: l.Transport(),
	}
}

// memAddr is a dummy net.Addr for the in-memory listener.
type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "mem://in-process" }
