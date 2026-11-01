// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/motwok/ImageAdvisor/internal/config"
	"github.com/motwok/ImageAdvisor/internal/core"
	"github.com/motwok/ImageAdvisor/internal/docker"
)

func TestParseFlags(t *testing.T) {
	f, err := ParseFlags(nil, io.Discard)
	if err != nil || f.ConfigFile != "/etc/imageadvisor/config.yml" || f.StoragePath != "/var/lib/imageadvisor" ||
		f.DockerSocket != docker.DefaultSocket || f.Listen != ":8080" || f.LogLevel != "info" || f.LogFormat != "logfmt" {
		t.Fatalf("defaults: %+v %v", f, err)
	}
	f, err = ParseFlags([]string{"--config.file=/data/c.yml", "--storage.path=/data", "--log.format=json", "--log.level=debug"}, io.Discard)
	if err != nil || f.ConfigFile != "/data/c.yml" || f.StoragePath != "/data" || f.LogFormat != "json" || f.LogLevel != "debug" {
		t.Fatalf("set: %+v %v", f, err)
	}
	for _, bad := range [][]string{{"--nope"}, {"--log.level=loud"}, {"--log.format=xml"}, {"extra"}} {
		if _, err := ParseFlags(bad, io.Discard); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

func TestLogger(t *testing.T) {
	var b bytes.Buffer
	NewLogger(Flags{LogLevel: "warn", LogFormat: "json"}, &b).Warn("x", "k", 1)
	if !strings.Contains(b.String(), `"msg":"x"`) {
		t.Fatalf("json: %s", b.String())
	}
	b.Reset()
	NewLogger(Flags{LogLevel: "error", LogFormat: "logfmt"}, &b).Warn("hidden")
	if b.Len() != 0 {
		t.Fatal("level not applied")
	}
	if !strings.Contains(VersionString(), "imageadvisor dev") {
		t.Fatal(VersionString())
	}
}

type handlerRT struct{ h http.Handler }

func (r handlerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w.Result(), nil
}

// fakeDaemon answers the calls of one reconcile with no containers.
func fakeDaemon(api string) *docker.Client {
	return docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			io.WriteString(w, `{"ApiVersion":"`+api+`"}`)
		default:
			io.WriteString(w, `[]`)
		}
	})})
}

func newServer(t *testing.T, cfgText, api string) *Server {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yml")
	if cfgText != "" {
		if err := os.WriteFile(file, []byte(cfgText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{Flags: Flags{ConfigFile: file}, Log: log, reload: make(chan chan error), trigger: make(chan struct{}, 1)}
	s.cfgStat = stamp(file)
	s.Core = core.New(config.Default(), core.Options{Docker: fakeDaemon(api), StorageDir: dir, Log: log})
	return s
}

func get(h http.Handler, method, p string) (int, string) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, p, nil))
	return w.Code, w.Body.String()
}

func TestHandler(t *testing.T) {
	s := newServer(t, "global: { check_interval: 1h }", "1.47")
	h := s.Handler()
	if code, _ := get(h, "GET", "/-/healthy"); code != 200 {
		t.Fatal("healthy")
	}
	if code, _ := get(h, "GET", "/-/ready"); code != 503 {
		t.Fatal("ready before reconcile")
	}
	if err := s.Core.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(h, "GET", "/-/ready"); code != 200 {
		t.Fatal("ready after reconcile")
	}
	code, body := get(h, "GET", "/metrics")
	if code != 200 || !strings.Contains(body, "imageadvisor_build_info") || !strings.Contains(body, "imageadvisor_reconcile_errors_total 0") {
		t.Fatalf("metrics %d:\n%s", code, body)
	}
	if code, _ := get(h, "GET", "/-/reload"); code != 405 {
		t.Fatal("reload GET")
	}
	go func() {
		res := <-s.reload
		res <- s.Reload()
		res2 := <-s.reload
		os.WriteFile(s.Flags.ConfigFile, []byte("bad: ["), 0o644)
		res2 <- s.Reload()
	}()
	if code, body := get(h, "POST", "/-/reload"); code != 200 {
		t.Fatalf("reload: %d %s", code, body)
	}
	if code, _ := get(h, "POST", "/-/reload"); code != 500 {
		t.Fatal("bad reload must fail")
	}
	if _, body = get(h, "GET", "/metrics"); !strings.Contains(body, "imageadvisor_config_last_reload_successful 0") {
		t.Fatal("reload failure not visible")
	}
}

func TestReloadRequestCanceled(t *testing.T) {
	s := newServer(t, "", "1.47")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/-/reload", nil).WithContext(ctx))
	if strings.Contains(w.Body.String(), "reloaded") {
		t.Fatal("no reload expected")
	}
}

func TestLoop(t *testing.T) {
	s := newServer(t, "global: { reconcile_interval: 5ms }", "1.47")
	ctx, cancel := context.WithCancel(context.Background())
	hup := make(chan os.Signal, 1)
	done := make(chan int)
	go func() { done <- s.loop(ctx, hup) }()
	s.poke()
	s.poke()
	hup <- syscall.SIGHUP
	res := make(chan error, 1)
	s.reload <- res
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if got := s.Core.Config().Global.ReconcileInterval.D(); got != 5*time.Millisecond {
		t.Fatalf("interval %v", got)
	}
	// A changed file is picked up at the next reconcile.
	os.WriteFile(s.Flags.ConfigFile, []byte("global: { reconcile_interval: 6ms }  "), 0o644)
	for i := 0; i < 400 && s.Core.Config().Global.ReconcileInterval.D() != 6*time.Millisecond; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d", code)
	}
	if s.Core.Config().Global.ReconcileInterval.D() != 6*time.Millisecond {
		t.Fatal("changed file not reloaded")
	}
}

// A registry check asks for a reconcile through the core's channel.
func TestLoopReconcileRequest(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	dc := docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version":
			io.WriteString(w, `{"ApiVersion":"1.47"}`)
		case r.URL.Path == "/v1.41/containers/json":
			calls.Add(1)
			io.WriteString(w, `[{"Id":"c1","Names":["/web"],"ImageID":"sha256:a","State":"running"}]`)
		case r.URL.Path == "/v1.41/containers/c1/json":
			io.WriteString(w, `{"Config":{"Image":"nginx:1"}}`)
		case r.URL.Path == "/v1.41/images/sha256:a/json":
			io.WriteString(w, `{"Id":"sha256:a"}`)
		case strings.HasPrefix(r.URL.Path, "/v1.41/distribution/"):
			io.WriteString(w, `{"Descriptor":{"digest":"sha256:b"}}`)
		default:
			http.NotFound(w, r)
		}
	})})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{Flags: Flags{ConfigFile: filepath.Join(dir, "none")}, Log: log, reload: make(chan chan error), trigger: make(chan struct{}, 1)}
	s.Core = core.New(config.Default(), core.Options{Docker: dc, StorageDir: dir, Log: log})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- s.loop(ctx, nil) }()
	go s.Core.RunChecks(ctx)
	for i := 0; i < 400 && calls.Load() < 2; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() < 2 {
		t.Fatal("no reconcile after the check")
	}
}

// A stopped loop runs no reconcile on the dead context.
func TestLoopStopped(t *testing.T) {
	s := newServer(t, "", "1.47")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := s.loop(ctx, nil); code != 0 || s.Core.Ready() {
		t.Fatalf("exit %d, ready %v", code, s.Core.Ready())
	}
}

func TestLoopDockerTooOld(t *testing.T) {
	s := newServer(t, "", "1.40")
	if code := s.loop(context.Background(), nil); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

func TestWatchEvents(t *testing.T) {
	s := newServer(t, "", "1.47")
	calls := 0
	dc := docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		io.WriteString(w, `{"Type":"image","Action":"pull"}`+"\n")
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	s.watchEvents(ctx, dc, time.Second, time.Minute)
	if calls != 2 || len(s.trigger) != 1 {
		t.Fatalf("calls %d, trigger %d", calls, len(s.trigger))
	}
}

func TestWatchEventsCanceledInStream(t *testing.T) {
	s := newServer(t, "", "1.47")
	dc := docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	s.watchEvents(ctx, dc, time.Second, time.Minute)
}

func TestWatchEventsBackoff(t *testing.T) {
	s := newServer(t, "", "1.47")
	calls := 0
	dc := docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// Every second stream lasts longer than the maximum and resets the backoff.
		if calls++; calls%2 == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.watchEvents(ctx, dc, time.Millisecond, 3*time.Millisecond)
	if calls < 4 {
		t.Fatalf("calls %d", calls)
	}
}

func TestWaitDoneTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitDone(ctx, nil)
}

func TestMainExitCodes(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"--version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "imageadvisor") {
		t.Fatalf("version: %d %s", code, out.String())
	}
	if code := Main([]string{"--bogus"}, &out, &errb); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	if code := Main([]string{"-h"}, &out, &errb); code != 0 {
		t.Fatalf("help: %d", code)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "c.yml")
	os.WriteFile(bad, []byte("nope: 1"), 0o644)
	if code := Main([]string{"--config.file=" + bad, "--storage.path=" + dir}, &out, &errb); code != 1 {
		t.Fatalf("bad config: %d", code)
	}
	if code := Main([]string{"--config.file=" + filepath.Join(dir, "none"), "--storage.path=" + dir,
		"--web.listen-address=256.0.0.1:1", "--docker.socket="}, &out, &errb); code != 1 {
		t.Fatalf("listen failure: %d", code)
	}
}

func TestRunUntilCanceled(t *testing.T) {
	dir := t.TempDir()
	var errb bytes.Buffer
	f := Flags{ConfigFile: filepath.Join(dir, "none"), StoragePath: dir, Listen: "127.0.0.1:0", DockerSocket: "",
		LogLevel: "info", LogFormat: "logfmt"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if code := run(ctx, f, NewLogger(f, &errb), nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "no config file") || !strings.Contains(errb.String(), "stopped") {
		t.Fatal(errb.String())
	}
}

func TestRunWithDocker(t *testing.T) {
	orig := newDocker
	defer func() { newDocker = orig }()
	newDocker = func(string) *docker.Client { return fakeDaemon("1.47") }
	dir := t.TempDir()
	var errb bytes.Buffer
	f := Flags{ConfigFile: filepath.Join(dir, "none"), StoragePath: dir, Listen: "127.0.0.1:0",
		DockerSocket: "fake", LogLevel: "info", LogFormat: "logfmt"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if code := run(ctx, f, NewLogger(f, &errb), nil); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}
