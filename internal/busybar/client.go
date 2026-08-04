package busybar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiVersionHeader = "X-Busy-Api-Version"

type Client struct {
	baseURL    string
	token      string
	apiVersion string
	http       *http.Client
}

type VersionInfo struct {
	APISemver string `json:"api_semver"`
}

type Element struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	X                 int    `json:"x"`
	Y                 int    `json:"y"`
	Text              string `json:"text,omitempty"`
	Font              string `json:"font,omitempty"`
	Color             string `json:"color,omitempty"`
	Path              string `json:"path,omitempty"`
	Display           string `json:"display"`
	Opacity           int    `json:"opacity,omitempty"`
	Timeout           int    `json:"timeout,omitempty"`
	Width             int    `json:"width,omitempty"`
	ScrollRate        int    `json:"scroll_rate,omitempty"`
	ScrollStartDelay  int    `json:"scroll_start_delay,omitempty"`
	ScrollRepeatDelay int    `json:"scroll_repeat_delay,omitempty"`
}

type DisplayElements struct {
	ApplicationName string    `json:"application_name"`
	Priority        int       `json:"priority"`
	Elements        []Element `json:"elements"`
}

func New(host, token string) *Client {
	host = strings.TrimSpace(host)
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return &Client{
		baseURL: strings.TrimRight(host, "/"),
		token:   strings.TrimSpace(token),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Connect(ctx context.Context) (VersionInfo, error) {
	var version VersionInfo
	if err := c.doJSON(ctx, http.MethodGet, "/api/version", nil, &version); err != nil {
		return version, err
	}
	if version.APISemver == "" {
		return version, fmt.Errorf("BUSY Bar returned an empty API version")
	}
	c.apiVersion = version.APISemver
	return version, nil
}

func (c *Client) UploadAsset(ctx context.Context, applicationName, filename string, data []byte) error {
	query := url.Values{
		"application_name": {applicationName},
		"file":             {filename},
	}
	path := "/api/assets/upload?" + query.Encode()
	req, err := c.request(ctx, http.MethodPost, path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	return c.send(req, nil)
}

func (c *Client) Draw(ctx context.Context, drawing DisplayElements) error {
	return c.doJSON(ctx, http.MethodPost, "/api/display/draw", drawing, nil)
}

func (c *Client) Clear(ctx context.Context, applicationName string) error {
	path := "/api/display/draw?" + url.Values{"application_name": {applicationName}}.Encode()
	req, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.send(req, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := c.request(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, result)
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create BUSY Bar request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-API-Token", c.token)
	}
	if c.apiVersion != "" {
		req.Header.Set(apiVersionHeader, c.apiVersion)
	}
	return req, nil
}

func (c *Client) send(req *http.Request, result any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("BUSY Bar %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	const maxResponse = 1 << 20
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("read BUSY Bar response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("BUSY Bar %s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, message)
	}
	if result != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, result); err != nil {
			return fmt.Errorf("decode BUSY Bar response: %w", err)
		}
	}
	return nil
}
