package manager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Qovra/hytale-daemon/internal/config"
	"github.com/Qovra/hytale-daemon/internal/database"
	"github.com/Qovra/hytale-daemon/internal/logger"
)

// State enum
type State string

const (
	StateStopped State = "STOPPED"
	StateRunning State = "RUNNING"
	StateCrashed State = "CRASHED"
)

// ServerManager supervises a single isolated Hytale-Proxy child process inside this Node.
type ServerManager struct {
	cfg          *config.DaemonConfig
	ID           string
	Name         string
	port         int
	allocatedRAM int
	nodeIP       string
	version      string
	serverType   string // "proxy" or "game"
	hostname     string
	
	mu           sync.Mutex
	desiredState State
	actualState  State
	cmd          *exec.Cmd
	stdoutBuf    *ringBuffer
	startTime    time.Time
}

// NewServerManager instantiates a struct to manage a specific server process.
func NewServerManager(cfg *config.DaemonConfig, id, name string, port, ram int, nodeIP, version, sType, hostname string) *ServerManager {
	return &ServerManager{
		cfg:          cfg,
		ID:           id,
		Name:         name,
		port:         port,
		allocatedRAM: ram,
		nodeIP:       nodeIP,
		version:      version,
		serverType:   sType,
		hostname:     hostname,
		desiredState: StateStopped,
		actualState:  StateStopped,
		stdoutBuf:    newRingBuffer(1024 * 64), 
	}
}

// Start launches the proxy if it's not already running.
func (sm *ServerManager) Start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.actualState == StateRunning {
		return errors.New("server is already running")
	}

	sm.desiredState = StateRunning
	return sm.spawn()
}

func (sm *ServerManager) WorkDir() string {
	// We use Name-ID to ensure uniqueness while fulfilling user request for descriptive folders
	workDir := filepath.Join("data", "servers", sm.ID)
	if sm.Name != "" && sm.ID != "00000000-0000-0000-0000-000000005520" {
		slugName := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(sm.Name, "-"))
		workDir = filepath.Join("data", "servers", fmt.Sprintf("%s-%s", slugName, sm.ID))
	}
	return workDir
}

// spawn internal assumes mutex is locked.
func (sm *ServerManager) spawn() error {
	// A real pterodactyl equivalent would copy the base template to a volume folder
	// and run it chrooted/dockerized. For this daemon, we'll run the binary directly
	// but specifying its own isolated config file.
	
	// Ensure isolated execution folder exists
	workDir := sm.WorkDir()
	_ = os.MkdirAll(workDir, 0755)

	binaryPath := sm.cfg.ProxyBinary
	var args []string

	if sm.serverType == "game" {
		// Hytale Server Logic (Official Specs)
		// 1. Determine execution directory and asset location via recursive search
		var jarPath string
		var jarExecDir string
		
		// We look for HytaleServer.jar in workDir and up to 2 levels deep
		filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || jarPath != "" { return nil }
			if !info.IsDir() && info.Name() == "HytaleServer.jar" {
				rel, _ := filepath.Rel(workDir, path)
				depth := len(strings.Split(rel, string(os.PathSeparator)))
				if depth <= 3 { // root is 1, subfolder is 2, sub-sub is 3
					jarPath = path
					jarExecDir = filepath.Dir(path)
				}
			}
			return nil
		})

		if jarPath == "" {
			return fmt.Errorf("HytaleServer.jar not discovered in %s (searched up to 2 levels deep). Please verify installation logs.", workDir)
		}

		execDir := jarExecDir
		assetsPath := "Assets.zip"
		
		// Try to find Assets.zip relative to the JAR
		if _, err := os.Stat(filepath.Join(execDir, "Assets.zip")); err == nil {
			assetsPath = "Assets.zip"
		} else if _, err := os.Stat(filepath.Join(filepath.Dir(execDir), "Assets.zip")); err == nil {
			assetsPath = "../Assets.zip"
		}

		binaryPath = "java"
		
		// 2. Build official arguments
		args = []string{
			fmt.Sprintf("-Xmx%dM", sm.allocatedRAM),
			"-Xms128M",
		}

		// Enable AOT Cache if file exists in the JAR directory
		if _, err := os.Stat(filepath.Join(execDir, "HytaleServer.aot")); err == nil {
			args = append(args, "-XX:AOTCache=HytaleServer.aot")
		}

		args = append(args, "-jar", "HytaleServer.jar")
		args = append(args, "--assets", assetsPath)
		args = append(args, "--bind", fmt.Sprintf("0.0.0.0:%d", sm.port))
		args = append(args, "--auth-mode", "authenticated")

		// Update command execution directory
		workDir = execDir
	} else {
		// Default Proxy behavior
		args = []string{"-config", "config.json"}
	}
	
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = workDir // Critical: run in its isolated folder

	// We use a multi-writer/scanner to BOTH capture logs in the buffer AND detect Auth codes
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	
	if err := cmd.Start(); err != nil {
		sm.actualState = StateCrashed
		return fmt.Errorf("failed to spawn server process %s: %w", sm.ID, err)
	}

	// Regex for OAuth2 detection
	reURL := regexp.MustCompile(`https?://[a-zA-Z0-9./?=_-]+`)
	reCode := regexp.MustCompile(`[A-Z0-9]{4}-[A-Z0-9]{4}`)

	// Scanner to detect auth links and pipe to buffer
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			sm.WriteLog(line)

			// Detect Auth URL/Code
			url := reURL.FindString(line)
			code := reCode.FindString(line)
			if (url != "" || code != "") && sm.nodeCfg != nil {
				// Report to backend as "installation" data but for a running server
				go func(u, c string) {
					// We reuse the installer's reporting logic essentially
					installer := NewInstaller(sm.nodeCfg.NodeID, sm.nodeCfg.BackendURL, sm.nodeCfg.APIToken, nil)
					// State is "running" because this is server identity auth, not installer auth
					installer.reportProgress(sm.ID, 100, false, "running", u, c)
				}(url, code)
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			sm.WriteLog("[ERR] " + scanner.Text())
		}
	}()

	sm.cmd = cmd
	sm.actualState = StateRunning
	sm.startTime = time.Now()
	
	log.Printf("[server-%s] spawned successfully mapping port %d (PID %d)", sm.ID, sm.port, sm.cmd.Process.Pid)

	// Sync states to DB
	if sm.ID != "00000000-0000-0000-0000-000000005520" {
		sm.updateDatabaseState("running")
		logger.LogEvent(context.Background(), "info", "server", sm.ID, "server.start", "Server started successfully", nil)
	}

	// Goroutine to wait on process and auto-restart if desired
	go sm.monitor(cmd)

	return nil
}

// monitor waits for the process to exit and reacts based on desired state.
func (sm *ServerManager) monitor(cmd *exec.Cmd) {
	err := cmd.Wait()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// If this is no longer the active command (e.g. manual restart race condition)
	if sm.cmd != cmd {
		return
	}

	exitCode := cmd.ProcessState.ExitCode()
	log.Printf("[server-%s] process exited with code %d. Err: %v", sm.ID, exitCode, err)
	sm.cmd = nil

	if sm.desiredState == StateStopped {
		sm.actualState = StateStopped
		if sm.ID != "00000000-0000-0000-0000-000000005520" {
			sm.updateDatabaseState("stopped")
			logger.LogEvent(context.Background(), "info", "server", sm.ID, "server.stop", "Server stopped cleanly", nil)
		}
		return
	}

	// Unexpected crash
	sm.actualState = StateCrashed
	if sm.ID != "00000000-0000-0000-0000-000000005520" {
		sm.updateDatabaseState("crashed")
		logger.LogEvent(context.Background(), "error", "server", sm.ID, "server.crashed", fmt.Sprintf("Server crashed unexpectedly with code %d", exitCode), nil)
	}

	log.Printf("[server-%s] crashed! Attempting to respawn in 3 seconds...", sm.ID)

	go func() {
		time.Sleep(3 * time.Second)
		sm.mu.Lock()
		defer sm.mu.Unlock()
		if sm.desiredState == StateRunning && sm.cmd == nil {
			log.Printf("[server-%s] performing auto-restart...", sm.ID)
			if err := sm.spawn(); err != nil {
				log.Printf("[server-%s] auto-restart failed: %v", sm.ID, err)
			}
		}
	}()
}

func (sm *ServerManager) Stop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.desiredState == StateStopped {
		return errors.New("server is not running")
	}

	sm.desiredState = StateStopped
	
	if sm.cmd != nil && sm.cmd.Process != nil {
		log.Printf("[server-%s] sending SIGTERM to PID %d", sm.ID, sm.cmd.Process.Pid)
		if err := sm.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			sm.cmd.Process.Kill()
		}
	} else {
		// ALready crashed / not running, force it to stopped
		sm.actualState = StateStopped
		if sm.ID != "00000000-0000-0000-0000-000000005520" {
			sm.updateDatabaseState("stopped")
			logger.LogEvent(context.Background(), "info", "server", sm.ID, "server.stop", "Server stopped cleanly", nil)
		}
	}

	return nil
}

// Restart is a helper to stop and immediately start again.
func (sm *ServerManager) Restart() error {
	_ = sm.Stop()
	time.Sleep(1 * time.Second)
	return sm.Start()
}

// updateDatabaseState wraps the context execution
func (sm *ServerManager) updateDatabaseState(status string) {
	if database.Pool == nil || sm.ID == "00000000-0000-0000-0000-000000005520" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	_, err := database.Pool.Exec(ctx, "UPDATE servers SET status = $1, updated_at = NOW() WHERE id = $2", status, sm.ID)
	if err != nil {
		log.Printf("[server-%s] DB state update failed: %v", sm.ID, err)
	}
}

// StatusOutput represents the current state.
type StatusOutput struct {
	DesiredState string `json:"desired_state"`
	ActualState  string `json:"actual_state"`
	PID          int    `json:"pid,omitempty"`
	Uptime       string `json:"uptime,omitempty"`
}

func (sm *ServerManager) Status() StatusOutput {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	out := StatusOutput{
		DesiredState: string(sm.desiredState),
		ActualState:  string(sm.actualState),
	}

	if sm.actualState == StateRunning && sm.cmd != nil && sm.cmd.Process != nil {
		out.PID = sm.cmd.Process.Pid
		out.Uptime = time.Since(sm.startTime).Round(time.Second).String()
	}
	return out
}

func (sm *ServerManager) WriteLog(line string) {
	if sm.stdoutBuf == nil { return }
	if !strings.HasSuffix(line, "\n") { line += "\n" }
	sm.stdoutBuf.Write([]byte(line))
}

// GetLogs returns the accumulated stdout/stderr lines as a string.
func (sm *ServerManager) GetLogs() string {
	return sm.stdoutBuf.String()
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
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(bytes.ToValidUTF8(r.buf, []byte{}))
}
