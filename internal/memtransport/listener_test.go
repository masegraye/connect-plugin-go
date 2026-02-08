package memtransport_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/masegraye/connect-plugin-go/internal/memtransport"
)

func TestNew(t *testing.T) {
	ln := memtransport.New()
	if ln == nil {
		t.Fatal("New() returned nil")
	}
	ln.Close()
}

func TestAddr(t *testing.T) {
	ln := memtransport.New()
	defer ln.Close()

	addr := ln.Addr()
	if addr.Network() != "mem" {
		t.Errorf("Network() = %q, want %q", addr.Network(), "mem")
	}
	if addr.String() != "mem://in-process" {
		t.Errorf("String() = %q, want %q", addr.String(), "mem://in-process")
	}
}

func TestBasicRoundTrip(t *testing.T) {
	ln := memtransport.New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello from memtransport")
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	client := ln.HTTPClient()
	resp, err := client.Get("http://mem/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from memtransport" {
		t.Errorf("body = %q, want %q", body, "hello from memtransport")
	}
}

func TestPostWithBody(t *testing.T) {
	ln := memtransport.New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "echo: %s", body)
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	client := ln.HTTPClient()
	resp, err := client.Post("http://mem/echo", "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "echo: ping" {
		t.Errorf("body = %q, want %q", body, "echo: ping")
	}
}

func TestHeaderPropagation(t *testing.T) {
	ln := memtransport.New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back a request header as a response header.
		val := r.Header.Get("X-Test-Header")
		w.Header().Set("X-Echo", val)
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	client := ln.HTTPClient()
	req, _ := http.NewRequest("GET", "http://mem/headers", nil)
	req.Header.Set("X-Test-Header", "memtransport-value")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Echo"); got != "memtransport-value" {
		t.Errorf("X-Echo = %q, want %q", got, "memtransport-value")
	}
}

func TestConcurrentConnections(t *testing.T) {
	ln := memtransport.New()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Small delay to force overlapping requests.
		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "id=%s", r.URL.Query().Get("id"))
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	client := ln.HTTPClient()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := client.Get(fmt.Sprintf("http://mem/concurrent?id=%d", id))
			if err != nil {
				errs <- fmt.Errorf("request %d: %w", id, err)
				return
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			expected := fmt.Sprintf("id=%d", id)
			if string(body) != expected {
				errs <- fmt.Errorf("request %d: body = %q, want %q", id, body, expected)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestListenerClose_AcceptReturnsError(t *testing.T) {
	ln := memtransport.New()
	ln.Close()

	_, err := ln.Accept()
	if err == nil {
		t.Fatal("Accept() after Close() should return error")
	}
}

func TestListenerClose_DialReturnsError(t *testing.T) {
	ln := memtransport.New()
	ln.Close()

	_, err := ln.DialContext(context.Background(), "", "")
	if err == nil {
		t.Fatal("DialContext() after Close() should return error")
	}
}

func TestListenerClose_Idempotent(t *testing.T) {
	ln := memtransport.New()

	// Close multiple times should not panic.
	if err := ln.Close(); err != nil {
		t.Errorf("first Close() error: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Errorf("second Close() error: %v", err)
	}
}

func TestDialContext_Cancelled(t *testing.T) {
	ln := memtransport.New()
	defer ln.Close()

	// Fill the connection buffer so DialContext blocks.
	for i := 0; i < 16; i++ {
		ln.DialContext(context.Background(), "", "")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := ln.DialContext(ctx, "", "")
	if err == nil {
		t.Fatal("DialContext() with cancelled context should return error")
	}
}

func TestDialContext_ProducesWorkingConn(t *testing.T) {
	ln := memtransport.New()
	defer ln.Close()

	// Dial from client side.
	go func() {
		clientConn, err := ln.DialContext(context.Background(), "", "")
		if err != nil {
			t.Errorf("DialContext failed: %v", err)
			return
		}
		clientConn.Write([]byte("hello"))
		clientConn.Close()
	}()

	// Accept from server side.
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	defer serverConn.Close()

	buf := make([]byte, 64)
	n, _ := serverConn.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Errorf("read = %q, want %q", buf[:n], "hello")
	}
}

func TestListenerImplementsInterface(t *testing.T) {
	var _ net.Listener = memtransport.New()
}

func TestHTTPClientImplementsConnectHTTPClient(t *testing.T) {
	ln := memtransport.New()
	defer ln.Close()

	// connect.HTTPClient is: interface{ Do(*http.Request) (*http.Response, error) }
	// *http.Client satisfies this.
	type httpClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	var _ httpClient = ln.HTTPClient()
}

func TestServerShutdown_GracefulDrain(t *testing.T) {
	ln := memtransport.New()

	requestStarted := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		// Simulate slow request.
		time.Sleep(100 * time.Millisecond)
		fmt.Fprint(w, "done")
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)

	client := ln.HTTPClient()

	// Start a request.
	var resp *http.Response
	var reqErr error
	done := make(chan struct{})
	go func() {
		resp, reqErr = client.Get("http://mem/slow")
		close(done)
	}()

	// Wait for handler to start, then shutdown.
	<-requestStarted
	srv.Shutdown(context.Background())

	// The in-flight request should complete.
	<-done
	if reqErr != nil {
		t.Fatalf("in-flight request failed: %v", reqErr)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "done" {
		t.Errorf("body = %q, want %q", body, "done")
	}
}
