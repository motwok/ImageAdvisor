// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads and validates the ImageAdvisor config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/motwok/ImageAdvisor/internal/ref"
	"go.yaml.in/yaml/v3"
)

// OwnLabels are set by ImageAdvisor and cannot be set from the config or Docker labels.
var OwnLabels = []string{"container", "image", "reference", "stack", "state", "mode"}

type Config struct {
	Global          Global      `yaml:"global"`
	Exclude         Exclude     `yaml:"exclude"`
	Registries      []Registry  `yaml:"registries"`
	ContainerLabels []LabelRef  `yaml:"container_labels"`
	Labels          []LabelRule `yaml:"labels"`
}

type Global struct {
	ReconcileInterval Duration `yaml:"reconcile_interval"`
	CheckInterval     Duration `yaml:"check_interval"`
	ForgetAfter       Duration `yaml:"forget_after"`
}

type Exclude struct {
	Containers []string `yaml:"containers"`
	Images     []string `yaml:"images"`
}

type Registry struct {
	Host     string `yaml:"host"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LabelRef is an allowlisted Docker label, written as "name" or {from, to}.
type LabelRef struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

func (r *LabelRef) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		r.From = n.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: %s must be a string", v.Line, k.Value)
			}
			switch k.Value {
			case "from":
				r.From = v.Value
			case "to":
				r.To = v.Value
			default:
				return fmt.Errorf("line %d: unknown key %q", k.Line, k.Value)
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: expected a name or {from, to}", n.Line)
}

// Target is the metric label name of an allowlisted Docker label.
func (r LabelRef) Target() string {
	if r.To != "" {
		return r.To
	}
	return "container_label_" + SanitizeLabelName(r.From)
}

type LabelRule struct {
	Match  Match             `yaml:"match"`
	Labels map[string]string `yaml:"labels"`
}

type Match struct {
	Stack     string `yaml:"stack"`
	Container string `yaml:"container"`
	Image     string `yaml:"image"`
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
	return &Config{Global: Global{
		ReconcileInterval: Duration(60 * time.Second),
		CheckInterval:     Duration(24 * time.Hour),
		ForgetAfter:       Duration(168 * time.Hour),
	}}
}

// LookupFunc returns an environment variable.
type LookupFunc func(name string) (string, bool)

// Load reads the file. A missing file gives the defaults and found=false,
// unless required is set (reload), where a missing or empty file is an error.
func Load(file string, lookup LookupFunc, required bool) (cfg *Config, found bool, err error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		if required {
			return nil, false, fmt.Errorf("config file %s is missing", file)
		}
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if required && len(bytes.TrimSpace(data)) == 0 {
		return nil, true, fmt.Errorf("config file %s is empty", file)
	}
	cfg, err = Parse(data, lookup)
	return cfg, true, err
}

func Parse(data []byte, lookup LookupFunc) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := cfg.expand(lookup); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// expand replaces the variables in all string values; an error names the key, not the value.
func (c *Config) expand(lookup LookupFunc) error {
	var err error
	ex := func(where string, s *string) {
		if err == nil {
			if *s, err = Expand(*s, lookup); err != nil {
				err = fmt.Errorf("%s: %w", where, err)
			}
		}
	}
	for i := range c.Exclude.Containers {
		ex(fmt.Sprintf("exclude.containers[%d]", i), &c.Exclude.Containers[i])
	}
	for i := range c.Exclude.Images {
		ex(fmt.Sprintf("exclude.images[%d]", i), &c.Exclude.Images[i])
	}
	for i := range c.Registries {
		r, where := &c.Registries[i], fmt.Sprintf("registries[%d]", i)
		ex(where+".host", &r.Host)
		ex(where+".username", &r.Username)
		ex(where+".password", &r.Password)
	}
	for i := range c.ContainerLabels {
		where := fmt.Sprintf("container_labels[%d]", i)
		ex(where+".from", &c.ContainerLabels[i].From)
		ex(where+".to", &c.ContainerLabels[i].To)
	}
	for i := range c.Labels {
		r, where := &c.Labels[i], fmt.Sprintf("labels[%d]", i)
		ex(where+".match.stack", &r.Match.Stack)
		ex(where+".match.container", &r.Match.Container)
		ex(where+".match.image", &r.Match.Image)
		for k, v := range r.Labels {
			ex(where+".labels."+k, &v)
			r.Labels[k] = v
		}
	}
	return err
}

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidLabelName reports whether n is a Prometheus label name that is not reserved.
func ValidLabelName(n string) bool {
	return labelName.MatchString(n) && !strings.HasPrefix(n, "__")
}

// IsOwn reports whether n is one of ImageAdvisor's own labels.
func IsOwn(n string) bool {
	for _, o := range OwnLabels {
		if n == o {
			return true
		}
	}
	return false
}

func checkLabelName(where, n string) error {
	if !ValidLabelName(n) {
		return fmt.Errorf("%s: invalid label name %q", where, n)
	}
	if IsOwn(n) {
		return fmt.Errorf("%s: label %q is set by ImageAdvisor", where, n)
	}
	return nil
}

func (c *Config) validate() error {
	g := c.Global
	if g.ReconcileInterval <= 0 || g.CheckInterval <= 0 || g.ForgetAfter <= 0 {
		return errors.New("global: durations must be greater than 0")
	}
	hosts := map[string]bool{}
	for i, r := range c.Registries {
		h := ref.NormalizeHost(r.Host)
		if h == "" {
			return fmt.Errorf("registries[%d]: host missing", i)
		}
		if hosts[h] {
			return fmt.Errorf("registries[%d]: host %s twice", i, h)
		}
		hosts[h] = true
	}
	seen := map[string]bool{}
	for _, l := range c.ContainerLabels {
		if l.From == "" {
			return errors.New("container_labels: empty label name")
		}
		n := l.Target()
		if err := checkLabelName("container_labels", n); err != nil {
			return err
		}
		if seen[n] {
			return fmt.Errorf("container_labels: two labels map to %q", n)
		}
		seen[n] = true
	}
	for i, r := range c.Labels {
		for k := range r.Labels {
			if err := checkLabelName(fmt.Sprintf("labels[%d]", i), k); err != nil {
				return err
			}
		}
	}
	return nil
}

// Registry returns the credentials for a normalized registry host, if any.
func (c *Config) Registry(host string) (Registry, bool) {
	for _, r := range c.Registries {
		if ref.NormalizeHost(r.Host) == host {
			return r, true
		}
	}
	return Registry{}, false
}

// SanitizeLabelName replaces every character outside [a-zA-Z0-9_] by "_".
func SanitizeLabelName(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

// MatchGlob matches name against pattern; "*" matches any characters, including "/".
func MatchGlob(pattern, name string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == name
	}
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(name, p)
		if i < 0 {
			return false
		}
		name = name[i+len(p):]
	}
	return strings.HasSuffix(name, last)
}

// MatchAny reports whether any of the patterns matches any of the names.
func MatchAny(patterns []string, names ...string) bool {
	for _, p := range patterns {
		for _, n := range names {
			if MatchGlob(p, n) {
				return true
			}
		}
	}
	return false
}
