package main

import (
	"bufio"
	"fmt"
	"log"
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

const (
	serviceDir   = "/workspace/etc/sv"
	logDir       = "/workspace/logs"
	maxBackoff   = 60 * time.Second
	backoffMul   = 2
	startStagger = 100 * time.Millisecond
)

type Service struct {
	Name    string
	RunPath string
	LogPath string
	dir     string

	cmd     *exec.Cmd
	logFile *os.File

	mu         sync.Mutex
	stopped    bool
	crashCount int
	backoff    time.Duration
	startTime  time.Time
	lastCrash  time.Time
}

type Supervisor struct {
	services map[string]*Service
}

func NewSupervisor() *Supervisor {
	return &Supervisor{services: make(map[string]*Service)}
}

func (s *Supervisor) Discover() error {
	entries, err := os.ReadDir(serviceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		svc := &Service{
			Name:    name,
			dir:     filepath.Join(serviceDir, name),
			RunPath: filepath.Join(serviceDir, name, "run"),
			LogPath: filepath.Join(logDir, name, "current"),
		}
		if _, err := os.Stat(svc.RunPath); err == nil {
			s.services[name] = svc
		}
	}
	return nil
}

func (s *Service) LogDir() string {
	return filepath.Join(logDir, s.Name)
}

func (s *Service) OpenLog() error {
	dir := s.LogDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.logFile = f
	return nil
}

func (s *Service) Start() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return fmt.Errorf("service is stopped")
	}
	s.mu.Unlock()

	if err := s.OpenLog(); err != nil {
		return err
	}

	cmd := exec.Command(s.RunPath)
	cmd.Stdout = s.logFile
	cmd.Stderr = s.logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.startTime = time.Now()
	s.mu.Unlock()

	go s.wait()
	return nil
}

func (s *Service) wait() {
	err := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped || s.cmd == nil {
		return
	}

	// Service exited
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

	log.Printf("[%s] exited: %v (restarting in %v, crash #%d)", s.Name, err, s.backoff, s.crashCount)

	// Close log file
	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}

	// Restart after backoff
	time.Sleep(s.backoff)

	// Check if stopped while sleeping
	if s.stopped {
		return
	}

	s.Start()
}

func (s *Service) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.backoff = 0
	s.crashCount = 0
	cmd := s.cmd
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// SIGTERM process group
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// SIGKILL
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}

	// Close log
	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}

	return nil
}

func (s *Service) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.cmd.Process == nil {
		if s.lastCrash.IsZero() {
			return fmt.Sprintf("%-12s stopped  (never started)", s.Name)
		}
		ago := time.Since(s.lastCrash).Round(time.Second)
		return fmt.Sprintf("%-12s stopped  exit    last crash %s ago", s.Name, ago)
	}

	uptime := time.Since(s.startTime).Round(time.Second)
	return fmt.Sprintf("%-12s running  PID %d  uptime %s", s.Name, s.cmd.Process.Pid, uptime)
}

func (s *Supervisor) StartAll() error {
	names := make([]string, 0, len(s.services))
	for name := range s.services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := s.services[name]
		log.Printf("Starting %s...", name)
		if err := svc.Start(); err != nil {
			log.Printf("[%s] failed to start: %v", name, err)
		}
		time.Sleep(startStagger)
	}
	return nil
}

func (s *Supervisor) StopAll() {
	for _, svc := range s.services {
		svc.Stop()
	}
}

func (s *Supervisor) Status() string {
	var lines []string
	for _, name := range sortedKeys(s.services) {
		lines = append(lines, s.services[name].Status())
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]*Service) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func Tail(name string, lines int) error {
	svc, ok := supervisor.services[name]
	if !ok {
		return fmt.Errorf("unknown service: %s", name)
	}

	f, err := os.Open(svc.LogPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var tail []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		tail = append(tail, scanner.Text())
		if len(tail) > lines {
			tail = tail[len(tail)-lines:]
		}
	}

	for _, l := range tail {
		fmt.Println(l)
	}
	return nil
}

func TailAll(lines int) error {
	for _, name := range sortedKeys(supervisor.services) {
		fmt.Printf("=== %s ===\n", name)
		if err := Tail(name, lines); err != nil {
			fmt.Printf("  (no log file)\n")
		}
	}
	return nil
}

var supervisor *Supervisor

func main() {
	supervisor = NewSupervisor()
	if err := supervisor.Discover(); err != nil {
		log.Fatal(err)
	}

	if len(os.Args) < 2 {
		// Run as supervisor
		log.Printf("section3: supervising %d services", len(supervisor.services))
		supervisor.StartAll()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		<-sig

		log.Println("section3: shutting down...")
		supervisor.StopAll()
		return
	}

	switch os.Args[1] {
	case "status":
		if len(os.Args) == 2 {
			fmt.Println(supervisor.Status())
		} else {
			name := os.Args[2]
			if svc, ok := supervisor.services[name]; ok {
				fmt.Println(svc.Status())
			} else {
				fmt.Printf("unknown service: %s\n", name)
				os.Exit(1)
			}
		}
	case "start":
		if len(os.Args) < 3 {
			fmt.Println("usage: section3 start <name>")
			os.Exit(2)
		}
		name := os.Args[2]
		if svc, ok := supervisor.services[name]; ok {
			if err := svc.Start(); err != nil {
				fmt.Printf("failed to start %s: %v\n", name, err)
				os.Exit(1)
			}
			fmt.Printf("started %s\n", name)
		} else {
			fmt.Printf("unknown service: %s\n", name)
			os.Exit(1)
		}
	case "stop":
		if len(os.Args) < 3 {
			fmt.Println("usage: section3 stop <name>")
			os.Exit(2)
		}
		name := os.Args[2]
		if svc, ok := supervisor.services[name]; ok {
			svc.Stop()
			fmt.Printf("stopped %s\n", name)
		} else {
			fmt.Printf("unknown service: %s\n", name)
			os.Exit(1)
		}
	case "restart":
		if len(os.Args) < 3 {
			fmt.Println("usage: section3 restart <name>")
			os.Exit(2)
		}
		name := os.Args[2]
		if svc, ok := supervisor.services[name]; ok {
			svc.Stop()
			time.Sleep(500 * time.Millisecond)
			if err := svc.Start(); err != nil {
				fmt.Printf("failed to restart %s: %v\n", name, err)
				os.Exit(1)
			}
			fmt.Printf("restarted %s\n", name)
		} else {
			fmt.Printf("unknown service: %s\n", name)
			os.Exit(1)
		}
	case "tail":
		lines := 20
		name := ""
		args := os.Args[2:]
		for len(args) > 0 {
			if args[0] == "-n" {
				if len(args) < 2 {
					fmt.Println("usage: section3 tail [-n <lines>] [name]")
					os.Exit(2)
				}
				n, err := strconv.Atoi(args[1])
				if err != nil {
					fmt.Printf("invalid number: %s\n", args[1])
					os.Exit(2)
				}
				lines = n
				args = args[2:]
			} else {
				name = args[0]
				args = args[1:]
			}
		}
		var err error
		if name == "" {
			err = TailAll(lines)
		} else {
			err = Tail(name, lines)
		}
		if err != nil {
			fmt.Printf("tail failed: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		fmt.Println(`section3 - service supervisor

Commands:
  section3                  Start the supervisor (default when run without args)
  section3 status           Show status of all services
  section3 status <name>  Show status of one service
  section3 start <name>    Start a service
  section3 stop <name>     Stop a service
  section3 restart <name>  Restart a service
  section3 tail [-n N] [name]  Show last N log lines (default: 20, all services if no name)
  section3 help             Show this help`)
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}
