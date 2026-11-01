// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) LookupFunc {
	return func(n string) (string, bool) { v, ok := m[n]; return v, ok }
}

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(""), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	g := c.Global
	if g.ReconcileInterval.D() != time.Minute || g.CheckInterval.D() != 24*time.Hour || g.ForgetAfter.D() != 168*time.Hour {
		t.Fatalf("%+v", g)
	}
}

func TestFullConfig(t *testing.T) {
	data := `
global:
  reconcile_interval: 30s
  check_interval: 6h
  forget_after: 48h
exclude:
  containers: [ "buildx_*" ]
  images: [ "${REG}/*" ]
registries:
  - host: ghcr.io
    username: ${U}
    password: ${P}
  - { host: index.docker.io, username: x, password: "${HUB:-}" }
container_labels:
  - com.example.team
  - { from: com.example.owner, to: owner_team }
labels:
  - match: { stack: "mail*", container: c, image: i }
    labels: { team: "${TEAM:-mail}" }
`
	c, err := Parse([]byte(data), env(map[string]string{"REG": "ghcr.io/x", "U": "u", "P": "p"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Global.CheckInterval.D() != 6*time.Hour || c.Exclude.Images[0] != "ghcr.io/x/*" || c.Exclude.Containers[0] != "buildx_*" {
		t.Fatalf("%+v", c)
	}
	if r, ok := c.Registry("ghcr.io"); !ok || r.Username != "u" || r.Password != "p" {
		t.Fatalf("ghcr: %+v", r)
	}
	if r, ok := c.Registry("docker.io"); !ok || r.Password != "" {
		t.Fatalf("hub: %+v", r)
	}
	if _, ok := c.Registry("quay.io"); ok {
		t.Fatal("quay")
	}
	if c.ContainerLabels[0].Target() != "container_label_com_example_team" || c.ContainerLabels[1].Target() != "owner_team" {
		t.Fatalf("%+v", c.ContainerLabels)
	}
	if r := c.Labels[0]; r.Match.Stack != "mail*" || r.Match.Container != "c" || r.Match.Image != "i" || r.Labels["team"] != "mail" {
		t.Fatalf("%+v", r)
	}
}

func TestInvalid(t *testing.T) {
	for _, data := range []string{
		"global: { check_interval: 0s }",
		"global: { reconcile_interval: soon }",
		"global: { forget_after: -1h }",
		"nope: 1",
		"global: [",
		"registries: [ { username: x } ]",
		"registries: [ { host: docker.io }, { host: index.docker.io } ]",
		"container_labels: [ '' ]",
		"container_labels: [ { from: a, to: '1x' } ]",
		"container_labels: [ { from: a, to: stack } ]",
		"container_labels: [ { from: a, to: __x } ]",
		"container_labels: [ a.b, a_b ]",
		"container_labels: [ { from: a, bad: b } ]",
		"container_labels: [ { from: [a] } ]",
		"container_labels: [ [a] ]",
		"labels: [ { labels: { mode: x } } ]",
		"labels: [ { labels: { 'a-b': x } } ]",
		"exclude: { images: [ '${MISSING}' ] }",
	} {
		if _, err := Parse([]byte(data), env(nil)); err == nil {
			t.Errorf("%q: expected error", data)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yml")
	c, found, err := Load(missing, env(nil), false)
	if err != nil || found || c.Global.CheckInterval.D() != 24*time.Hour {
		t.Fatalf("missing: %+v %v %v", c, found, err)
	}
	if _, _, err := Load(missing, env(nil), true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing at reload: %v", err)
	}
	file := filepath.Join(dir, "config.yml")
	os.WriteFile(file, []byte(" \n"), 0o644)
	if _, _, err := Load(file, env(nil), false); err != nil {
		t.Fatalf("empty at start: %v", err)
	}
	if _, found, err := Load(file, env(nil), true); err == nil || !found {
		t.Fatalf("empty at reload: %v", err)
	}
	os.WriteFile(file, []byte("global: { check_interval: 1h }"), 0o644)
	if c, found, err := Load(file, env(nil), true); err != nil || !found || c.Global.CheckInterval.D() != time.Hour {
		t.Fatalf("file: %+v %v %v", c, found, err)
	}
	if _, _, err := Load(dir, env(nil), false); err == nil {
		t.Fatal("directory: expected error")
	}
}

func TestExpand(t *testing.T) {
	e := env(map[string]string{"A": "x", "E": ""})
	for in, want := range map[string]string{
		"plain": "plain", "${A}": "x", "a${A}b": "axb", "${B:-def}": "def", "${E:-def}": "def",
		"${A:-def}": "x", "$$": "$", "$$A": "$A", "$1": "$1", "cost $": "cost $", "$x": "$x",
	} {
		if got, err := Expand(in, e); err != nil || got != want {
			t.Errorf("Expand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"${B}", "${", "${1A}", "pa${ss", "${my pass}"} {
		if _, err := Expand(in, e); err == nil {
			t.Errorf("Expand(%q): expected error", in)
		} else if strings.Contains(err.Error(), "pa") {
			t.Errorf("Expand(%q): value in the error: %v", in, err)
		}
	}
	// The error names the key, never the value.
	_, err := Parse([]byte(`registries: [ { host: x, password: "pa${ss" } ]`), e)
	if err == nil || !strings.HasPrefix(err.Error(), "registries[0].password: unterminated ${") {
		t.Fatalf("error: %v", err)
	}
}

func TestLabelHelpers(t *testing.T) {
	if SanitizeLabelName("a.b-c/D_1") != "a_b_c_D_1" {
		t.Fatal("sanitize")
	}
	if !ValidLabelName("team") || ValidLabelName("__x") || ValidLabelName("1a") {
		t.Fatal("ValidLabelName")
	}
	if !IsOwn("mode") || IsOwn("team") {
		t.Fatal("IsOwn")
	}
}

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		p, n string
		ok   bool
	}{
		{"nginx", "nginx", true}, {"nginx", "nginx2", false},
		{"*", "", true}, {"*", "a/b", true},
		{"ghcr.io/*", "ghcr.io/a/b:1", true}, {"ghcr.io/*", "docker.io/x", false},
		{"*:dev", "a/b:dev", true}, {"*:dev", "a/b:dev2", false},
		{"a*b*c", "aXbYc", true}, {"a*b*c", "aXcYb", false}, {"a*a", "a", false},
		{"buildx_*", "buildx_buildkit_x", true}, {"*mid*", "xmidy", true}, {"*mid*", "xmiy", false},
	} {
		if MatchGlob(c.p, c.n) != c.ok {
			t.Errorf("%q %q: want %v", c.p, c.n, c.ok)
		}
	}
	if !MatchAny([]string{"x", "a*"}, "b", "ab") || MatchAny([]string{"x"}, "y") || MatchAny(nil, "y") {
		t.Fatal("MatchAny")
	}
}
