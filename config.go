package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	CIDR          string        `yaml:"cidr"`
	StateFile     string        `yaml:"state_file"`
	Verbose       bool          `yaml:"verbose"`
	Auth          AuthConfig    `yaml:"auth"`
	Whitelist     []string      `yaml:"whitelist"`
	Admin         AdminConfig   `yaml:"admin"`
	Dynamic       DynamicConfig `yaml:"dynamic"`
	Sticky        StickyConfig  `yaml:"sticky"`
	Fixed         FixedConfig   `yaml:"fixed"`
	ConfigSource  string        `yaml:"-"`
	StateFilePath string        `yaml:"-"`
}

type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type DynamicConfig struct {
	HTTPPort   int `yaml:"http_port"`
	Socks5Port int `yaml:"socks5_port"`
}

type StickyConfig struct {
	HTTPPort        int   `yaml:"http_port"`
	Socks5Port      int   `yaml:"socks5_port"`
	RotationSeconds int64 `yaml:"rotation_seconds"`
}

func (s StickyConfig) RotationDuration() time.Duration {
	return time.Duration(s.RotationSeconds) * time.Second
}

type AdminConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Token   string `yaml:"token"`
}

type FixedConfig struct {
	HTTPPorts   []int `yaml:"http_ports"`
	Socks5Ports []int `yaml:"socks5_ports"`
}

func (f FixedConfig) AllPorts() []int {
	ports := make([]int, 0, len(f.HTTPPorts)+len(f.Socks5Ports))
	ports = append(ports, f.HTTPPorts...)
	ports = append(ports, f.Socks5Ports...)
	return ports
}

func DefaultConfig() *Config {
	return &Config{
		StateFile: "state.json",
		Admin: AdminConfig{
			Listen: "127.0.0.1:52120",
		},
		Dynamic: DynamicConfig{
			HTTPPort:   52122,
			Socks5Port: 52123,
		},
		Sticky: StickyConfig{
			RotationSeconds: 60,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if cfg.StateFile == "" {
		cfg.StateFile = "state.json"
	}
	cfg.StateFilePath = cfg.StateFile
	if !filepath.IsAbs(cfg.StateFilePath) {
		cfg.StateFilePath = filepath.Join(filepath.Dir(path), cfg.StateFilePath)
	}
	return cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	if path == "" || path == "command-line" {
		return fmt.Errorf("config file path is not available")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return atomicWriteFile(path, data, 0600)
}

func (c *Config) Validate() error {
	if c.CIDR == "" {
		return fmt.Errorf("cidr is empty")
	}
	if err := validateIPv6CIDR(c.CIDR); err != nil {
		return fmt.Errorf("invalid cidr: %w", err)
	}
	if err := validateAuthConfig(c.Auth); err != nil {
		return err
	}
	if _, err := ParseWhitelist(c.Whitelist); err != nil {
		return err
	}
	if c.Admin.Enabled {
		if c.Admin.Listen == "" {
			return fmt.Errorf("admin.listen is empty")
		}
		if c.Admin.Token == "" {
			return fmt.Errorf("admin.token is empty")
		}
	}

	seen := make(map[int]string)
	if err := validatePort(c.Dynamic.HTTPPort, "dynamic.http_port"); err != nil {
		return err
	}
	seen[c.Dynamic.HTTPPort] = "dynamic.http_port"

	if err := validatePort(c.Dynamic.Socks5Port, "dynamic.socks5_port"); err != nil {
		return err
	}
	if prev := seen[c.Dynamic.Socks5Port]; prev != "" {
		return fmt.Errorf("port %d is duplicated by %s and dynamic.socks5_port", c.Dynamic.Socks5Port, prev)
	}
	seen[c.Dynamic.Socks5Port] = "dynamic.socks5_port"

	if c.Sticky.HTTPPort != 0 {
		if err := validatePort(c.Sticky.HTTPPort, "sticky.http_port"); err != nil {
			return err
		}
		if prev := seen[c.Sticky.HTTPPort]; prev != "" {
			return fmt.Errorf("port %d is duplicated by %s and sticky.http_port", c.Sticky.HTTPPort, prev)
		}
		seen[c.Sticky.HTTPPort] = "sticky.http_port"
	}
	if c.Sticky.Socks5Port != 0 {
		if err := validatePort(c.Sticky.Socks5Port, "sticky.socks5_port"); err != nil {
			return err
		}
		if prev := seen[c.Sticky.Socks5Port]; prev != "" {
			return fmt.Errorf("port %d is duplicated by %s and sticky.socks5_port", c.Sticky.Socks5Port, prev)
		}
		seen[c.Sticky.Socks5Port] = "sticky.socks5_port"
	}
	if c.Sticky.HTTPPort != 0 || c.Sticky.Socks5Port != 0 {
		if c.Sticky.RotationSeconds <= 0 {
			return fmt.Errorf("sticky.rotation_seconds must be greater than 0")
		}
		if c.Sticky.RotationSeconds > int64((time.Duration(1<<63-1))/time.Second) {
			return fmt.Errorf("sticky.rotation_seconds is too large")
		}
	}

	for _, port := range c.Fixed.HTTPPorts {
		if err := validatePort(port, "fixed.http_ports"); err != nil {
			return err
		}
		if prev := seen[port]; prev != "" {
			return fmt.Errorf("port %d is duplicated by %s and fixed.http_ports", port, prev)
		}
		seen[port] = "fixed.http_ports"
	}
	for _, port := range c.Fixed.Socks5Ports {
		if err := validatePort(port, "fixed.socks5_ports"); err != nil {
			return err
		}
		if prev := seen[port]; prev != "" {
			return fmt.Errorf("port %d is duplicated by %s and fixed.socks5_ports", port, prev)
		}
		seen[port] = "fixed.socks5_ports"
	}
	return nil
}

func validatePort(port int, name string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}

func validateAuthConfig(auth AuthConfig) error {
	if auth.Username == "" && auth.Password == "" {
		return nil
	}
	if auth.Username == "" || auth.Password == "" {
		return fmt.Errorf("auth.username and auth.password must be configured together")
	}
	return nil
}
