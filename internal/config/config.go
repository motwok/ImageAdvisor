// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads and validates the ImageAdvisor config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Global Global `yaml:"global"`
}

type Global struct {
	CheckInterval Duration `yaml:"check_interval"`
}

// Duration is a time.Duration written as "60s", "1h", ...
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %v", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

func Default() *Config {
	return &Config{Global: Global{CheckInterval: Duration(24 * time.Hour)}}
}

// Load reads the file. A missing file gives the defaults and found=false.
func Load(file string) (cfg *Config, found bool, err error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg, err = Parse(data)
	return cfg, true, err
}

func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if cfg.Global.CheckInterval <= 0 {
		return nil, errors.New("global.check_interval must be greater than 0")
	}
	return cfg, nil
}
