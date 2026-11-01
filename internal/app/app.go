// SPDX-License-Identifier: AGPL-3.0-or-later

// Package app wires flags, config, core and the HTTP server.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/motwok/ImageAdvisor/internal/config"
	"github.com/motwok/ImageAdvisor/internal/core"
	"github.com/motwok/ImageAdvisor/internal/docker"
	"github.com/motwok/ImageAdvisor/internal/metrics"
)

// Replaced in tests.
var newDocker = docker.New

// Set by -ldflags.
var (
	Version   = "dev"
	Revision  = "unknown"
	BuildTime = "0"
)

type Flags struct {
	ConfigFile   string
	StoragePath  string
	DockerSocket string
	Listen       string
	LogLevel     string
	LogFormat    string
	Version      bool
}

// ParseFlags parses the command line (spec 3). Errors mean exit 2.
func ParseFlags(args []string, out io.Writer) (Flags, error) {
	var f Flags
	fs := flag.NewFlagSet("imageadvisor", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&f.ConfigFile, "config.file", "/etc/imageadvisor/config.yml", "config file")
	fs.StringVar(&f.StoragePath, "storage.path", "/var/lib/imageadvisor", "directory of state.json")
	fs.StringVar(&f.DockerSocket, "docker.socket", docker.DefaultSocket, "Docker socket or named pipe")
	fs.StringVar(&f.Listen, "web.listen-address", ":8080", "listen address for /metrics")
	fs.StringVar(&f.LogLevel, "log.level", "info", "debug, info, warn or error")
	fs.StringVar(&f.LogFormat, "log.format", "logfmt", "logfmt or json")
	fs.BoolVar(&f.Version, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if _, err := parseLevel(f.LogLevel); err != nil {
		return f, err
	}
	if f.LogFormat != "logfmt" && f.LogFormat != "json" {
		return f, fmt.Errorf("invalid --log.format %q", f.LogFormat)
	}
	return f, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid --log.level %q", s)
}

func NewLogger(f Flags, w io.Writer) *slog.Logger {
	lvl, _ := parseLevel(f.LogLevel)
	opts := &slog.HandlerOptions{Level: lvl}
	if f.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func VersionString() string {
	return fmt.Sprintf("imageadvisor %s (revision %s, %s)", Version, Revision, runtime.Version())
}

func buildInfo() core.BuildInfo {
	var t int64
	fmt.Sscan(BuildTime, &t)
	return core.BuildInfo{Version: Version, Revision: Revision, GoVersion: runtime.Version(), BuildTime: t}
}

// Server holds the running parts.
type Server struct {
	Flags   Flags
	Log     *slog.Logger
	Core    *core.Core
	reload  chan chan error
	trigger chan struct{}
	cfgStat fileStamp
}

type fileStamp struct {
	exists bool
	mod    time.Time
	size   int64
}

func stamp(file string) fileStamp {
	fi, err := os.Stat(file)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{true, fi.ModTime(), fi.Size()}
}

// Reload loads the config file and activates it if valid (CFG-4, CFG-5).
func (s *Server) Reload() error {
	s.cfgStat = stamp(s.Flags.ConfigFile)
	cfg, _, err := config.Load(s.Flags.ConfigFile, os.LookupEnv, true)
	if err != nil {
		s.Core.ReloadFailed()
		s.Log.Error("config reload failed", "file", s.Flags.ConfigFile, "err", err)
		return err
	}
	s.Core.SetConfig(cfg)
	s.Log.Info("config reloaded", "file", s.Flags.ConfigFile)
	return nil
}

// Handler serves /metrics and the lifecycle endpoints (spec 12).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", metrics.ContentType)
		metrics.Write(w, s.Core.Collect(buildInfo()))
	})
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ImageAdvisor is healthy.\n")
	})
	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, _ *http.Request) {
		if !s.Core.Ready() {
			http.Error(w, "ImageAdvisor is not ready.", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ImageAdvisor is ready.\n")
	})
	mux.HandleFunc("/-/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "only POST", http.StatusMethodNotAllowed)
			return
		}
		res := make(chan error, 1)
		select {
		case s.reload <- res:
		case <-r.Context().Done():
			return
		}
		if err := <-res; err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		io.WriteString(w, "config reloaded\n")
	})
	return mux
}

// Main runs ImageAdvisor and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	f, err := ParseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if f.Version {
		fmt.Fprintln(stdout, VersionString())
		return 0
	}
	log := NewLogger(f, stderr)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	return run(ctx, f, log, hup)
}

// run starts everything and blocks until ctx ends or the Docker API is too old.
func run(ctx context.Context, f Flags, log *slog.Logger, hup <-chan os.Signal) int {
	s := &Server{Flags: f, Log: log, reload: make(chan chan error), trigger: make(chan struct{}, 1)}
	s.cfgStat = stamp(f.ConfigFile)
	cfg, found, err := config.Load(f.ConfigFile, os.LookupEnv, false)
	if err != nil {
		log.Error("invalid config", "file", f.ConfigFile, "err", err)
		return 1
	}
	if !found {
		log.Info("no config file, using defaults", "file", f.ConfigFile)
	}
	var dk core.Docker
	var dc *docker.Client
	if f.DockerSocket != "" {
		dc = newDocker(f.DockerSocket)
		dk = dc
	}
	s.Core = core.New(cfg, core.Options{Docker: dk, StorageDir: f.StoragePath, Log: log})
	log.Info("starting", "version", Version, "revision", Revision, "listen", f.Listen)

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	ln, err := net.Listen("tcp", f.Listen)
	if err != nil {
		log.Error("listen failed", "err", err)
		return 1
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)

	checksDone := make(chan struct{})
	go func() { s.Core.RunChecks(ctx); close(checksDone) }()
	if dc != nil {
		go s.watchEvents(ctx, dc, time.Second, time.Minute)
	}
	code := s.loop(ctx, hup)

	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	waitDone(shutdown, checksDone)
	s.Core.Save()
	srv.Shutdown(shutdown)
	log.Info("stopped")
	return code
}

func waitDone(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// loop runs the reconciles (DIS-1) until ctx ends; 1 means the Docker API is too old.
func (s *Server) loop(ctx context.Context, hup <-chan os.Signal) int {
	for {
		if st := stamp(s.Flags.ConfigFile); st != s.cfgStat {
			s.Reload()
		}
		if err := s.Core.Reconcile(ctx); err != nil {
			s.Log.Error("unsupported Docker", "err", err)
			return 1
		}
		timer := time.NewTimer(s.Core.Config().Global.ReconcileInterval.D())
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		case <-s.trigger:
			timer.Stop()
		case <-s.Core.ReconcileRequests():
			timer.Stop()
		case <-hup:
			timer.Stop()
			s.Reload()
		case res := <-s.reload:
			timer.Stop()
			res <- s.Reload()
		}
	}
}

func (s *Server) poke() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// watchEvents triggers a reconcile on relevant Docker events (DIS-1, DIS-2).
func (s *Server) watchEvents(ctx context.Context, dc *docker.Client, minRetry, maxRetry time.Duration) {
	backoff := minRetry
	for ctx.Err() == nil {
		start := time.Now()
		err := dc.Events(ctx, func(e docker.Event) {
			if e.Relevant() {
				s.poke()
			}
		})
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > maxRetry {
			backoff = minRetry
		}
		s.Log.Debug("Docker event stream lost", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxRetry {
			backoff = maxRetry
		}
	}
}
