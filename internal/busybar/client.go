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

	"github.com/coder/websocket"
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

type InputEvent struct {
	EncoderDelta int
}

type Element struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	X                 int    `json:"x"`
	Y                 int    `json:"y"`
	Text              string `json:"text,omitempty"`
	Font              string `json:"font,omitempty"`
	Align             string `json:"align,omitempty"`
	Color             string `json:"color,omitempty"`
	Path              string `json:"path,omitempty"`
	Display           string `json:"display"`
	Opacity           *int   `json:"opacity,omitempty"`
	Width             int    `json:"width,omitempty"`
	ScrollRate        *int   `json:"scroll_rate,omitempty"`
	ScrollRepeatDelay int    `json:"scroll_repeat_delay,omitempty"`
}

type Drawing struct {
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
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
	}
}

func (c *Client) Connect(ctx context.Context) (VersionInfo, error) {
	var version VersionInfo
	if err := c.json(ctx, http.MethodGet, "/api/version", nil, &version); err != nil {
		return version, err
	}
	if version.APISemver == "" {
		return version, fmt.Errorf("BUSY Bar returned an empty API version")
	}
	c.apiVersion = version.APISemver
	return version, nil
}

func (c *Client) Draw(ctx context.Context, drawing Drawing) error {
	return c.json(ctx, http.MethodPost, "/api/display/draw", drawing, nil)
}

func (c *Client) Clear(ctx context.Context, application string) error {
	path := "/api/display/draw?" + url.Values{"application_name": {application}}.Encode()
	return c.raw(ctx, http.MethodDelete, path, nil, "")
}

func (c *Client) DeleteAssets(ctx context.Context, application string) error {
	path := "/api/assets/upload?" + url.Values{"application_name": {application}}.Encode()
	return c.raw(ctx, http.MethodDelete, path, nil, "")
}

func (c *Client) UploadAsset(ctx context.Context, application, filename string, payload []byte) error {
	path := "/api/assets/upload?" + url.Values{
		"application_name": {application},
		"file":             {filename},
	}.Encode()
	return c.raw(ctx, http.MethodPost, path, bytes.NewReader(payload), "application/octet-stream")
}

// StreamInputs is the native BUSY Bar input channel. It blocks until the
// connection closes or ctx is canceled, delivering decoded hardware events in
// device order.
func (c *Client) StreamInputs(ctx context.Context, handle func(InputEvent)) error {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("parse BUSY Bar address: %w", err)
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = "/api/status/ws"
	query := endpoint.Query()
	if c.token != "" {
		// Keep the query parameter for compatibility with firmware versions that
		// accepted WebSocket credentials there before the HTTP API standardized
		// on X-API-Token for every endpoint.
		query.Set("x-api-token", c.token)
	}
	endpoint.RawQuery = query.Encode()

	headers := make(http.Header, 2)
	if c.token != "" {
		headers.Set("X-API-Token", c.token)
	}
	if c.apiVersion != "" {
		headers.Set(apiVersionHeader, c.apiVersion)
	}
	connection, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("connect input stream: %w", err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"enable":true}`)); err != nil {
		return fmt.Errorf("enable input stream: %w", err)
	}
	for {
		kind, payload, err := connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("read input stream: %w", err)
		}
		if kind != websocket.MessageBinary {
			continue
		}
		for _, event := range DecodeInputEvents(payload) {
			handle(event)
		}
	}
}

func (c *Client) json(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode BUSY Bar request: %w", err)
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

func (c *Client) raw(ctx context.Context, method, path string, body io.Reader, contentType string) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.send(req, nil)
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
	// Firmware 25 is more reliable when each HTTP mutation owns its connection.
	req.Close = true
	return req, nil
}

func (c *Client) send(req *http.Request, result any) error {
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("BUSY Bar %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read BUSY Bar response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("BUSY Bar %s %s returned %d: %s", req.Method, req.URL.Path, response.StatusCode, message)
	}
	if result != nil && len(payload) != 0 {
		if err := json.Unmarshal(payload, result); err != nil {
			return fmt.Errorf("decode BUSY Bar response: %w", err)
		}
	}
	return nil
}

// DecodeInputEvents extracts encoder deltas from BSB_State.State protobuf
// frames while skipping all state fields the Apple Music app does not consume.
func DecodeInputEvents(payload []byte) []InputEvent {
	var events []InputEvent
	for len(payload) != 0 {
		field, wire, stateUpdate, rest, ok := nextField(payload)
		if !ok {
			break
		}
		payload = rest
		if field != 2 || wire != 2 {
			continue
		}
		for len(stateUpdate) != 0 {
			field, wire, input, updateRest, ok := nextField(stateUpdate)
			if !ok {
				break
			}
			stateUpdate = updateRest
			if field != 11 || wire != 2 {
				continue
			}
			for len(input) != 0 {
				field, wire, encoder, inputRest, ok := nextField(input)
				if !ok {
					break
				}
				input = inputRest
				if field != 3 || wire != 2 {
					continue
				}
				if delta, ok := encoderDelta(encoder); ok {
					events = append(events, InputEvent{EncoderDelta: delta})
				}
			}
		}
	}
	return events
}

func encoderDelta(payload []byte) (int, bool) {
	for len(payload) != 0 {
		field, wire, value, rest, ok := nextField(payload)
		if !ok {
			return 0, false
		}
		payload = rest
		if field == 1 && wire == 0 {
			encoded, _, ok := varint(value)
			if !ok {
				return 0, false
			}
			return int(encoded>>1) ^ -int(encoded&1), true
		}
	}
	return 0, false
}

func nextField(payload []byte) (field, wire int, value, rest []byte, ok bool) {
	key, keySize, ok := varint(payload)
	if !ok {
		return 0, 0, nil, nil, false
	}
	field, wire = int(key>>3), int(key&7)
	payload = payload[keySize:]
	switch wire {
	case 0:
		_, size, ok := varint(payload)
		if !ok {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:size], payload[size:], true
	case 1:
		if len(payload) < 8 {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:8], payload[8:], true
	case 2:
		length, size, ok := varint(payload)
		if !ok || length > uint64(len(payload)-size) {
			return 0, 0, nil, nil, false
		}
		end := size + int(length)
		return field, wire, payload[size:end], payload[end:], true
	case 5:
		if len(payload) < 4 {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:4], payload[4:], true
	default:
		return 0, 0, nil, nil, false
	}
}

func varint(payload []byte) (uint64, int, bool) {
	var value uint64
	for index, current := range payload {
		if index >= 10 {
			return 0, 0, false
		}
		value |= uint64(current&0x7f) << (7 * index)
		if current < 0x80 {
			return value, index + 1, true
		}
	}
	return 0, 0, false
}
