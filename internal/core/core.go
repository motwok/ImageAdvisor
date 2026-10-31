// SPDX-License-Identifier: AGPL-3.0-or-later

// Package core runs the checks and produces the metrics.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/motwok/ImageAdvisor/internal/config"
	"github.com/motwok/ImageAdvisor/internal/metrics"
)

// Docker is the part of the Docker API ImageAdvisor uses.
type Docker interface {
	CheckVersion(ctx context.Context) (string, bool, error)
}

// ErrDockerTooOld stops ImageAdvisor.
var ErrDockerTooOld = errors.New("docker API too old")

type Options struct {
	Docker Docker
	Log    *slog.Logger
	Now    func() time.Time
}

type Core struct {
	mu  sync.Mutex
	cfg *config.Config
	dk  Docker
	log *slog.Logger
	now func() time.Time

	versionOK    bool
	ready        bool
	checkErrors  float64
	checkSeconds float64
	reloadOK     bool
}

func New(cfg *config.Config, o Options) *Core {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Core{cfg: cfg, dk: o.Docker, log: o.Log, now: o.Now, reloadOK: true}
}

// SetConfig activates a new config after a successful reload.
func (c *Core) SetConfig(cfg *config.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	c.reloadOK = true
}

// ReloadFailed records a failed reload; the previous config stays.
func (c *Core) ReloadFailed() {
	c.mu.Lock()
	c.reloadOK = false
	c.mu.Unlock()
}

func (c *Core) Config() *config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

func (c *Core) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

// Check runs one check pass. It returns ErrDockerTooOld only; other errors are
// counted and logged.
func (c *Core) Check(ctx context.Context) error {
	start := c.now()
	err := c.checkVersion(ctx)
	if errors.Is(err, ErrDockerTooOld) {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.checkErrors++
		c.log.Warn("Docker not reachable", "err", err)
	} else {
		c.ready = true
	}
	c.checkSeconds = c.now().Sub(start).Seconds()
	return nil
}

func (c *Core) checkVersion(ctx context.Context) error {
	c.mu.Lock()
	ok := c.versionOK
	c.mu.Unlock()
	if ok {
		return nil
	}
	if c.dk == nil {
		return errors.New("no Docker socket configured")
	}
	v, reached, err := c.dk.CheckVersion(ctx)
	if err != nil {
		if reached {
			return fmt.Errorf("%w: %v", ErrDockerTooOld, err)
		}
		return err
	}
	c.log.Info("Docker API", "version", v)
	c.mu.Lock()
	c.versionOK = true
	c.mu.Unlock()
	return nil
}

// BuildInfo describes the binary for imageadvisor_build_info.
type BuildInfo struct {
	Version, Revision, GoVersion string
	BuildTime                    int64
}

func m(name, help, typ string) metrics.Family {
	return metrics.Family{Name: "imageadvisor_" + name, Help: help, Type: typ}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Collect returns all metric families.
func (c *Core) Collect(bi BuildInfo) []metrics.Family {
	c.mu.Lock()
	checkErrors, checkSeconds, reloadOK := c.checkErrors, c.checkSeconds, c.reloadOK
	c.mu.Unlock()

	one := func(f metrics.Family, val float64) metrics.Family {
		f.Samples = []metrics.Sample{{Value: val}}
		return f
	}
	return []metrics.Family{
		one(m("check_duration_seconds", "Duration of the last check.", "gauge"), checkSeconds),
		one(m("check_errors_total", "Failed checks.", "counter"), checkErrors),
		one(m("config_last_reload_successful", "1 if the last config load succeeded.", "gauge"), b2f(reloadOK)),
		{Name: "imageadvisor_build_info", Help: "Build information.", Type: "gauge", Samples: []metrics.Sample{{Labels: map[string]string{
			"version": bi.Version, "revision": bi.Revision, "goversion": bi.GoVersion}, Value: 1}}},
		one(m("build_timestamp_seconds", "Build time of the binary.", "gauge"), float64(bi.BuildTime)),
	}
}
