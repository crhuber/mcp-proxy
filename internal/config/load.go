package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads, parses, env-interpolates, and validates the config file at
// path. It returns an aggregated error (see ValidationErrors) describing
// every problem found, rather than stopping at the first one.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	if err := interpolateEnv(&cfg); err != nil {
		return nil, err
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
