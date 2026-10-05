package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

	// KnownFields makes a typo'd key (e.g. "selec" instead of "select") a
	// startup error instead of a silently-ignored field.
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
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
