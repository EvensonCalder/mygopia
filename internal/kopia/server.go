package kopia

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mygopia/internal/config"
)

const maxServerLogLines = 300

// DesktopNotificationPayload is emitted by kopia server when --kopiaui-notifications is enabled.
type DesktopNotificationPayload struct {
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Severity int    `json:"severity"`
}

// ServerRunner manages the lifecycle of a local `kopia server start` child process for one repository config.
type ServerRunner struct {
	configFile string
	logDir     string
	onNotify   func(DesktopNotificationPayload)

	startMu    sync.Mutex
	mu         sync.RWMutex
	kopiaBin   string
	rcloneBin  string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *Client
	address    string
	certSHA256 string
	running    bool
	logs       []string
	exitErr    error
	doneCh     chan struct{}
}

// NewServerRunner creates a runner bound to a specific Kopia .config path.
func NewServerRunner(kopiaBin, configFile, logDir string, onNotify func(DesktopNotificationPayload)) *ServerRunner {
	return &ServerRunner{
		kopiaBin:   kopiaBin,
		configFile: configFile,
		logDir:     logDir,
		onNotify:   onNotify,
	}
}

// SetKopiaBinary updates the kopia binary path for subsequent server starts.
func (s *ServerRunner) SetKopiaBinary(bin string) {
	s.mu.Lock()
	s.kopiaBin = bin
	s.mu.Unlock()
}

// SetRcloneBinary updates the optional rclone binary path so its directory is injected into PATH.
func (s *ServerRunner) SetRcloneBinary(bin string) {
	s.mu.Lock()
	s.rcloneBin = bin
	s.mu.Unlock()
}

// Start launches `kopia server start` and waits until its ephemeral HTTPS API is ready.
func (s *ServerRunner) Start(ctx context.Context) (*Client, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.RLock()
	if s.running && s.client != nil {
		cli := s.client
		s.mu.RUnlock()
		return cli, nil
	}
	bin := s.kopiaBin
	rcloneBin := s.rcloneBin
	s.mu.RUnlock()

	if err := config.EnsurePrivateDir(filepath.Dir(s.configFile)); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}

	args := []string{
		"server", "start",
		"--ui",
		"--control-api",
		"--tls-generate-cert",
		"--tls-generate-cert-name=127.0.0.1",
		"--tls-print-server-cert",
		"--random-password",
		"--random-server-control-password",
		"--disable-csrf-token-checks",
		"--async-repo-connect",
		"--kopiaui-notifications",
		"--shutdown-on-stdin",
		"--address=127.0.0.1:0",
		"--config-file=" + s.configFile,
	}
	if s.logDir != "" {
		if err := config.EnsurePrivateDir(s.logDir); err == nil {
			args = append(args, "--log-dir="+s.logDir)
		}
	}

	cmd := exec.Command(bin, args...)
	configureSubprocess(cmd)
	cmd.Env = config.SubprocessEnv(bin, rcloneBin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start kopia server: %w", err)
	}

	doneCh := make(chan struct{})
	readyCh := make(chan struct{})
	notifySem := make(chan struct{}, 4)
	var readyOnce sync.Once

	var (
		paramMu  sync.Mutex
		isReady  bool
		srvAddr  string
		srvPass  string
		ctrlPass string
		certPEM  []byte
		certSHA  string
	)

	checkReady := func() {
		if srvAddr != "" && srvPass != "" && ctrlPass != "" && len(certPEM) > 0 {
			isReady = true
			readyOnce.Do(func() { close(readyCh) })
		}
	}

	parseLine := func(line string) {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			return
		}
		if key, val, ok := strings.Cut(trimmed, ": "); ok {
			switch key {
			case "SERVER PASSWORD":
				paramMu.Lock()
				if !isReady {
					srvPass = val
					checkReady()
				}
				paramMu.Unlock()
				s.appendLog("SERVER PASSWORD: [redacted]")
				return
			case "SERVER CONTROL PASSWORD":
				paramMu.Lock()
				if !isReady {
					ctrlPass = val
					checkReady()
				}
				paramMu.Unlock()
				s.appendLog("SERVER CONTROL PASSWORD: [redacted]")
				return
			case "SERVER CERT SHA256":
				paramMu.Lock()
				if !isReady {
					certSHA = strings.TrimSpace(val)
				}
				paramMu.Unlock()
				s.appendLog(trimmed)
				return
			case "SERVER CERTIFICATE":
				decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
				paramMu.Lock()
				if !isReady && decErr == nil {
					certPEM = decoded
					checkReady()
				}
				paramMu.Unlock()
				s.appendLog("SERVER CERTIFICATE: [pinned]")
				return
			case "SERVER ADDRESS":
				paramMu.Lock()
				if !isReady {
					srvAddr = strings.TrimSpace(val)
					checkReady()
				}
				paramMu.Unlock()
				s.appendLog(trimmed)
				return
			case "NOTIFICATION":
				if s.onNotify != nil {
					var n DesktopNotificationPayload
					if err := json.Unmarshal([]byte(val), &n); err == nil {
						select {
						case notifySem <- struct{}{}:
							go func(payload DesktopNotificationPayload) {
								defer func() { <-notifySem }()
								s.onNotify(payload)
							}(n)
						default:
						}
					}
				}
				return
			}
		}
		s.appendLog(trimmed)
	}

	var pipeWG sync.WaitGroup
	scanPipe := func(r io.Reader) {
		defer pipeWG.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for sc.Scan() {
			parseLine(sc.Text())
		}
	}

	pipeWG.Add(2)
	go scanPipe(stderr)
	go scanPipe(stdout)

	go func() {
		pipeWG.Wait()
		waitErr := cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
			s.running = false
			s.exitErr = waitErr
			s.client = nil
		}
		s.mu.Unlock()
		close(doneCh)
	}()

	killAndWait := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-doneCh
	}

	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	select {
	case <-readyCh:
	case <-doneCh:
		s.mu.RLock()
		wErr := s.exitErr
		s.mu.RUnlock()
		return nil, fmt.Errorf("kopia server exited during startup: %w", wErr)
	case <-startCtx.Done():
		killAndWait()
		return nil, fmt.Errorf("timed out waiting for kopia server startup")
	}

	paramMu.Lock()
	addr, pass, cPass, pemBytes, sha := srvAddr, srvPass, ctrlPass, certPEM, certSHA
	paramMu.Unlock()

	normAddr := addr
	if !strings.Contains(normAddr, "://") {
		normAddr = "https://" + normAddr
	}
	parsedURL, parseErr := url.Parse(normAddr)
	if parseErr != nil || parsedURL.Scheme != "https" || parsedURL.Hostname() != "127.0.0.1" {
		killAndWait()
		return nil, fmt.Errorf("refusing non-loopback or non-HTTPS kopia server address %q", addr)
	}
	if sha != "" {
		block, _ := pem.Decode(pemBytes)
		if block == nil {
			killAndWait()
			return nil, fmt.Errorf("invalid kopia server certificate PEM")
		}
		sum := sha256.Sum256(block.Bytes)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), sha) {
			killAndWait()
			return nil, fmt.Errorf("kopia server certificate SHA256 mismatch")
		}
	}

	cli, err := NewClient(ClientConfig{
		BaseURL:         normAddr,
		Username:        "kopia",
		Password:        pass,
		ControlPassword: cPass,
		ServerCertPEM:   pemBytes,
	})
	if err != nil {
		killAndWait()
		return nil, err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.client = cli
	s.address = addr
	s.certSHA256 = sha
	s.running = true
	s.doneCh = doneCh
	s.exitErr = nil
	s.mu.Unlock()

	return cli, nil
}

// Client returns the active HTTP client if the server is running.
func (s *ServerRunner) Client() *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Running reports whether the underlying kopia server process is alive.
func (s *ServerRunner) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Address returns the bound HTTPS address (e.g. https://127.0.0.1:54321).
func (s *ServerRunner) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.address
}

// CertSHA256 returns the SHA256 fingerprint of the ephemeral server TLS certificate.
func (s *ServerRunner) CertSHA256() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.certSHA256
}

// Logs returns a copy of recent server stderr/stdout lines (with credentials redacted).
func (s *ServerRunner) Logs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.logs))
	copy(out, s.logs)
	return out
}

func (s *ServerRunner) appendLog(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logs) >= maxServerLogLines {
		copy(s.logs, s.logs[1:])
		s.logs[len(s.logs)-1] = line
	} else {
		s.logs = append(s.logs, line)
	}
}

// Stop shuts down the kopia server cleanly via stdin EOF, falling back to Kill after 3 seconds.
func (s *ServerRunner) Stop() {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	cmd := s.cmd
	stdin := s.stdin
	done := s.doneCh
	running := s.running
	s.stdin = nil
	s.client = nil
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if !running || cmd == nil || done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}
}
