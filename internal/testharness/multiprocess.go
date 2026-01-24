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

// MultiProcessHarness manages multiple service processes for integration testing.
// Ensures proper startup ordering and cleanup.
type MultiProcessHarness struct {
	Processes []ProcessConfig
}

// ProcessConfig defines a process to run.
type ProcessConfig struct {
	Name    string   // Process name for logging
	Cmd     string   // Binary path
	Args    []string // Command line arguments
	Env     []string // Environment variables
	Port    int      // Port to wait for (0 = don't wait)
	WaitFor string   // Process name to wait for before starting
}

// Run starts all processes in order and ensures cleanup.
func (h *MultiProcessHarness) Run(ctx context.Context) error {
	started := make([]*exec.Cmd, 0, len(h.Processes))

	// Cleanup all processes on exit
	defer func() {
		fmt.Fprintf(os.Stderr, "Cleaning up %d processes...\n", len(started))
		for i := len(started) - 1; i >= 0; i-- {
			cmd := started[i]
			if cmd != nil && cmd.Process != nil {
				cmd.Process.Signal(os.Interrupt)
			}
		}
		// Wait for graceful shutdown
		time.Sleep(500 * time.Millisecond)
		// Force kill any remaining
		for _, cmd := range started {
			if cmd != nil && cmd.Process != nil {
				cmd.Process.Kill()
			}
		}
	}()

	// Start processes
	for i, cfg := range h.Processes {
		fmt.Fprintf(os.Stderr, "[%d/%d] Starting %s...\n", i+1, len(h.Processes), cfg.Name)

		procCmd := exec.CommandContext(ctx, cfg.Cmd, cfg.Args...)
		procCmd.Env = append(os.Environ(), cfg.Env...)
		procCmd.Stdout = os.Stdout
		procCmd.Stderr = os.Stderr

		if err := procCmd.Start(); err != nil {
			return fmt.Errorf("failed to start %s: %w", cfg.Name, err)
		}
		started = append(started, procCmd)

		// Wait for port if configured
		if cfg.Port > 0 {
			if err := h.waitForPort(ctx, cfg.Port, 5*time.Second); err != nil {
				return fmt.Errorf("%s did not start: %w", cfg.Name, err)
			}
			fmt.Fprintf(os.Stderr, "  %s ready on port %d\n", cfg.Name, cfg.Port)
		} else {
			// Small delay for startup
			time.Sleep(200 * time.Millisecond)
		}
	}

	fmt.Fprintf(os.Stderr, "All %d processes started successfully\n", len(started))
	return nil
}

// waitForPort polls until the port is listening or timeout.
func (h *MultiProcessHarness) waitForPort(ctx context.Context, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := fmt.Sprintf("localhost:%d", port)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for port %d: %w", port, ctx.Err())
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				// Port is listening, make sure HTTP is responding
				return h.checkHTTP(addr)
			}
		}
	}
}

// checkHTTP makes a simple HTTP request to verify server is responding.
func (h *MultiProcessHarness) checkHTTP(addr string) error {
	url := "http://" + addr + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Any response is fine - server is up
	return nil
}
