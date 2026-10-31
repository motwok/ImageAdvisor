// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/motwok/ImageAdvisor/internal/config"
	"github.com/motwok/ImageAdvisor/internal/metrics"
)

type fakeDocker struct {
	version string
	reached bool
	err     error
	calls   int
}

func (f *fakeDocker) CheckVersion(context.Context) (string, bool, error) {
	f.calls++
	return f.version, f.reached, f.err
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func text(t *testing.T, c *Core) string {
	var b bytes.Buffer
	if err := metrics.Write(&b, c.Collect(BuildInfo{Version: "v1", Revision: "r", GoVersion: "go", BuildTime: 7})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCheck(t *testing.T) {
	dk := &fakeDocker{version: "1.47", reached: true}
	now := time.Unix(100, 0)
	c := New(config.Default(), Options{Docker: dk, Log: quiet(), Now: func() time.Time { now = now.Add(time.Second); return now }})
	if c.Ready() {
		t.Fatal("ready before check")
	}
	for i := 0; i < 2; i++ {
		if err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Ready() || dk.calls != 1 {
		t.Fatalf("ready %v, version calls %d", c.Ready(), dk.calls)
	}
	out := text(t, c)
	for _, want := range []string{
		"imageadvisor_check_duration_seconds 1\n",
		"imageadvisor_check_errors_total 0\n",
		"imageadvisor_config_last_reload_successful 1\n",
		`imageadvisor_build_info{goversion="go",revision="r",version="v1"} 1`,
		"imageadvisor_build_timestamp_seconds 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestCheckUnreachable(t *testing.T) {
	c := New(config.Default(), Options{Docker: &fakeDocker{err: errors.New("dial")}, Log: quiet()})
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Ready() || !strings.Contains(text(t, c), "imageadvisor_check_errors_total 1\n") {
		t.Fatal("unreachable Docker must count an error")
	}
	n := New(config.Default(), Options{Log: quiet()})
	if err := n.Check(context.Background()); err != nil || n.Ready() {
		t.Fatal("no Docker socket")
	}
}

func TestCheckTooOld(t *testing.T) {
	c := New(config.Default(), Options{Docker: &fakeDocker{version: "1.40", reached: true, err: errors.New("old")}})
	if err := c.Check(context.Background()); !errors.Is(err, ErrDockerTooOld) {
		t.Fatalf("got %v", err)
	}
}

func TestConfig(t *testing.T) {
	c := New(config.Default(), Options{})
	cfg := &config.Config{Global: config.Global{CheckInterval: config.Duration(time.Hour)}}
	c.ReloadFailed()
	if !strings.Contains(text(t, c), "imageadvisor_config_last_reload_successful 0\n") {
		t.Fatal("failed reload not visible")
	}
	c.SetConfig(cfg)
	if c.Config() != cfg || !strings.Contains(text(t, c), "imageadvisor_config_last_reload_successful 1\n") {
		t.Fatal("config not set")
	}
}
