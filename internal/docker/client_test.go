// SPDX-License-Identifier: AGPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type rt struct{ h http.Handler }

func (r rt) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w.Result(), nil
}

func fake(api string) *Client {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ApiVersion":"`+api+`"}`)
	})
	mux.HandleFunc("/v1.41/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			http.Error(w, "all missing", 400)
			return
		}
		io.WriteString(w, `[{"Id":"abc","Names":["/web-1"],"Image":"nginx:1.29","ImageID":"sha256:1","State":"running","Created":5}]`)
	})
	mux.HandleFunc("/v1.41/containers/abc/json", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"Id":"abc","Image":"sha256:1","Config":{"Image":"ghcr.io/motwok/imageadvisor:latest"}}`)
	})
	mux.HandleFunc("/v1.41/images/sha256:1/json", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"RepoDigests":["ghcr.io/motwok/imageadvisor@sha256:aa"]}`)
	})
	mux.HandleFunc("/v1.41/distribution/", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"Descriptor":{"digest":"sha256:bb"}}`)
	})
	mux.HandleFunc("/v1.41/containers/missing/json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"message":"No such container"}`)
	})
	return NewWithHTTP(rt{mux})
}

func TestClient(t *testing.T) {
	ctx := context.Background()
	c := fake("1.47")
	if v, reached, err := c.CheckVersion(ctx); err != nil || !reached || v != "1.47" {
		t.Fatalf("version: %v %v %v", v, reached, err)
	}
	cs, err := c.Containers(ctx)
	if err != nil || len(cs) != 1 || cs[0].Name() != "web-1" || cs[0].Image != "nginx:1.29" || cs[0].ImageID != "sha256:1" {
		t.Fatalf("containers: %v %v", cs, err)
	}
	if j, err := c.Container(ctx, "abc"); err != nil || j.Config.Image != "ghcr.io/motwok/imageadvisor:latest" {
		t.Fatalf("container: %v %v", j, err)
	}
	if d, err := c.ImageRepoDigests(ctx, "sha256:1"); err != nil || d[0] != "ghcr.io/motwok/imageadvisor@sha256:aa" {
		t.Fatalf("digests: %v %v", d, err)
	}
	if d, err := c.DistributionDigest(ctx, "ghcr.io/motwok/imageadvisor:latest"); err != nil || d != "sha256:bb" {
		t.Fatalf("distribution: %v %v", d, err)
	}
	_, err = c.Container(ctx, "missing")
	if e, ok := err.(*APIError); !ok || e.Status != 404 || !strings.Contains(e.Error(), "No such container") {
		t.Fatalf("api error: %v", err)
	}
}

func TestOldDaemon(t *testing.T) {
	if _, reached, err := fake("1.40").CheckVersion(context.Background()); err == nil || !reached {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestCompareVersion(t *testing.T) {
	if CompareVersion("1.41", "1.41") != 0 || CompareVersion("1.9", "1.41") >= 0 || CompareVersion("1.47", "1.41") <= 0 ||
		CompareVersion("2", "1.99") <= 0 {
		t.Fatal("CompareVersion")
	}
}

func TestName(t *testing.T) {
	if (Container{ID: "x"}).Name() != "x" {
		t.Fatal("name fallback")
	}
}

type errRT struct{}

func (errRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("dial failed") }

func TestErrors(t *testing.T) {
	var nilCtx context.Context
	c := fake("1.47")
	if _, err := c.Containers(nilCtx); err == nil {
		t.Error("get: bad request")
	}
	if _, _, err := c.CheckVersion(nilCtx); err == nil {
		t.Error("version: bad request")
	}

	ctx := context.Background()
	d := NewWithHTTP(errRT{})
	if _, err := d.Containers(ctx); err == nil {
		t.Error("get: dial")
	}
	if _, reached, err := d.CheckVersion(ctx); err == nil || reached {
		t.Error("version: dial")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "plain failure\n")
	})
	e := NewWithHTTP(rt{mux})
	if _, err := e.Containers(ctx); err == nil || err.(*APIError).Message != "plain failure" {
		t.Errorf("plain error: %v", err)
	}
	if _, reached, err := e.CheckVersion(ctx); err == nil || !reached {
		t.Errorf("version: bad body: %v", err)
	}
}

func TestUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ApiVersion":"1.47"}`)
	}))
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	if v, _, err := New(sock).CheckVersion(context.Background()); err != nil || v != "1.47" {
		t.Fatalf("%q %v", v, err)
	}
}
