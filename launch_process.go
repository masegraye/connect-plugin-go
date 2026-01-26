package connectplugin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ProcessStrategy launches plugins as child processes.
// Plugins run as separate OS processes and self-register with the host.
type ProcessStrategy struct {
	mu        sync.Mutex
	processes map[string]*exec.Cmd
}

// NewProcessStrategy creates a new process-based launch strategy.
func NewProcessStrategy() *ProcessStrategy {
	return &ProcessStrategy{
		processes: make(map[string]*exec.Cmd),
	}
}

// Name returns the strategy name.
func (s *ProcessStrategy) Name() string {
	return "process"
}

// Launch starts a plugin binary as a child process.
func (s *ProcessStrategy) Launch(ctx context.Context, spec PluginSpec) (string, func(), error) {
	if spec.BinaryPath == "" {
		return "", nil, fmt.Errorf("BinaryPath required for process strategy")
	}

	// 1. Start plugin binary as child process
	hostURL := spec.HostURL
	if hostURL == "" {
		hostURL = "http://localhost:8080"  // Default
	}

	cmd := exec.CommandContext(ctx, spec.BinaryPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", spec.Port),
		fmt.Sprintf("HOST_URL=%s", hostURL),  // Unmanaged: plugin connects to host
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("failed to start process %s: %w", spec.BinaryPath, err)
	}

	s.mu.Lock()
	s.processes[spec.Name] = cmd
	s.mu.Unlock()

	// 2. Wait for plugin to be ready (listening on port)
	endpoint := fmt.Sprintf("http://localhost:%d", spec.Port)
	if err := waitForPluginReady(endpoint, 5*time.Second); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return "", nil, fmt.Errorf("plugin %s didn't become ready: %w", spec.Name, err)
	}

	// 3. Return endpoint and cleanup function
	cleanup := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Graceful shutdown attempt
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
			time.Sleep(100 * time.Millisecond)
			cmd.Process.Kill()
			cmd.Wait()
		}

		delete(s.processes, spec.Name)
	}

	return endpoint, cleanup, nil
}

// waitForPluginReady polls until the plugin endpoint is ready or timeout.
func waitForPluginReady(endpoint string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Try to connect to port
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for plugin: %w", ctx.Err())
		case <-ticker.C:
			// Try TCP connection
			conn, err := net.DialTimeout("tcp", endpoint[7:], 100*time.Millisecond) // Strip "http://"
			if err == nil {
				conn.Close()
				// Port is open, verify HTTP responds
				return checkHTTPReady(endpoint)
			}
		}
	}
}

// checkHTTPReady makes a simple HTTP request to verify server is responding.
func checkHTTPReady(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Any response means server is up (even 404)
	return nil
}
