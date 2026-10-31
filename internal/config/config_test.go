// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(""))
	if err != nil || c.Global.CheckInterval.D() != 24*time.Hour {
		t.Fatalf("defaults: %+v %v", c, err)
	}
}

func TestParse(t *testing.T) {
	c, err := Parse([]byte("global:\n  check_interval: 6h\n"))
	if err != nil || c.Global.CheckInterval.D() != 6*time.Hour {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{
		"global: { check_interval: 0s }",
		"global: { check_interval: soon }",
		"global: { unknown: 1 }",
		"nope: 1",
		"global: [",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, found, err := Load(filepath.Join(dir, "missing.yml"))
	if err != nil || found || c.Global.CheckInterval.D() != 24*time.Hour {
		t.Fatalf("missing: %+v %v %v", c, found, err)
	}
	file := filepath.Join(dir, "config.yml")
	os.WriteFile(file, []byte("global: { check_interval: 1h }"), 0o644)
	c, found, err = Load(file)
	if err != nil || !found || c.Global.CheckInterval.D() != time.Hour {
		t.Fatalf("file: %+v %v %v", c, found, err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Fatal("directory: expected error")
	}
}
