// SPDX-License-Identifier: AGPL-3.0-or-later

// Package docker is a minimal read-only client for the Docker Engine API.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MinAPIVersion is the oldest supported Docker API version.
const MinAPIVersion = "1.41"

type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	ImageID string            `json:"ImageID"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`
}

// Name is the container name without the leading slash.
func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

type ContainerJSON struct {
	ID     string `json:"Id"`
	Image  string `json:"Image"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
}

type Image struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
}

type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
}

// Relevant reports whether an event can change what ImageAdvisor reports.
func (e Event) Relevant() bool {
	switch e.Type {
	case "container":
		switch e.Action {
		case "create", "start", "stop", "die", "destroy":
			return true
		}
	case "image":
		switch e.Action {
		case "pull", "tag", "untag", "delete":
			return true
		}
	}
	return false
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type Client struct {
	hc      *http.Client
	stream  *http.Client
	version string
}

// New creates a client for the Docker socket (Linux) or named pipe (Windows).
func New(socket string) *Client {
	d := dialer(socket)
	return &Client{
		hc:      &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: d}},
		stream:  &http.Client{Transport: &http.Transport{DialContext: d}},
		version: MinAPIVersion,
	}
}

// NewWithHTTP uses a custom transport (tests).
func NewWithHTTP(rt http.RoundTripper) *Client {
	return &Client{hc: &http.Client{Transport: rt}, stream: &http.Client{Transport: rt}, version: MinAPIVersion}
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker API %d: %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, p string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v"+c.version+p, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(b, &m) != nil || m.Message == "" {
			m.Message = strings.TrimSpace(string(b))
		}
		return &APIError{Status: resp.StatusCode, Message: m.Message}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CheckVersion reads the daemon's API version and fails below MinAPIVersion.
// It returns the version and whether the daemon was reached.
func (c *Client) CheckVersion(ctx context.Context) (version string, reached bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/version", nil)
	if err != nil {
		return "", false, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	var v struct {
		APIVersion string `json:"ApiVersion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", true, err
	}
	if CompareVersion(v.APIVersion, MinAPIVersion) < 0 {
		return v.APIVersion, true, fmt.Errorf("Docker API %s is older than the required %s", v.APIVersion, MinAPIVersion)
	}
	return v.APIVersion, true, nil
}

// CompareVersion compares dotted versions numerically.
func CompareVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var r []Container
	err := c.do(ctx, "/containers/json?all=1", nil, &r)
	return r, err
}

func (c *Client) Container(ctx context.Context, id string) (ContainerJSON, error) {
	var r ContainerJSON
	err := c.do(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &r)
	return r, err
}

// Image inspects an image by ID or reference; an unknown one is an *APIError with Status 404.
func (c *Client) Image(ctx context.Context, idOrRef string) (Image, error) {
	var r Image
	err := c.do(ctx, "/images/"+idOrRef+"/json", nil, &r)
	return r, err
}

// Distribution asks the daemon for the registry digest of an image reference.
// auth is the X-Registry-Auth value, or empty.
func (c *Client) Distribution(ctx context.Context, ref, auth string) (string, error) {
	var r struct {
		Descriptor struct {
			Digest string `json:"digest"`
		} `json:"Descriptor"`
	}
	var h http.Header
	if auth != "" {
		h = http.Header{"X-Registry-Auth": {auth}}
	}
	err := c.do(ctx, "/distribution/"+ref+"/json", h, &r)
	return r.Descriptor.Digest, err
}

// Events streams container and image events until ctx ends or the stream breaks.
func (c *Client) Events(ctx context.Context, fn func(Event)) error {
	filters := url.QueryEscape(`{"type":["container","image"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v"+c.version+"/events?filters="+filters, nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Message: "events"}
	}
	dec := json.NewDecoder(bufio.NewReader(resp.Body))
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			return err
		}
		fn(e)
	}
}
