// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/motwok/ImageAdvisor/internal/config"
	"github.com/motwok/ImageAdvisor/internal/core"
	"github.com/motwok/ImageAdvisor/internal/docker"
)

func TestParseFlags(t *testing.T) {
	f, err := ParseFlags(nil, io.Discard)
	if err != nil || f.ConfigFile != "/etc/imageadvisor/config.yml" || f.DockerSocket != "/var/run/docker.sock" ||
		f.Listen != ":8080" || f.LogLevel != "info" || f.LogFormat != "logfmt" {
		t.Fatalf("defaults: %+v %v", f, err)
	}
	f, err = ParseFlags([]string{"--config.file=/data/c.yml", "--log.format=json", "--log.level=debug"}, io.Discard)
	if err != nil || f.ConfigFile != "/data/c.yml" || f.LogFormat != "json" || f.LogLevel != "debug" {
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

func fakeDocker(api string) *docker.Client {
	return docker.NewWithHTTP(handlerRT{http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ApiVersion":"`+api+`"}`)
	})})
}

func newServer(t *testing.T, cfgText, api string) *Server {
	file := filepath.Join(t.TempDir(), "config.yml")
	if cfgText != "" {
		if err := os.WriteFile(file, []byte(cfgText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{Flags: Flags{ConfigFile: file}, Log: log, reload: make(chan chan error)}
	s.cfgStat = stamp(file)
	s.Core = core.New(config.Default(), core.Options{Docker: fakeDocker(api), Log: log})
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
		t.Fatal("ready before check")
	}
	if err := s.Core.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(h, "GET", "/-/ready"); code != 200 {
		t.Fatal("ready after check")
	}
	code, body := get(h, "GET", "/metrics")
	if code != 200 || !strings.Contains(body, "imageadvisor_build_info") || !strings.Contains(body, "imageadvisor_check_errors_total 0") {
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
	s := newServer(t, "global: { check_interval: 5ms }", "1.47")
	ctx, cancel := context.WithCancel(context.Background())
	hup := make(chan os.Signal, 1)
	done := make(chan int)
	go func() { done <- s.loop(ctx, hup) }()
	hup <- syscall.SIGHUP
	res := make(chan error, 1)
	s.reload <- res
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if got := s.Core.Config().Global.CheckInterval.D(); got != 5*time.Millisecond {
		t.Fatalf("interval %v", got)
	}
	// A changed file is picked up on the next pass.
	os.WriteFile(s.Flags.ConfigFile, []byte("global: { check_interval: 6ms }  "), 0o644)
	for i := 0; i < 200 && s.Core.Config().Global.CheckInterval.D() != 6*time.Millisecond; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d", code)
	}
	if s.Core.Config().Global.CheckInterval.D() != 6*time.Millisecond {
		t.Fatal("changed file not reloaded")
	}
}

func TestLoopDockerTooOld(t *testing.T) {
	s := newServer(t, "", "1.40")
	if code := s.loop(context.Background(), nil); code != 1 {
		t.Fatalf("exit %d", code)
	}
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
	if code := Main([]string{"--config.file=" + bad}, &out, &errb); code != 1 {
		t.Fatalf("bad config: %d", code)
	}
	if code := Main([]string{"--config.file=" + filepath.Join(dir, "none"),
		"--web.listen-address=256.0.0.1:1", "--docker.socket="}, &out, &errb); code != 1 {
		t.Fatalf("listen failure: %d", code)
	}
}

func TestMainDockerTooOld(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ApiVersion":"1.40"}`)
	}))
	srv.Listener = l
	srv.Start()
	defer srv.Close()

	var out, errb bytes.Buffer
	code := Main([]string{"--config.file=" + filepath.Join(dir, "none"), "--docker.socket=" + sock,
		"--web.listen-address=127.0.0.1:0"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "unsupported Docker") || !strings.Contains(errb.String(), "no config file") {
		t.Fatalf("exit %d:\n%s", code, errb.String())
	}
}
