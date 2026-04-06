package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestService creates a Service wired to a temp log dir.
func newTestService(t *testing.T, command, restart string) *Service {
	t.Helper()
	return &Service{
		Name:    "test",
		Command: command,
		Restart: restart,
		logPath: filepath.Join(t.TempDir(), "test.log"),
	}
}

// startTestServer sets up supervisor and socket in temp paths, returns the listener.
// Caller must close the listener in t.Cleanup.
func startTestServer(t *testing.T, services map[string]*Service) net.Listener {
	t.Helper()

	dir := t.TempDir()

	origSocket := socketPath
	origLogDir := logDir
	origSupervisor := supervisor
	socketPath = filepath.Join(dir, "section3.sock")
	logDir = dir
	t.Cleanup(func() {
		socketPath = origSocket
		logDir = origLogDir
		supervisorMu.Lock()
		supervisor = origSupervisor
		supervisorMu.Unlock()
	})

	sup := NewSupervisor()
	for name, svc := range services {
		svc.Name = name
		svc.logPath = filepath.Join(dir, name+".log")
		sup.services[name] = svc
		sup.serviceKeys = append(sup.serviceKeys, name)
	}

	supervisorMu.Lock()
	supervisor = sup
	supervisorMu.Unlock()

	ln, err := serveSocket()
	if err != nil {
		t.Fatalf("serveSocket: %v", err)
	}
	t.Cleanup(func() {
		ln.Close()
		os.Remove(socketPath)
	})

	return ln
}

// send sends a command to the test socket and returns the response.
func send(t *testing.T, cmd string) string {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	fmt.Fprintln(conn, cmd)
	conn.(*net.UnixConn).CloseWrite()

	out, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimRight(string(out), "\n")
}

// =============================================================================
// Service lifecycle unit tests (no socket)
// =============================================================================

func TestShouldRestart(t *testing.T) {
	crashErr := fmt.Errorf("exit status 1")

	cases := []struct {
		restart string
		err     error
		want    bool
	}{
		{"always", nil, true},
		{"always", crashErr, true},
		{"never", nil, false},
		{"never", crashErr, false},
		{"on-crash", nil, false},
		{"on-crash", crashErr, true},
	}
	for _, c := range cases {
		svc := &Service{Restart: c.restart}
		got := svc.shouldRestart(c.err)
		if got != c.want {
			t.Errorf("restart=%q err=%v: got %v, want %v", c.restart, c.err, got, c.want)
		}
	}
}

func TestServiceStartStop(t *testing.T) {
	svc := newTestService(t, "sleep 60", "never")

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	svc.mu.Lock()
	running := svc.running
	svc.mu.Unlock()
	if !running {
		t.Fatal("expected running=true after Start()")
	}

	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() did not return within 10s")
	}

	svc.mu.Lock()
	running = svc.running
	svc.mu.Unlock()
	if running {
		t.Error("expected running=false after Stop()")
	}
}

func TestServiceRestartOnCrash(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")

	svc := newTestService(t, fmt.Sprintf("echo x >> %s", countFile), "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	time.Sleep(2500 * time.Millisecond)

	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("could not read count file: %v", err)
	}
	count := bytes.Count(data, []byte("x"))
	if count < 2 {
		t.Errorf("expected at least 2 starts, got %d", count)
	}
}

func TestServiceNeverRestart(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")

	svc := newTestService(t, fmt.Sprintf("echo x >> %s", countFile), "never")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	time.Sleep(1500 * time.Millisecond)

	data, _ := os.ReadFile(countFile)
	count := bytes.Count(data, []byte("x"))
	if count != 1 {
		t.Errorf("expected exactly 1 start, got %d", count)
	}
}

func TestServiceOnCrashPolicy(t *testing.T) {
	t.Run("non-zero exit restarts", func(t *testing.T) {
		dir := t.TempDir()
		countFile := filepath.Join(dir, "count")
		svc := newTestService(t, fmt.Sprintf("echo x >> %s && exit 1", countFile), "on-crash")
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		defer svc.Stop()
		time.Sleep(2500 * time.Millisecond)
		data, _ := os.ReadFile(countFile)
		if bytes.Count(data, []byte("x")) < 2 {
			t.Errorf("expected at least 2 starts for non-zero exit")
		}
	})

	t.Run("zero exit does not restart", func(t *testing.T) {
		dir := t.TempDir()
		countFile := filepath.Join(dir, "count")
		svc := newTestService(t, fmt.Sprintf("echo x >> %s", countFile), "on-crash")
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		defer svc.Stop()
		time.Sleep(1500 * time.Millisecond)
		data, _ := os.ReadFile(countFile)
		if bytes.Count(data, []byte("x")) != 1 {
			t.Errorf("expected exactly 1 start for zero exit")
		}
	})
}

// TestStopDuringBackoff verifies Stop() is not blocked by the backoff sleep.
// Before the fix, wait() held s.mu during time.Sleep, so Stop() would block.
func TestStopDuringBackoff(t *testing.T) {
	svc := newTestService(t, "exit 1", "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond) // let the first crash register

	start := time.Now()
	svc.Stop()
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("Stop() took %v; likely blocked by backoff sleep (want < 2s)", elapsed)
	}
}

func TestBackoffIncreases(t *testing.T) {
	svc := newTestService(t, "exit 1", "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	time.Sleep(2500 * time.Millisecond)

	svc.mu.Lock()
	backoff := svc.backoff
	crashes := svc.crashCount
	svc.mu.Unlock()

	if crashes < 1 {
		t.Fatalf("expected at least 1 crash, got %d", crashes)
	}
	if backoff < 1*time.Second {
		t.Errorf("backoff = %v after %d crash(es); want >= 1s", backoff, crashes)
	}
}

func TestStatus(t *testing.T) {
	t.Run("never started", func(t *testing.T) {
		svc := &Service{Name: "svc", logPath: filepath.Join(t.TempDir(), "svc.log")}
		s := svc.Status()
		if !strings.Contains(s, "stopped") || !strings.Contains(s, "never started") {
			t.Errorf("unexpected status: %q", s)
		}
	})

	t.Run("running", func(t *testing.T) {
		svc := newTestService(t, "sleep 60", "never")
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		defer svc.Stop()
		s := svc.Status()
		if !strings.Contains(s, "running") || !strings.Contains(s, "PID") {
			t.Errorf("expected 'running' and 'PID', got: %q", s)
		}
	})

	t.Run("stopped after crash", func(t *testing.T) {
		svc := newTestService(t, "exit 1", "never")
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		defer svc.Stop()
		time.Sleep(300 * time.Millisecond)
		if strings.Contains(svc.Status(), "running") {
			t.Errorf("service should not show 'running' after crash")
		}
	})
}

// =============================================================================
// Config and log tests
// =============================================================================

func TestLoadConfig(t *testing.T) {
	yml := `
services:
  worker:
    command: /usr/bin/worker
    restart: on-crash
  web:
    command: /usr/bin/web serve
    restart: always
`
	f, err := os.CreateTemp(t.TempDir(), "section3-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(yml)
	f.Close()

	orig := configPath
	configPath = f.Name()
	t.Cleanup(func() { configPath = orig })

	sup := NewSupervisor()
	if err := sup.LoadConfig(); err != nil {
		t.Fatal(err)
	}

	if len(sup.services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(sup.services))
	}
	web := sup.services["web"]
	if web == nil {
		t.Fatal("missing 'web'")
	}
	if web.Command != "/usr/bin/web serve" {
		t.Errorf("web.Command = %q", web.Command)
	}
	if web.Restart != "always" {
		t.Errorf("web.Restart = %q", web.Restart)
	}
	if sup.serviceKeys[0] != "web" || sup.serviceKeys[1] != "worker" {
		t.Errorf("serviceKeys not sorted: %v", sup.serviceKeys)
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()

	origLogDir := logDir
	logDir = dir
	t.Cleanup(func() { logDir = origLogDir })

	logPath := filepath.Join(dir, "svc.log")
	big := bytes.Repeat([]byte("a"), maxLogSize+1)
	if err := os.WriteFile(logPath, big, 0644); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Name: "svc", Command: "true", Restart: "never", logPath: logPath}
	if err := svc.OpenLog(); err != nil {
		t.Fatal(err)
	}
	svc.logFile.Close()

	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error("expected rotated log at " + logPath + ".1")
	}

	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Errorf("new log should be empty after rotation, got size %d", fi.Size())
	}
}

// =============================================================================
// Socket tests
// =============================================================================

func TestSocketStatus(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"web": svc})

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	out := send(t, "status")
	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' in status, got: %q", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("expected service name 'web', got: %q", out)
	}
}

func TestSocketStatusNamed(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"web": svc})

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	// Named service
	out := send(t, "status web")
	if !strings.Contains(out, "running") {
		t.Errorf("status web: expected 'running', got: %q", out)
	}

	// Unknown service
	out = send(t, "status bogus")
	if !strings.HasPrefix(out, "ERROR:") {
		t.Errorf("expected ERROR for unknown service, got: %q", out)
	}
}

func TestSocketStartStop(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"web": svc})

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	// Stop via socket
	out := send(t, "stop web")
	if !strings.Contains(out, "stopped web") {
		t.Errorf("expected 'stopped web', got: %q", out)
	}

	// Status should show stopped
	out = send(t, "status web")
	if strings.Contains(out, "running") {
		t.Errorf("expected stopped status after stop, got: %q", out)
	}

	// Start via socket
	out = send(t, "start web")
	if !strings.Contains(out, "started web") {
		t.Errorf("expected 'started web', got: %q", out)
	}
	defer svc.Stop()

	// Status should show running
	out = send(t, "status web")
	if !strings.Contains(out, "running") {
		t.Errorf("expected running status after start, got: %q", out)
	}
}

func TestSocketStartAlreadyRunning(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"web": svc})

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	out := send(t, "start web")
	if !strings.HasPrefix(out, "ERROR:") {
		t.Errorf("expected ERROR when starting already-running service, got: %q", out)
	}
}

func TestSocketRestart(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"web": svc})

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	svc.mu.Lock()
	pidBefore := svc.cmd.Process.Pid
	svc.mu.Unlock()

	out := send(t, "restart web")
	if !strings.Contains(out, "restarted web") {
		t.Errorf("expected 'restarted web', got: %q", out)
	}

	// Give the new process a moment to start
	time.Sleep(100 * time.Millisecond)

	svc.mu.Lock()
	pidAfter := svc.cmd.Process.Pid
	svc.mu.Unlock()

	if pidBefore == pidAfter {
		t.Errorf("PID did not change after restart (%d == %d)", pidBefore, pidAfter)
	}
}

func TestSocketReload(t *testing.T) {
	origConfig := configPath
	t.Cleanup(func() { configPath = origConfig })

	// Initial config: only "alpha"
	writeConfig := func(yml string) {
		f, err := os.CreateTemp(t.TempDir(), "*.yml")
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(yml)
		f.Close()
		configPath = f.Name()
	}

	writeConfig(`
services:
  alpha:
    command: sleep 60
    restart: never
`)

	alphaDir := t.TempDir()
	alpha := &Service{Command: "sleep 60", Restart: "never", logPath: filepath.Join(alphaDir, "alpha.log")}

	dir := t.TempDir()
	origSocket := socketPath
	origLogDir := logDir
	origSupervisor := supervisor
	socketPath = filepath.Join(dir, "section3.sock")
	logDir = dir
	t.Cleanup(func() {
		socketPath = origSocket
		logDir = origLogDir
		supervisorMu.Lock()
		supervisor = origSupervisor
		supervisorMu.Unlock()
	})

	sup := NewSupervisor()
	sup.services["alpha"] = alpha
	alpha.Name = "alpha"
	sup.serviceKeys = []string{"alpha"}
	supervisorMu.Lock()
	supervisor = sup
	supervisorMu.Unlock()

	ln, err := serveSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(socketPath) })

	if err := alpha.Start(); err != nil {
		t.Fatal(err)
	}

	// Swap config to only "beta"
	writeConfig(`
services:
  beta:
    command: sleep 60
    restart: never
`)

	out := send(t, "reload")
	if !strings.Contains(out, "reloaded") {
		t.Errorf("expected 'reloaded', got: %q", out)
	}

	supervisorMu.RLock()
	newSup := supervisor
	supervisorMu.RUnlock()

	if _, ok := newSup.services["alpha"]; ok {
		t.Error("alpha should have been removed after reload")
	}
	beta, ok := newSup.services["beta"]
	if !ok {
		t.Error("beta should be present after reload")
	}
	_ = beta
}

func TestSocketTail(t *testing.T) {
	svc := &Service{Command: "sleep 60", Restart: "never"}
	startTestServer(t, map[string]*Service{"svc": svc})

	// Write log content after setup so we use the path assigned by startTestServer.
	if err := os.WriteFile(svc.logPath, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := send(t, "tail -n 2 svc")
	if !strings.Contains(out, "line2") || !strings.Contains(out, "line3") {
		t.Errorf("expected last 2 lines, got: %q", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("line1 should have been excluded by -n 2, got: %q", out)
	}
}

func TestSocketUnknownCommand(t *testing.T) {
	startTestServer(t, map[string]*Service{})

	out := send(t, "bogus")
	if !strings.HasPrefix(out, "ERROR:") {
		t.Errorf("expected ERROR for unknown command, got: %q", out)
	}
}

func TestSocketSingleInstance(t *testing.T) {
	startTestServer(t, map[string]*Service{})

	// The socket is already listening. A second dial should succeed (daemon "running").
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("expected socket to be accessible: %v", err)
	}
	conn.Close()

	// runDaemon would call os.Exit(1) if it could connect — verify the check logic.
	// We test it by just confirming the dial succeeds, which is what the check uses.
}
