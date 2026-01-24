package testharness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Harness manages server and client processes for integration testing.
type Harness struct {
	ServerCmd string
	ClientCmd string
	Addr      string
	Env       []string
}

// Run starts the server, runs the client, and ensures cleanup.
func (h *Harness) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	fmt.Fprintf(os.Stderr, "Starting server: %s\n", h.ServerCmd)
	serverCmd := exec.CommandContext(ctx, h.ServerCmd)
	serverCmd.Env = append(os.Environ(), h.Env...)
	serverCmd.Stdout = os.Stdout
	serverCmd.Stderr = os.Stderr

	if err := serverCmd.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Ensure server is killed on exit
	defer func() {
		fmt.Fprintf(os.Stderr, "Stopping server...\n")
		cancel() // Signal context cancellation
		serverCmd.Process.Signal(os.Interrupt)
		// Wait up to 5s for graceful shutdown
		time.Sleep(5 * time.Second)
		serverCmd.Process.Kill() // Force kill if still running
	}()

	// Wait for server to be ready
	fmt.Fprintf(os.Stderr, "Waiting for server to be ready...\n")
	if err := h.waitForReady(ctx, 10*time.Second); err != nil {
		return fmt.Errorf("server did not become ready: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Server ready\n")

	// Run client
	fmt.Fprintf(os.Stderr, "Running client: %s\n", h.ClientCmd)
	clientCmd := exec.CommandContext(ctx, h.ClientCmd)
	clientCmd.Env = append(os.Environ(), h.Env...)
	clientCmd.Stdout = os.Stdout
	clientCmd.Stderr = os.Stderr

	if err := clientCmd.Run(); err != nil {
		return fmt.Errorf("client failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Client completed successfully\n")
	return nil
}

// waitForReady polls the server until it responds or timeout.
func (h *Harness) waitForReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := h.Addr
	if addr == "" {
		addr = "localhost:8080"
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for server: %w", ctx.Err())
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				// Server is listening, make sure it's responding to HTTP
				return h.checkHTTP(addr)
			}
		}
	}
}

// checkHTTP makes a simple HTTP request to verify server is responding.
func (h *Harness) checkHTTP(addr string) error {
	url := "http://" + addr + "/"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Any response is fine - server is up
	return nil
}
