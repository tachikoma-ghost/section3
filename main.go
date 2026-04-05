package main

import (
	"bufio"
	"fmt"
	"gopkg.in/yaml.v3"
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
	configPath   = "/workspace/section3.yml"
	logDir       = "/tmp/section3-logs"
	maxBackoff   = 60 * time.Second
	backoffMul   = 2
	startStagger = 100 * time.Millisecond
	maxLogSize   = 1 * 1024 * 1024 // 1MB
	maxLogFiles  = 5
)

type Config struct {
	Services map[string]ServiceConfig `yaml:"services"`
}

type ServiceConfig struct {
	Command   string   `yaml:"command"`
	Restart   string   `yaml:"restart"` // always, never, on-crash
	DependsOn []string `yaml:"depends_on"`
}

type Service struct {
	Name    string
	Command string
	Restart string
	cmd     *exec.Cmd
	logFile *os.File
	logPath string

	mu         sync.Mutex
	stopped    bool
	crashCount int
	backoff    time.Duration
	startTime  time.Time
	lastCrash  time.Time
}

type Supervisor struct {
	services    map[string]*Service
	serviceKeys []string // sorted keys
}

func NewSupervisor() *Supervisor {
	return &Supervisor{services: make(map[string]*Service)}
}

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
		s.services[name] = &Service{
			Name:    name,
			Command: sc.Command,
			Restart: sc.Restart,
			logPath: filepath.Join(logDir, name+".log"),
		}
		s.serviceKeys = append(s.serviceKeys, name)
	}
	sort.Strings(s.serviceKeys)
	return nil
}

func (s *Service) OpenLog() error {
	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	// Check rotation
	s.checkRotation()

	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.logFile = f
	return nil
}

func (s *Service) checkRotation() {
	fi, err := os.Stat(s.logPath)
	if err != nil || fi.Size() < maxLogSize {
		return
	}

	// Rotate: foo.log -> foo.log.1, foo.log.1 -> foo.log.2, etc.
	for i := maxLogFiles - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", s.logPath, i)
		new := fmt.Sprintf("%s.%d", s.logPath, i+1)
		os.Rename(old, new)
	}
	os.Rename(s.logPath, s.logPath+".1")
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

	cmd := exec.Command("/bin/sh", "-c", s.Command)
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

func (s *Service) shouldRestart() bool {
	if s.Restart == "never" {
		return false
	}
	return true
}

func (s *Service) wait() {
	err := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped || s.cmd == nil {
		return
	}

	if !s.shouldRestart() {
		log.Printf("[%s] exited: %v (not restarting, restart=never)", s.Name, err)
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

	log.Printf("[%s] exited: %v (restarting in %v, crash #%d)", s.Name, err, s.backoff, s.crashCount)

	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}

	time.Sleep(s.backoff)

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

	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}

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
	for _, name := range s.serviceKeys {
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
	for _, name := range s.serviceKeys {
		lines = append(lines, s.services[name].Status())
	}
	return strings.Join(lines, "\n")
}

func Tail(name string, lines int) error {
	svc, ok := supervisor.services[name]
	if !ok {
		return fmt.Errorf("unknown service: %s", name)
	}

	f, err := os.Open(svc.logPath)
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
	for _, name := range supervisor.serviceKeys {
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
	if err := supervisor.LoadConfig(); err != nil {
		log.Fatal(err)
	}

	if len(os.Args) < 2 {
		// Daemonize: use start-stop-daemon or nohup
		binary, err := filepath.Abs(os.Args[0])
		if err != nil {
			log.Fatal(err)
		}
		cmd := exec.Command("nohup", binary, "--daemon")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = nil
		cmd.Start()
		fmt.Printf("section3: started as daemon (pid %d)\n", cmd.Process.Pid)
		os.Exit(0)
	}

	// --daemon flag: actually run the supervisor (child process after fork)
	if os.Args[1] == "--daemon" {
		// Ensure log directory exists
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "section3: failed to create log dir %s: %v\n", logDir, err)
			os.Exit(1)
		}
		// Redirect logs to file in logDir
		logFd, err := os.OpenFile(filepath.Join(logDir, "section3.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "section3: failed to open log: %v\n", err)
			os.Exit(1)
		}
		log.SetOutput(logFd)
		log.SetFlags(log.LstdFlags)
		log.Printf("section3: daemon starting, pid %d", os.Getpid())
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
	case "reload":
		old := supervisor.services
		supervisor = NewSupervisor()
		if err := supervisor.LoadConfig(); err != nil {
			fmt.Printf("reload failed: %v\n", err)
			os.Exit(1)
		}

		// Stop services not in new config
		for name, svc := range old {
			if _, ok := supervisor.services[name]; !ok {
				svc.Stop()
				log.Printf("stopped %s (removed from config)", name)
			}
		}

		// Start new services
		for _, name := range supervisor.serviceKeys {
			if _, ok := old[name]; !ok {
				supervisor.services[name].Start()
				log.Printf("started %s (added from config)", name)
			}
		}

		fmt.Println("reloaded")
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
  section3               Start the supervisor (default when run without args)
  section3 status        Show status of all services
  section3 status <name> Show status of one service
  section3 start <name>  Start a service
  section3 stop <name>   Stop a service
  section3 restart <name> Restart a service
  section3 reload        Reload config (add/remove services)
  section3 tail [-n N] [name]  Show last N log lines (default: 20, all if no name)
  section3 help          Show this help`)
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}
