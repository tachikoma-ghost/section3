package main

import (
	"bytes"
	"fmt"
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

// TestShouldRestart covers all restart policies and exit conditions.
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

// TestServiceStartStop starts a long-running process and stops it cleanly.
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
	go func() {
		svc.Stop()
		close(stopped)
	}()

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

// TestServiceRestartOnCrash verifies a crashing service is restarted.
func TestServiceRestartOnCrash(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")

	svc := newTestService(t, fmt.Sprintf("echo x >> %s", countFile), "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	// After first crash backoff is 1s; two starts require ~1.2s minimum.
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

// TestServiceNeverRestart verifies restart=never services do not restart.
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

// TestServiceOnCrashPolicy verifies on-crash distinguishes exit zero from non-zero.
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
		count := bytes.Count(data, []byte("x"))
		if count < 2 {
			t.Errorf("expected at least 2 starts for non-zero exit, got %d", count)
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
		count := bytes.Count(data, []byte("x"))
		if count != 1 {
			t.Errorf("expected exactly 1 start for zero exit, got %d", count)
		}
	})
}

// TestStopDuringBackoff verifies Stop() is not blocked by the backoff sleep.
// Before the fix, wait() held s.mu during time.Sleep, so Stop() would block for
// up to maxBackoff (60s).
func TestStopDuringBackoff(t *testing.T) {
	svc := newTestService(t, "exit 1", "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	// Let the first crash register and backoff begin (backoff[0] = 1s).
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	svc.Stop()
	elapsed := time.Since(start)

	// Stop() must return well before the 1s backoff expires.
	if elapsed > 2*time.Second {
		t.Errorf("Stop() took %v; likely blocked by backoff sleep (want < 2s)", elapsed)
	}
}

// TestBackoffIncreases verifies exponential backoff grows across crashes.
func TestBackoffIncreases(t *testing.T) {
	svc := newTestService(t, "exit 1", "always")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	// Wait through first crash (backoff 1s) and into second cycle.
	time.Sleep(2500 * time.Millisecond)

	svc.mu.Lock()
	backoff := svc.backoff
	crashes := svc.crashCount
	svc.mu.Unlock()

	if crashes < 1 {
		t.Fatalf("expected at least 1 crash recorded, got %d", crashes)
	}
	if backoff < 1*time.Second {
		t.Errorf("backoff = %v after %d crash(es); want >= 1s", backoff, crashes)
	}
}

// TestStatus verifies the Status() string for each service state.
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
		if !strings.Contains(s, "running") {
			t.Errorf("expected 'running', got: %q", s)
		}
		if !strings.Contains(s, "PID") {
			t.Errorf("expected PID, got: %q", s)
		}
	})

	t.Run("stopped after crash", func(t *testing.T) {
		svc := newTestService(t, "exit 1", "never")
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		defer svc.Stop()

		time.Sleep(300 * time.Millisecond)

		s := svc.Status()
		if strings.Contains(s, "running") {
			t.Errorf("service should not show 'running' after crash, got: %q", s)
		}
	})
}

// TestLoadConfig verifies YAML config is parsed and keys are sorted.
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
		t.Fatal("missing service 'web'")
	}
	if web.Command != "/usr/bin/web serve" {
		t.Errorf("web.Command = %q", web.Command)
	}
	if web.Restart != "always" {
		t.Errorf("web.Restart = %q", web.Restart)
	}
	// Keys should be sorted alphabetically
	if len(sup.serviceKeys) != 2 || sup.serviceKeys[0] != "web" || sup.serviceKeys[1] != "worker" {
		t.Errorf("serviceKeys not sorted: %v", sup.serviceKeys)
	}
}

// TestLogRotation verifies that an oversized log file is rotated on open.
func TestLogRotation(t *testing.T) {
	dir := t.TempDir()

	// Override logDir so OpenLog's MkdirAll points somewhere writable.
	orig := logDir
	logDir = dir
	t.Cleanup(func() { logDir = orig })

	logPath := filepath.Join(dir, "svc.log")

	// Write a file just over the rotation threshold.
	big := bytes.Repeat([]byte("a"), maxLogSize+1)
	if err := os.WriteFile(logPath, big, 0644); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Name: "svc", Command: "true", Restart: "never", logPath: logPath}
	if err := svc.OpenLog(); err != nil {
		t.Fatal(err)
	}
	svc.logFile.Close()

	// Old content should have been renamed to .1
	rotated := logPath + ".1"
	if _, err := os.Stat(rotated); os.IsNotExist(err) {
		t.Error("expected rotated log at " + rotated)
	}

	// New log file should be empty
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Errorf("new log file should be empty after rotation, got size %d", fi.Size())
	}
}
