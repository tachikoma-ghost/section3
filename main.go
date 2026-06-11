package main

import (
	"bufio"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	configPath = "/workspace/section3.yml"
	logDir     = "/tmp/section3-logs"
	socketPath = "/tmp/section3.sock"
)

const (
	maxBackoff          = 60 * time.Second
	backoffMul          = 2
	startStagger        = 100 * time.Millisecond
	maxLogSize          = 1 * 1024 * 1024 // 1MB
	maxLogBackups       = 5               // rotated copies kept per log (.1 .. .5)
	rotateRetryCooldown = 30 * time.Second
)

// supervisor is the live instance; swapped atomically by reloadConfig.
var (
	supervisor   *Supervisor
	supervisorMu sync.RWMutex
)

// --- Types ---

type Config struct {
	Defaults ServiceConfig            `yaml:"defaults"`
	Services map[string]ServiceConfig `yaml:"services"`
}

type ServiceConfig struct {
	Command   string   `yaml:"command"`
	Dir       string   `yaml:"dir"`
	Restart   string   `yaml:"restart"` // always, never, on-crash
	DependsOn []string `yaml:"depends_on"`
}

type Service struct {
	Name    string
	Command string
	Dir     string
	Restart string
	cmd     *exec.Cmd
	logw    *rotatingWriter
	logPath string

	mu         sync.Mutex
	stopped    bool
	running    bool          // true while process is alive
	done       chan struct{} // closed by wait() when process exits
	copyDone   chan struct{} // closed when the log copy goroutine finishes
	crashCount int
	backoff    time.Duration
	startTime  time.Time
	lastCrash  time.Time
}

type Supervisor struct {
	services    map[string]*Service
	serviceKeys []string // sorted
}

func NewSupervisor() *Supervisor {
	return &Supervisor{services: make(map[string]*Service)}
}

// --- Config ---

func (s *Supervisor) LoadConfig() error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	s.serviceKeys = nil
	for name, sc := range cfg.Services {
		if sc.Dir == "" {
			sc.Dir = cfg.Defaults.Dir
		}
		if sc.Restart == "" {
			sc.Restart = cfg.Defaults.Restart
		}
		s.services[name] = &Service{
			Name:    name,
			Command: sc.Command,
			Dir:     sc.Dir,
			Restart: sc.Restart,
			logPath: filepath.Join(logDir, name+".log"),
		}
		s.serviceKeys = append(s.serviceKeys, name)
	}
	sort.Strings(s.serviceKeys)
	return nil
}

// --- Log ---

// rotatingWriter is an append-only log writer that rotates the file once it
// reaches maxSize, keeping maxBackups renamed copies (.1 is newest). Service
// output is piped through it by the supervisor — the child never holds the
// log fd itself, which is what makes rotation possible while it runs.
type rotatingWriter struct {
	path       string
	maxSize    int64
	maxBackups int

	mu       sync.Mutex
	f        *os.File
	size     int64
	lastFail time.Time
}

func newRotatingWriter(path string) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	w := &rotatingWriter{path: path, maxSize: maxLogSize, maxBackups: maxLogBackups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f = f
	w.size = fi.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	var rotErrs []error
	if w.f == nil && time.Since(w.lastFail) >= rotateRetryCooldown {
		if err := w.open(); err != nil {
			w.lastFail = time.Now()
			rotErrs = append(rotErrs, err)
		}
	}
	if w.f != nil && w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		rotErrs = append(rotErrs, w.rotateLocked()...)
	}
	var n int
	var err error
	if w.f == nil {
		err = fmt.Errorf("log %s is not open", w.path)
	} else {
		n, err = w.f.Write(p)
		w.size += int64(n)
	}
	w.mu.Unlock()

	// Logged after unlock: the daemon's own log is a rotatingWriter, so
	// logging while holding the lock would deadlock on re-entry.
	for _, e := range rotErrs {
		log.Printf("log rotation %s: %v", w.path, e)
	}
	return n, err
}

// rotateLocked shifts path -> .1 -> .2 ... and reopens a fresh file. On any
// failure it backs off for rotateRetryCooldown so a persistent error cannot
// recurse through the daemon's own log writer.
func (w *rotatingWriter) rotateLocked() []error {
	if time.Since(w.lastFail) < rotateRetryCooldown {
		return nil
	}
	var errs []error
	w.f.Close()
	w.f = nil
	for i := w.maxBackups - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(old); err != nil {
			continue
		}
		if err := os.Rename(old, fmt.Sprintf("%s.%d", w.path, i+1)); err != nil {
			errs = append(errs, err)
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		errs = append(errs, err)
	}
	if err := w.open(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		w.lastFail = time.Now()
	}
	return errs
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// drainAndClose gives the log copy goroutine a moment to flush the last
// output, then closes the writer.
func drainAndClose(w *rotatingWriter, copyDone chan struct{}) {
	if w == nil {
		return
	}
	if copyDone != nil {
		select {
		case <-copyDone:
		case <-time.After(time.Second):
		}
	}
	w.Close()
}

// --- Service lifecycle ---

// Start holds s.mu for its full duration so concurrent calls (socket command
// racing the crash-restart loop) cannot double-spawn the process.
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return fmt.Errorf("service is stopped")
	}
	if s.running {
		return fmt.Errorf("already running")
	}

	// The writer survives crash restarts; Stop() closes and clears it.
	if s.logw == nil {
		w, err := newRotatingWriter(s.logPath)
		if err != nil {
			return err
		}
		s.logw = w
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}

	cmd := exec.Command("/bin/sh", "-c", s.Command)
	cmd.Stdout = pw
	cmd.Stderr = pw
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	pw.Close() // the child holds its own copy of the write end

	copyDone := make(chan struct{})
	logw := s.logw
	go func() {
		defer close(copyDone)
		defer pr.Close()
		// ErrClosed just means the writer was closed by Stop() while a
		// straggling descendant still held the pipe.
		if _, err := io.Copy(logw, pr); err != nil && !errors.Is(err, os.ErrClosed) {
			log.Printf("[%s] log copy: %v", s.Name, err)
		}
	}()

	done := make(chan struct{})
	s.cmd = cmd
	s.running = true
	s.done = done
	s.copyDone = copyDone
	s.startTime = time.Now()

	go s.wait(cmd, done)
	return nil
}

func (s *Service) shouldRestart(exitErr error) bool {
	switch s.Restart {
	case "never":
		return false
	case "on-crash":
		return exitErr != nil
	default: // "always"
		return true
	}
}

func (s *Service) wait(cmd *exec.Cmd, done chan struct{}) {
	defer close(done)

	err := cmd.Wait()

	s.mu.Lock()
	s.running = false

	if s.stopped {
		s.mu.Unlock() // Stop() owns log cleanup
		return
	}

	if !s.shouldRestart(err) {
		log.Printf("[%s] exited: %v (not restarting, restart=%s)", s.Name, err, s.Restart)
		logw, copyDone := s.logw, s.copyDone
		s.logw = nil
		s.mu.Unlock()
		drainAndClose(logw, copyDone)
		return
	}

	s.lastCrash = time.Now()
	if s.backoff == 0 {
		s.backoff = 1 * time.Second
	} else {
		s.backoff *= backoffMul
		if s.backoff > maxBackoff {
			s.backoff = maxBackoff
		}
	}
	s.crashCount++
	backoff := s.backoff
	crashCount := s.crashCount

	s.mu.Unlock()

	log.Printf("[%s] exited: %v (restarting in %v, crash #%d)", s.Name, err, backoff, crashCount)
	time.Sleep(backoff)

	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()

	if stopped {
		return
	}

	if err := s.Start(); err != nil {
		log.Printf("[%s] restart failed: %v", s.Name, err)
	}
}

func (s *Service) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.backoff = 0
	s.crashCount = 0
	cmd := s.cmd
	done := s.done
	logw := s.logw
	copyDone := s.copyDone
	s.logw = nil
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// Kill the whole group: a lone Process.Kill leaves
				// grandchildren alive, still holding the log pipe.
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
	}

	drainAndClose(logw, copyDone)
	return nil
}

func (s *Service) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		if s.lastCrash.IsZero() {
			return fmt.Sprintf("%-12s stopped  (never started)", s.Name)
		}
		ago := time.Since(s.lastCrash).Round(time.Second)
		return fmt.Sprintf("%-12s stopped  exit    last crash %s ago", s.Name, ago)
	}

	uptime := time.Since(s.startTime).Round(time.Second)
	return fmt.Sprintf("%-12s running  PID %d  uptime %s", s.Name, s.cmd.Process.Pid, uptime)
}

// --- Supervisor ---

func (s *Supervisor) StartAll() {
	for _, name := range s.serviceKeys {
		svc := s.services[name]
		log.Printf("starting %s...", name)
		if err := svc.Start(); err != nil {
			log.Printf("[%s] failed to start: %v", name, err)
		}
		time.Sleep(startStagger)
	}
}

func (s *Supervisor) StopAll() {
	for _, svc := range s.services {
		svc.Stop()
	}
}

func (s *Supervisor) Status() string {
	var lines []string
	for _, name := range s.serviceKeys {
		lines = append(lines, s.services[name].Status())
	}
	return strings.Join(lines, "\n")
}

func (s *Supervisor) Tail(name string, n int) string {
	svc, ok := s.services[name]
	if !ok {
		return fmt.Sprintf("ERROR: unknown service: %s", name)
	}
	// Include the newest backup so tail stays useful right after a rotation.
	var lines []string
	found := false
	for _, p := range []string{svc.logPath + ".1", svc.logPath} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		found = true
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
		}
		if err := scanner.Err(); err != nil {
			lines = append(lines, fmt.Sprintf("(log read error: %v)", err))
		}
		f.Close()
	}
	if !found {
		return "(no log file)"
	}
	return strings.Join(lines, "\n")
}

// --- Config reload ---

func reloadConfig() error {
	newSup := NewSupervisor()
	if err := newSup.LoadConfig(); err != nil {
		return err
	}

	supervisorMu.RLock()
	old := supervisor
	supervisorMu.RUnlock()

	// Stop services removed from config.
	for name, svc := range old.services {
		if _, ok := newSup.services[name]; !ok {
			svc.Stop()
			log.Printf("stopped %s (removed from config)", name)
		}
	}

	// Carry over existing running services; start new ones.
	for _, name := range newSup.serviceKeys {
		if existing, ok := old.services[name]; ok {
			newSup.services[name] = existing
		} else {
			if err := newSup.services[name].Start(); err != nil {
				log.Printf("failed to start %s: %v", name, err)
			} else {
				log.Printf("started %s (added to config)", name)
			}
		}
	}

	supervisorMu.Lock()
	supervisor = newSup
	supervisorMu.Unlock()
	return nil
}

// --- Unix socket server ---

func serveSocket() (net.Listener, error) {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", socketPath, err)
	}
	os.Chmod(socketPath, 0600)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(conn)
		}
	}()
	return ln, nil
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	args := strings.Fields(strings.TrimSpace(line))
	if len(args) == 0 {
		return
	}

	w := bufio.NewWriter(conn)
	defer w.Flush()

	supervisorMu.RLock()
	sup := supervisor
	supervisorMu.RUnlock()

	switch args[0] {
	case "status":
		if len(args) == 1 {
			fmt.Fprintln(w, sup.Status())
		} else {
			if svc, ok := sup.services[args[1]]; ok {
				fmt.Fprintln(w, svc.Status())
			} else {
				fmt.Fprintf(w, "ERROR: unknown service: %s\n", args[1])
			}
		}

	case "start":
		if len(args) < 2 {
			fmt.Fprintln(w, "ERROR: usage: start <name>")
			return
		}
		name := args[1]
		svc, ok := sup.services[name]
		if !ok {
			fmt.Fprintf(w, "ERROR: unknown service: %s\n", name)
			return
		}
		svc.mu.Lock()
		svc.stopped = false
		svc.mu.Unlock()
		if err := svc.Start(); err != nil {
			fmt.Fprintf(w, "ERROR: %v\n", err)
			return
		}
		fmt.Fprintf(w, "started %s\n", name)

	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(w, "ERROR: usage: stop <name>")
			return
		}
		name := args[1]
		svc, ok := sup.services[name]
		if !ok {
			fmt.Fprintf(w, "ERROR: unknown service: %s\n", name)
			return
		}
		svc.Stop()
		fmt.Fprintf(w, "stopped %s\n", name)

	case "restart":
		if len(args) < 2 {
			fmt.Fprintln(w, "ERROR: usage: restart <name>")
			return
		}
		name := args[1]
		svc, ok := sup.services[name]
		if !ok {
			fmt.Fprintf(w, "ERROR: unknown service: %s\n", name)
			return
		}
		svc.Stop()
		svc.mu.Lock()
		svc.stopped = false
		svc.mu.Unlock()
		if err := svc.Start(); err != nil {
			fmt.Fprintf(w, "ERROR: %v\n", err)
			return
		}
		fmt.Fprintf(w, "restarted %s\n", name)

	case "reload":
		if err := reloadConfig(); err != nil {
			fmt.Fprintf(w, "ERROR: %v\n", err)
			return
		}
		fmt.Fprintln(w, "reloaded")

	case "tail":
		n := 20
		name := ""
		rest := args[1:]
		for len(rest) > 0 {
			if rest[0] == "-n" && len(rest) > 1 {
				v, err := strconv.Atoi(rest[1])
				if err != nil {
					fmt.Fprintf(w, "ERROR: invalid line count: %s\n", rest[1])
					return
				}
				n = v
				rest = rest[2:]
			} else {
				name = rest[0]
				rest = rest[1:]
			}
		}
		if name == "" {
			for _, sname := range sup.serviceKeys {
				fmt.Fprintf(w, "=== %s ===\n", sname)
				fmt.Fprintln(w, sup.Tail(sname, n))
			}
		} else {
			fmt.Fprintln(w, sup.Tail(name, n))
		}

	default:
		fmt.Fprintf(w, "ERROR: unknown command: %s\n", args[0])
	}
}

// --- CLI client ---

func dialDaemon(args []string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("section3 is not running (cannot connect to %s)", socketPath)
	}
	defer conn.Close()

	fmt.Fprintln(conn, strings.Join(args, " "))
	conn.(*net.UnixConn).CloseWrite()

	scanner := bufio.NewScanner(conn)
	exitErr := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ERROR: ") {
			fmt.Fprintln(os.Stderr, line[7:])
			exitErr = true
		} else {
			fmt.Println(line)
		}
	}
	if exitErr {
		os.Exit(1)
	}
	return nil
}

// --- Daemon ---

func runDaemon() {
	// Single-instance guard: if we can connect, a daemon is already running.
	if conn, err := net.DialTimeout("unix", socketPath, time.Second); err == nil {
		conn.Close()
		fmt.Fprintln(os.Stderr, "section3: already running")
		os.Exit(1)
	}

	logw, err := newRotatingWriter(filepath.Join(logDir, "section3.log"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "section3: failed to open log: %v\n", err)
		os.Exit(1)
	}
	log.SetOutput(logw)
	log.SetFlags(log.LstdFlags)

	supervisorMu.Lock()
	supervisor = NewSupervisor()
	if err := supervisor.LoadConfig(); err != nil {
		log.Fatalf("section3: failed to load config: %v", err)
	}
	supervisorMu.Unlock()

	ln, err := serveSocket()
	if err != nil {
		log.Fatalf("section3: %v", err)
	}

	log.Printf("section3: starting, pid %d", os.Getpid())
	supervisor.StartAll()

	term := make(chan os.Signal, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	signal.Notify(hup, syscall.SIGHUP)

	for {
		select {
		case <-term:
			log.Println("section3: shutting down...")
			ln.Close()
			os.Remove(socketPath)
			supervisorMu.RLock()
			sup := supervisor
			supervisorMu.RUnlock()
			sup.StopAll()
			return

		case <-hup:
			log.Println("section3: reloading config...")
			if err := reloadConfig(); err != nil {
				log.Printf("section3: reload failed: %v", err)
			}
		}
	}
}

// --- Entry point ---

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--daemon" {
		runDaemon()
		return
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		fmt.Println(`section3 - service supervisor

Commands:
  section3               Start the supervisor daemon
  section3 status        Show status of all services
  section3 status <name> Show status of one service
  section3 start <name>  Start a service
  section3 stop <name>   Stop a service
  section3 restart <name> Restart a service
  section3 reload        Reload config (add/remove services)
  section3 tail [-n N] [name]  Show last N log lines (default: 20, all if no name)
  section3 self version  Show binary version
  section3 self update   Update the binary to the latest release
  section3 help          Show this help`)
	case "self":
		if err := runSelf(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		if err := dialDaemon(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
