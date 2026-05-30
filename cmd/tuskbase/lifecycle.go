package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

const (
	systemdUnitName              = "tuskbase.service"
	launchdLabel                 = "com.tuskbase.daemon"
	windowsTaskName              = "TuskbaseDaemon"
	defaultReadyWait             = 5 * time.Second
	defaultLockWait              = 10 * time.Second
	defaultPollPeriod            = 100 * time.Millisecond
	serviceExecutableInstallHint = "install Tuskbase from a release/package or run a stable built binary before enabling autostart"
)

var (
	errServiceUnsupported  = errors.New("user service backend unsupported")
	newLifecycleController = func() lifecycleController { return newDefaultLifecycleController() }
	currentExecutable      = os.Executable
)

type lifecycleController interface {
	EnsureReady(context.Context, userConfig) error
	InstallAndStart(context.Context, userConfig) lifecycleResult
	Uninstall(context.Context, userConfig) lifecycleResult
	Restart(context.Context, userConfig) lifecycleResult
	Status(context.Context, userConfig) lifecycleStatus
}

type lifecycleResult struct {
	Backend  string
	State    string
	LogPath  string
	Detail   string
	Degraded bool
	Err      error
}

type lifecycleStatus struct {
	Backend     string
	State       string
	Autostart   string
	LogPath     string
	Detail      string
	Health      *daemon.Health
	HealthError error
}

type userServiceManager interface {
	Backend() string
	Install(context.Context, userConfig) error
	Uninstall(context.Context, userConfig) error
	Start(context.Context, userConfig) error
	Restart(context.Context, userConfig) error
	Status(context.Context, userConfig) serviceStatus
}

type serviceStatus struct {
	Backend   string
	State     string
	Autostart string
	Detail    string
}

type lifecycleLock interface {
	Unlock() error
}

type defaultLifecycleController struct {
	client        *http.Client
	services      userServiceManager
	startDetached func(context.Context, userConfig, string) error
	acquireLock   func(context.Context) (lifecycleLock, error)
	readyTimeout  time.Duration
	pollPeriod    time.Duration
	logPath       func() string
}

func newDefaultLifecycleController() *defaultLifecycleController {
	return &defaultLifecycleController{
		client:        &http.Client{Timeout: 750 * time.Millisecond},
		services:      osUserServiceManager{goos: runtime.GOOS},
		startDetached: startDetachedDaemon,
		acquireLock: func(ctx context.Context) (lifecycleLock, error) {
			path, err := lifecycleLockPath()
			if err != nil {
				return nil, err
			}
			return acquireFileLifecycleLock(ctx, path, defaultLockWait)
		},
		readyTimeout: defaultReadyWait,
		pollPeriod:   defaultPollPeriod,
		logPath:      defaultDaemonLogPath,
	}
}

func (c *defaultLifecycleController) EnsureReady(ctx context.Context, cfg userConfig) error {
	cfg = normalizedDaemonConfig(cfg)
	if err := requireLoopback(cfg.Addr); err != nil {
		return fmt.Errorf("refusing to auto-start non-loopback Tuskbase daemon at %s: %w", cfg.Addr, err)
	}
	if health, err := c.readyHealth(ctx, cfg.Addr); err == nil {
		_ = health
		return nil
	} else if isBadHealth(err) {
		return fmt.Errorf("Tuskbase daemon health check failed at http://%s/healthz: %w; log: %s", cfg.Addr, err, c.logPath())
	}

	lock, err := c.acquireLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire Tuskbase daemon start lock: %w", err)
	}
	defer lock.Unlock()

	if _, err := c.readyHealth(ctx, cfg.Addr); err == nil {
		return nil
	} else if isBadHealth(err) {
		return fmt.Errorf("Tuskbase daemon health check failed at http://%s/healthz: %w; log: %s", cfg.Addr, err, c.logPath())
	}

	return c.startOrWake(ctx, cfg)
}

func (c *defaultLifecycleController) InstallAndStart(ctx context.Context, cfg userConfig) lifecycleResult {
	cfg = normalizedDaemonConfig(cfg)
	result := lifecycleResult{Backend: c.services.Backend(), LogPath: c.logPath()}
	if !cfg.daemonAutostartEnabled() {
		result.State = "skipped"
		result.Detail = "autostart disabled"
		return result
	}
	if err := requireLoopback(cfg.Addr); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		result.Detail = "bridge self-start remains available for loopback daemon configs"
		return result
	}
	if err := c.services.Install(ctx, cfg); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		result.Detail = "autostart install failed; bridge self-start remains available"
		return result
	}
	if err := c.services.Start(ctx, cfg); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		result.Detail = "autostart installed but start failed; bridge self-start remains available"
		return result
	}
	if _, err := c.waitReady(ctx, cfg.Addr); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		result.Detail = "service was started but readiness timed out"
		return result
	}
	result.State = "running"
	result.Detail = "autostart installed and daemon ready"
	return result
}

func (c *defaultLifecycleController) Uninstall(ctx context.Context, cfg userConfig) lifecycleResult {
	cfg = normalizedDaemonConfig(cfg)
	result := lifecycleResult{Backend: c.services.Backend(), LogPath: c.logPath()}
	if err := c.services.Uninstall(ctx, cfg); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		return result
	}
	result.State = "uninstalled"
	return result
}

func (c *defaultLifecycleController) Restart(ctx context.Context, cfg userConfig) lifecycleResult {
	cfg = normalizedDaemonConfig(cfg)
	result := lifecycleResult{Backend: c.services.Backend(), LogPath: c.logPath()}
	if err := requireLoopback(cfg.Addr); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		return result
	}
	if err := c.services.Restart(ctx, cfg); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		return result
	}
	if _, err := c.waitReady(ctx, cfg.Addr); err != nil {
		result.State = "degraded"
		result.Degraded = true
		result.Err = err
		return result
	}
	result.State = "running"
	result.Detail = "daemon restarted and ready"
	return result
}

func (c *defaultLifecycleController) Status(ctx context.Context, cfg userConfig) lifecycleStatus {
	cfg = normalizedDaemonConfig(cfg)
	service := c.services.Status(ctx, cfg)
	status := lifecycleStatus{Backend: service.Backend, State: service.State, Autostart: service.Autostart, Detail: service.Detail, LogPath: c.logPath()}
	if health, err := c.readyHealth(ctx, cfg.Addr); err != nil {
		status.HealthError = err
	} else {
		status.Health = &health
	}
	return status
}

func (c *defaultLifecycleController) startOrWake(ctx context.Context, cfg userConfig) error {
	logPath := c.logPath()
	serviceErr := c.services.Start(ctx, cfg)
	if serviceErr == nil {
		if _, err := c.waitReady(ctx, cfg.Addr); err == nil {
			return nil
		} else if isBadHealth(err) {
			return fmt.Errorf("Tuskbase daemon health check failed at http://%s/healthz: %w; log: %s", cfg.Addr, err, logPath)
		} else {
			serviceErr = fmt.Errorf("service start accepted but daemon did not become ready at http://%s/healthz: %w", cfg.Addr, err)
		}
	} else if _, err := c.readyHealth(ctx, cfg.Addr); err == nil {
		return nil
	} else if isBadHealth(err) {
		return fmt.Errorf("Tuskbase daemon health check failed at http://%s/healthz: %w; log: %s", cfg.Addr, err, logPath)
	}

	if err := c.startDetached(ctx, cfg, logPath); err != nil {
		return fmt.Errorf("start Tuskbase daemon via %s: %v; detached fallback: %w; log: %s", c.services.Backend(), serviceErr, err, logPath)
	}
	if _, err := c.waitReady(ctx, cfg.Addr); err != nil {
		if isBadHealth(err) {
			return fmt.Errorf("Tuskbase daemon health check failed at http://%s/healthz: %w; log: %s", cfg.Addr, err, logPath)
		}
		return fmt.Errorf("Tuskbase daemon did not become ready at http://%s/healthz: %w; log: %s", cfg.Addr, err, logPath)
	}
	return nil
}

func (c *defaultLifecycleController) waitReady(ctx context.Context, addr string) (daemon.Health, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, c.readyTimeout)
	defer cancel()
	var lastErr error
	for {
		health, err := c.readyHealth(deadlineCtx, addr)
		if err == nil {
			return health, nil
		}
		if isBadHealth(err) {
			return daemon.Health{}, err
		}
		lastErr = err
		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return daemon.Health{}, lastErr
			}
			return daemon.Health{}, deadlineCtx.Err()
		case <-time.After(c.pollPeriod):
		}
	}
}

func (c *defaultLifecycleController) readyHealth(ctx context.Context, addr string) (daemon.Health, error) {
	client := c.client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return daemon.Health{}, badHealthError{err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return daemon.Health{}, daemonDownError{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return daemon.Health{}, badHealthError{err: fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	var health daemon.Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return daemon.Health{}, badHealthError{err: fmt.Errorf("invalid health response: %w", err)}
	}
	if health.Status != "ok" {
		return daemon.Health{}, badHealthError{err: fmt.Errorf("status %q", health.Status)}
	}
	if !health.MCP {
		return daemon.Health{}, badHealthError{err: errors.New("MCP surface is disabled")}
	}
	return health, nil
}

type daemonDownError struct{ err error }

func (e daemonDownError) Error() string { return e.err.Error() }
func (e daemonDownError) Unwrap() error { return e.err }

type badHealthError struct{ err error }

func (e badHealthError) Error() string { return e.err.Error() }
func (e badHealthError) Unwrap() error { return e.err }

func isBadHealth(err error) bool {
	var bad badHealthError
	return errors.As(err, &bad)
}

func normalizedDaemonConfig(cfg userConfig) userConfig {
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = modeLocalBasic
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = defaultAddr
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		cfg.DBPath = defaultDBPath()
	}
	applyDaemonDefaults(&cfg)
	return cfg
}

func printLifecycleResult(w io.Writer, label string, result lifecycleResult) {
	state := result.State
	if state == "" {
		state = "unknown"
	}
	fmt.Fprintf(w, "%s: %s", label, state)
	if result.Backend != "" {
		fmt.Fprintf(w, " backend=%s", result.Backend)
	}
	if result.Detail != "" {
		fmt.Fprintf(w, " detail=%q", result.Detail)
	}
	if result.Err != nil {
		fmt.Fprintf(w, " error=%q", result.Err.Error())
	}
	fmt.Fprintln(w)
	if result.LogPath != "" {
		fmt.Fprintf(w, "%s_log: %s\n", label, result.LogPath)
	}
}

func printLifecycleStatus(w io.Writer, status lifecycleStatus) {
	if status.Health != nil {
		fmt.Fprintf(w, "daemon: running\n")
		fmt.Fprintf(w, "health: %s\n", status.Health.Status)
		fmt.Fprintf(w, "mcp: %t\n", status.Health.MCP)
		fmt.Fprintf(w, "rest: %t\n", status.Health.REST)
		fmt.Fprintf(w, "auth_policy: %s\n", status.Health.AuthPolicy)
		fmt.Fprintf(w, "auth_source: %s\n", status.Health.AuthSource)
	} else {
		fmt.Fprintf(w, "daemon: down\n")
		if status.HealthError != nil {
			fmt.Fprintf(w, "health_error: %s\n", status.HealthError)
		}
	}
	fmt.Fprintf(w, "service_backend: %s\n", status.Backend)
	fmt.Fprintf(w, "service_state: %s\n", emptyDefault(status.State, "unknown"))
	fmt.Fprintf(w, "service_autostart: %s\n", emptyDefault(status.Autostart, "unknown"))
	if status.Detail != "" {
		fmt.Fprintf(w, "service_detail: %s\n", status.Detail)
	}
	if status.LogPath != "" {
		fmt.Fprintf(w, "log_path: %s\n", status.LogPath)
	}
}

func emptyDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type fileLifecycleLock struct {
	path string
	file *os.File
}

func acquireFileLifecycleLock(ctx context.Context, path string, timeout time.Duration) (lifecycleLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "pid=%d created_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			return fileLifecycleLock{path: path, file: file}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > timeout {
			_ = os.Remove(path)
			continue
		}
		select {
		case <-deadlineCtx.Done():
			return nil, deadlineCtx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (l fileLifecycleLock) Unlock() error {
	if l.file != nil {
		_ = l.file.Close()
	}
	return os.Remove(l.path)
}

func lifecycleLockPath() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	return filepath.Join(root, "tuskbase", "daemon.lock"), nil
}

func defaultDaemonLogPath() string {
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	return filepath.Join(root, "tuskbase", "daemon.log")
}

func startDetachedDaemon(ctx context.Context, cfg userConfig, logPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exe, err := currentExecutable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	args := daemonServeArgs(cfg)
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	closeErr := logFile.Close()
	releaseErr := cmd.Process.Release()
	if releaseErr != nil {
		return errors.Join(closeErr, releaseErr)
	}
	return closeErr
}

func daemonServeArgs(cfg userConfig) []string {
	cfg = normalizedDaemonConfig(cfg)
	args := []string{"serve"}
	if cfg.daemonMCPEnabled() {
		args = append(args, "--http-mcp")
	}
	if cfg.daemonRESTEnabled() {
		args = append(args, "--rest")
	}
	args = append(args, "--addr", cfg.Addr, "--db", cfg.DBPath)
	return args
}

type osUserServiceManager struct {
	goos string
}

func (m osUserServiceManager) Backend() string {
	switch m.goos {
	case "linux":
		return "systemd-user"
	case "darwin":
		return "launchd"
	case "windows":
		return "windows-scheduled-task"
	default:
		return "unsupported"
	}
}

func (m osUserServiceManager) Install(ctx context.Context, cfg userConfig) error {
	switch m.goos {
	case "linux":
		return installSystemdUserService(ctx, cfg)
	case "darwin":
		return installLaunchAgent(ctx, cfg)
	case "windows":
		return installWindowsScheduledTask(ctx, cfg)
	default:
		return errServiceUnsupported
	}
}

func (m osUserServiceManager) Uninstall(ctx context.Context, cfg userConfig) error {
	switch m.goos {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return err
		}
		_ = runServiceCommand(ctx, "systemctl", "--user", "disable", "--now", systemdUnitName)
		path, err := systemdUnitPath()
		if err == nil {
			_ = os.Remove(path)
		}
		return runServiceCommand(ctx, "systemctl", "--user", "daemon-reload")
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			return err
		}
		_ = runServiceCommand(ctx, "launchctl", "remove", launchdLabel)
		path, err := launchAgentPath()
		if err == nil {
			_ = os.Remove(path)
		}
		return nil
	case "windows":
		if _, err := exec.LookPath("schtasks"); err != nil {
			return err
		}
		return runServiceCommand(ctx, "schtasks", "/Delete", "/TN", windowsTaskName, "/F")
	default:
		_ = cfg
		return errServiceUnsupported
	}
}

func (m osUserServiceManager) Start(ctx context.Context, cfg userConfig) error {
	switch m.goos {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return err
		}
		return runServiceCommand(ctx, "systemctl", "--user", "start", systemdUnitName)
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			return err
		}
		path, err := launchAgentPath()
		if err != nil {
			return err
		}
		return runServiceCommand(ctx, "launchctl", "load", "-w", path)
	case "windows":
		if _, err := exec.LookPath("schtasks"); err != nil {
			return err
		}
		return runServiceCommand(ctx, "schtasks", "/Run", "/TN", windowsTaskName)
	default:
		_ = cfg
		return errServiceUnsupported
	}
}

func (m osUserServiceManager) Restart(ctx context.Context, cfg userConfig) error {
	switch m.goos {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return err
		}
		return runServiceCommand(ctx, "systemctl", "--user", "restart", systemdUnitName)
	case "darwin":
		if err := m.Uninstall(ctx, cfg); err != nil {
			return err
		}
		if err := m.Install(ctx, cfg); err != nil {
			return err
		}
		return m.Start(ctx, cfg)
	case "windows":
		if _, err := exec.LookPath("schtasks"); err != nil {
			return err
		}
		_ = runServiceCommand(ctx, "schtasks", "/End", "/TN", windowsTaskName)
		return m.Start(ctx, cfg)
	default:
		return errServiceUnsupported
	}
}

func (m osUserServiceManager) Status(ctx context.Context, cfg userConfig) serviceStatus {
	status := serviceStatus{Backend: m.Backend(), State: "unknown", Autostart: "unknown"}
	switch m.goos {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			status.State = "unavailable"
			status.Autostart = "degraded"
			status.Detail = err.Error()
			return status
		}
		status.State = trimCommandOutput(ctx, "systemctl", "--user", "is-active", systemdUnitName)
		status.Autostart = trimCommandOutput(ctx, "systemctl", "--user", "is-enabled", systemdUnitName)
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			status.State = "unavailable"
			status.Autostart = "degraded"
			status.Detail = err.Error()
			return status
		}
		status.State = trimCommandOutput(ctx, "launchctl", "list", launchdLabel)
		status.Autostart = "installed-if-plist-exists"
	case "windows":
		if _, err := exec.LookPath("schtasks"); err != nil {
			status.State = "unavailable"
			status.Autostart = "degraded"
			status.Detail = err.Error()
			return status
		}
		status.State = trimCommandOutput(ctx, "schtasks", "/Query", "/TN", windowsTaskName)
		status.Autostart = "logon-task"
	default:
		_ = cfg
		status.State = "fallback"
		status.Autostart = "unsupported"
		status.Detail = "bridge can start a detached daemon when needed"
	}
	return status
}

func renderSystemdUnitForService(cfg userConfig) (string, error) {
	exe, err := serviceExecutablePath()
	if err != nil {
		return "", err
	}
	return renderSystemdUnit(exe, cfg), nil
}

func renderLaunchAgentPlistForService(cfg userConfig) (string, error) {
	exe, err := serviceExecutablePath()
	if err != nil {
		return "", err
	}
	return renderLaunchAgentPlist(exe, cfg), nil
}

func renderWindowsScheduledTaskCommandForService(cfg userConfig) ([]string, error) {
	exe, err := serviceExecutablePath()
	if err != nil {
		return nil, err
	}
	return renderWindowsScheduledTaskCommand(exe, cfg), nil
}

func serviceExecutablePath() (string, error) {
	exe, err := currentExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve Tuskbase executable for autostart: %w; %s", err, serviceExecutableInstallHint)
	}
	return validateServiceExecutablePath(exe)
}

func validateServiceExecutablePath(exe string) (string, error) {
	if strings.TrimSpace(exe) == "" {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: current executable path is empty; %s", serviceExecutableInstallHint)
	}
	clean, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: resolve %q: %w; %s", exe, err, serviceExecutableInstallHint)
	}
	if isGoBuildPath(clean) || isGoTestExecutable(clean) {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: current executable %q looks like a temporary Go build artifact; %s", clean, serviceExecutableInstallHint)
	}
	if !isTuskbaseExecutableName(clean) {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: current executable %q is not named tuskbase; %s", clean, serviceExecutableInstallHint)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: current executable %q is not available: %w; %s", clean, err, serviceExecutableInstallHint)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", fmt.Errorf("autostart requires a stable installed tuskbase executable: current executable %q is not a regular file; %s", clean, serviceExecutableInstallHint)
	}
	return clean, nil
}

func isGoBuildPath(path string) bool {
	for _, part := range strings.Split(normalizedExecutablePath(path), "/") {
		if strings.HasPrefix(strings.ToLower(part), "go-build") {
			return true
		}
	}
	return false
}

func isGoTestExecutable(path string) bool {
	return strings.HasSuffix(strings.ToLower(serviceExecutableBase(path)), ".test")
}

func isTuskbaseExecutableName(path string) bool {
	base := strings.ToLower(serviceExecutableBase(path))
	return base == "tuskbase" || base == "tuskbase.exe"
}

func serviceExecutableBase(path string) string {
	normalized := normalizedExecutablePath(path)
	idx := strings.LastIndex(normalized, "/")
	if idx < 0 {
		return normalized
	}
	return normalized[idx+1:]
}

func normalizedExecutablePath(path string) string {
	return strings.ReplaceAll(filepath.Clean(path), "\\", "/")
}

func installSystemdUserService(ctx context.Context, cfg userConfig) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return err
	}
	unit, err := renderSystemdUnitForService(cfg)
	if err != nil {
		return err
	}
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(unit), 0o600); err != nil {
		return err
	}
	if err := runServiceCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return runServiceCommand(ctx, "systemctl", "--user", "enable", systemdUnitName)
}

func installLaunchAgent(ctx context.Context, cfg userConfig) error {
	if _, err := exec.LookPath("launchctl"); err != nil {
		return err
	}
	plist, err := renderLaunchAgentPlistForService(cfg)
	if err != nil {
		return err
	}
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		return err
	}
	return nil
}

func installWindowsScheduledTask(ctx context.Context, cfg userConfig) error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return err
	}
	args, err := renderWindowsScheduledTaskCommandForService(cfg)
	if err != nil {
		return err
	}
	return runServiceCommand(ctx, args[0], args[1:]...)
}

func systemdUnitPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "systemd", "user", systemdUnitName), nil
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func renderSystemdUnit(exe string, cfg userConfig) string {
	parts := append([]string{exe}, daemonServeArgs(cfg)...)
	for i, part := range parts {
		parts[i] = systemdArg(part)
	}
	return fmt.Sprintf(`[Unit]
Description=Tuskbase local daemon
After=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, strings.Join(parts, " "))
}

func systemdArg(value string) string {
	if strings.ContainsAny(value, " \t\n\"'") {
		return strconv.Quote(value)
	}
	return value
}

func renderLaunchAgentPlist(exe string, cfg userConfig) string {
	args := append([]string{exe}, daemonServeArgs(cfg)...)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + launchdLabel + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range args {
		b.WriteString("    <string>" + html.EscapeString(arg) + "</string>\n")
	}
	b.WriteString(`  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
`)
	return b.String()
}

func renderWindowsScheduledTaskCommand(exe string, cfg userConfig) []string {
	command := windowsCommandLine(append([]string{exe}, daemonServeArgs(cfg)...))
	return []string{"schtasks", "/Create", "/TN", windowsTaskName, "/SC", "ONLOGON", "/RL", "LIMITED", "/TR", command, "/F"}
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = strconv.Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func runServiceCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func trimCommandOutput(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return text
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "ok"
	}
	return text
}
