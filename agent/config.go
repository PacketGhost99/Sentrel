// Package agent implements the Heartbeat monitoring agent.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config is the agent's on-disk configuration. Everything about what to
// collect comes from the server; this file only says how to reach it.
type Config struct {
	ServerURL     string `yaml:"server_url"`
	AgentID       string `yaml:"agent_id"`
	StateDir      string `yaml:"state_dir"`
	CAFingerprint string `yaml:"ca_fingerprint,omitempty"`
	Name          string `yaml:"name,omitempty"`
	LogLevel      string `yaml:"log_level,omitempty"`

	path string `yaml:"-"`
}

// DefaultConfigPath is the platform default config location.
func DefaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Heartbeat Agent", "agent.yaml")
	}
	return "/etc/heartbeat-agent/agent.yaml"
}

// DefaultStateDir is the platform default state directory.
func DefaultStateDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Heartbeat Agent")
	}
	return "/var/lib/heartbeat-agent"
}

// LoadConfig reads the config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s not found: register the agent first (heartbeat-agent register SERVER TOKEN)", path)
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.ServerURL == "" || c.AgentID == "" {
		return nil, fmt.Errorf("%s is incomplete; re-register the agent", path)
	}
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir()
	}
	c.path = path
	return &c, nil
}

// Save writes the config file.
func (c *Config) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte("# Heartbeat agent configuration (written by `heartbeat-agent register`)\n"), b...), 0o640); err != nil {
		return err
	}
	c.path = path
	return os.Rename(tmp, path)
}

// Path returns where the config was loaded from.
func (c *Config) Path() string { return c.path }

func (c *Config) keyFile() string  { return filepath.Join(c.StateDir, "agent.key") }
func (c *Config) certFile() string { return filepath.Join(c.StateDir, "agent.crt") }
func (c *Config) caFile() string   { return filepath.Join(c.StateDir, "ca.pem") }

// JobKeyFile is next to the config file. On Linux the installer makes it
// root-owned so the privileged helper can trust it.
func (c *Config) JobKeyFile() string { return filepath.Join(filepath.Dir(c.path), "job.pub") }
