package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withConfigDir points the loader at a temporary directory holding the given
// files, and restores the real one afterwards.
func withConfigDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	prev := configDir
	configDir = dir
	t.Cleanup(func() { configDir = prev })
}

func TestLoadConfigMergesFiles(t *testing.T) {
	withConfigDir(t, map[string]string{
		"10-dev.yml":       "services:\n  signalshell:\n    command: /bin/true\n",
		"20-workspace.yml": "services:\n  telegram:\n    command: /bin/true\n",
	})
	s := NewSupervisor()
	if err := s.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(s.serviceKeys) != 2 {
		t.Fatalf("want 2 services, got %v", s.serviceKeys)
	}
	if s.serviceKeys[0] != "signalshell" || s.serviceKeys[1] != "telegram" {
		t.Errorf("serviceKeys not sorted: %v", s.serviceKeys)
	}
}

// .yaml is picked up alongside .yml so a fragment's extension is not a silent
// reason for a service to be missing.
func TestLoadConfigAcceptsBothExtensions(t *testing.T) {
	withConfigDir(t, map[string]string{
		"a.yml":  "services:\n  one:\n    command: /bin/true\n",
		"b.yaml": "services:\n  two:\n    command: /bin/true\n",
	})
	s := NewSupervisor()
	if err := s.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(s.services) != 2 {
		t.Errorf("want both files loaded, got %v", s.serviceKeys)
	}
}

// The whole point of splitting the config: a fragment must not inherit the
// defaults of whichever unrelated file happens to sort ahead of it.
func TestDefaultsAreScopedToTheirOwnFile(t *testing.T) {
	withConfigDir(t, map[string]string{
		"10-first.yml":  "defaults:\n  dir: /workspace\nservices:\n  project:\n    command: /bin/true\n",
		"20-second.yml": "services:\n  fragment:\n    command: /bin/true\n",
	})
	s := NewSupervisor()
	if err := s.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := s.services["project"].Dir; got != "/workspace" {
		t.Errorf("project should take its own file's default, got %q", got)
	}
	if got := s.services["fragment"].Dir; got != "" {
		t.Errorf("fragment must not inherit the other file's default, got %q", got)
	}
}

// A duplicate is an error naming both files, not a silent last-one-wins that
// would depend on filename order.
func TestDuplicateServiceIsAnError(t *testing.T) {
	withConfigDir(t, map[string]string{
		"10-dev.yml":       "services:\n  signalshell:\n    command: /bin/dev\n",
		"20-workspace.yml": "services:\n  signalshell:\n    command: /bin/workspace\n",
	})
	s := NewSupervisor()
	err := s.LoadConfig()
	if err == nil {
		t.Fatal("want an error for a service defined twice, got nil")
	}
	for _, want := range []string{"signalshell", "10-dev.yml", "20-workspace.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

// An empty directory means a supervisor managing nothing, which is
// indistinguishable from a healthy one until someone notices the services are
// gone. Most likely cause is a half-finished migration.
func TestEmptyConfigDirIsAnError(t *testing.T) {
	withConfigDir(t, nil)
	s := NewSupervisor()
	err := s.LoadConfig()
	if err == nil {
		t.Fatal("want an error for an empty config dir, got nil")
	}
	if !strings.Contains(err.Error(), "nothing to supervise") {
		t.Errorf("error should say why it matters: %v", err)
	}
}

func TestMissingConfigDirIsAnError(t *testing.T) {
	prev := configDir
	configDir = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { configDir = prev })
	s := NewSupervisor()
	if err := s.LoadConfig(); err == nil {
		t.Fatal("want an error for a missing config dir, got nil")
	}
}

func TestInvalidYAMLNamesTheFile(t *testing.T) {
	withConfigDir(t, map[string]string{
		"broken.yml": "services:\n  bad:\n   command: [unclosed\n",
	})
	s := NewSupervisor()
	err := s.LoadConfig()
	if err == nil {
		t.Fatal("want an error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "broken.yml") {
		t.Errorf("error should name the offending file: %v", err)
	}
}

func TestConfigOriginsReportsEachFile(t *testing.T) {
	withConfigDir(t, map[string]string{
		"10-dev.yml":       "services:\n  signalshell:\n    command: /bin/true\n",
		"20-workspace.yml": "services:\n  telegram:\n    command: /bin/true\n",
	})
	s := NewSupervisor()
	if err := s.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	out := s.ConfigOrigins()
	for _, want := range []string{"10-dev.yml", "signalshell", "20-workspace.yml", "telegram"} {
		if !strings.Contains(out, want) {
			t.Errorf("ConfigOrigins should mention %q:\n%s", want, out)
		}
	}
}
