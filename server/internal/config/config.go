package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	WSPath    string `yaml:"ws_path"`
	PortRange string `yaml:"port_range"`
	AdminPass string `yaml:"admin_pass"`
	DataDir   string `yaml:"data_dir"`
	Listen    string `yaml:"listen"`

	PortMin int
	PortMax int
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		WSPath:    "/api/v1/stream",
		PortRange: "10000-20000",
		AdminPass: "changeme",
		DataDir:   "/data",
		Listen:    ":8080",
	}

	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	if v := os.Getenv("WS_PATH"); v != "" {
		cfg.WSPath = v
	}
	if v := os.Getenv("PORT_RANGE"); v != "" {
		cfg.PortRange = v
	}
	if v := os.Getenv("ADMIN_PASS"); v != "" {
		cfg.AdminPass = v
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("LISTEN"); v != "" {
		cfg.Listen = v
	}

	parts := strings.SplitN(cfg.PortRange, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid port_range: %s", cfg.PortRange)
	}
	min, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid port_range min: %w", err)
	}
	max, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid port_range max: %w", err)
	}
	cfg.PortMin = min
	cfg.PortMax = max

	return cfg, nil
}
