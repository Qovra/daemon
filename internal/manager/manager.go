package manager

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/Qovra/hytale-daemon/internal/config"
)

// State enum
type State string

const (
	StateStopped State = "STOPPED"
	StateRunning State = "RUNNING"
	StateCrashed State = "CRASHED"
)

// Manager supervises the Hytale-Proxy child process.
type Manager struct {
	cfg          *config.DaemonConfig
	mu           sync.Mutex
	desiredState State
	actualState  State
	cmd          *exec.Cmd
	stdoutBuf    *ringBuffer
	startTime    time.Time
}

// New creates a new Manager instance.
func New(cfg *config.DaemonConfig) *Manager {
	return &Manager{
		cfg:          cfg,
		desiredState: StateStopped,
		actualState:  StateStopped,
		stdoutBuf:    newRingBuffer(1024 * 64), // 64kb of log history max
	}
}

// Start launches the proxy if it's not already running.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.actualState == StateRunning {
		return errors.New("proxy is already running")
	}

	m.desiredState = StateRunning
	return m.spawn()
}

// spawn internal assumes mutex is locked.
func (m *Manager) spawn() error {
	m.cmd = exec.Command(m.cfg.ProxyBinary, m.cfg.ProxyArgs...)
	
	// Tee output so we capture logs dynamically
	m.cmd.Stdout = m.stdoutBuf
	m.cmd.Stderr = m.stdoutBuf

	if err := m.cmd.Start(); err != nil {
		m.actualState = StateCrashed
		return fmt.Errorf("failed to spawn proxy: %w", err)
	}

	m.actualState = StateRunning
	m.startTime = time.Now()
	log.Printf("[manager] proxy spawned successfully with PID %d", m.cmd.Process.Pid)

	// Goroutine to wait on process and auto-restart if desired
	go m.monitor(m.cmd)

	return nil
}

// monitor waits for the process to exit. If it wasn't requested to stop,
// it tries to auto-restart the process.
func (m *Manager) monitor(cmd *exec.Cmd) {
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// If this is no longer the active command (e.g. we restarted manually), do nothing.
	if m.cmd != cmd {
		return
	}

	exitCode := cmd.ProcessState.ExitCode()
	log.Printf("[manager] proxy process exited with code %d. Err: %v", exitCode, err)

	m.cmd = nil

	if m.desiredState == StateStopped {
		m.actualState = StateStopped
		log.Printf("[manager] proxy shut down cleanly by user request")
		return
	}

	// Unexpected crash
	m.actualState = StateCrashed
	log.Printf("[manager] proxy crashed unexpectedly! Attempting to respawn in 3 seconds...")

	// Launch respawn in background so we don't hold the mutex
	go func() {
		time.Sleep(3 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.desiredState == StateRunning && m.cmd == nil {
			log.Printf("[manager] performing auto-restart...")
			if err := m.spawn(); err != nil {
				log.Printf("[manager] auto-restart failed: %v", err)
			}
		}
	}()
}

// Stop sends SIGTERM to the proxy and disables auto-restart.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.desiredState == StateStopped {
		return errors.New("proxy is not running")
	}

	m.desiredState = StateStopped
	
	if m.cmd != nil && m.cmd.Process != nil {
		log.Printf("[manager] sending SIGTERM to proxy PID %d", m.cmd.Process.Pid)
		// On windows this will fail, but since we target macOS/Debian syscall.SIGTERM is correct.
		if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			// Fallback
			m.cmd.Process.Kill()
		}
	}

	return nil
}

// Restart is a helper to stop and immediately start again.
func (m *Manager) Restart() error {
	_ = m.Stop()
	time.Sleep(1 * time.Second) // allow graceful shutdown before starting again
	return m.Start()
}

// StatusOutput represents the current Daemon/Proxy health state.
type StatusOutput struct {
	DesiredState string `json:"desired_state"`
	ActualState  string `json:"actual_state"`
	PID          int    `json:"pid,omitempty"`
	Uptime       string `json:"uptime,omitempty"`
}

// Status returns a thread-safe snapshot of the proxy process status.
func (m *Manager) Status() StatusOutput {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := StatusOutput{
		DesiredState: string(m.desiredState),
		ActualState:  string(m.actualState),
	}

	if m.actualState == StateRunning && m.cmd != nil && m.cmd.Process != nil {
		out.PID = m.cmd.Process.Pid
		out.Uptime = time.Since(m.startTime).Round(time.Second).String()
	}

	return out
}

// GetLogs returns the most recent captured standard output of the proxy.
func (m *Manager) GetLogs() string {
	return m.stdoutBuf.String()
}

// --- simple concurrency-safe ring buffer for logs ---

type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max, buf: make([]byte, 0, max)}
}

func (r *ringBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		// slice the newest `max` bytes
		r.buf = r.buf[len(r.buf)-r.max:]
		// this slice allocation could be optimized into a circular buffer,
		// but simple slice manipulation is fast enough for <1MB log histories.
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(bytes.ToValidUTF8(r.buf, []byte{}))
}
