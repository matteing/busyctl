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
	Color             string `json:"color,omitempty"`
	Path              string `json:"path,omitempty"`
	Display           string `json:"display"`
	Opacity           *int   `json:"opacity,omitempty"`
	Loop              bool   `json:"loop,omitempty"`
	AwaitPreviousEnd  bool   `json:"await_previous_end,omitempty"`
	Section           string `json:"section,omitempty"`
	Timeout           int    `json:"timeout,omitempty"`
	Width             int    `json:"width,omitempty"`
	ScrollRate        *int   `json:"scroll_rate,omitempty"`
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
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
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

func (c *Client) DeleteAssets(ctx context.Context, applicationName string) error {
	path := "/api/assets/upload?" + url.Values{"application_name": {applicationName}}.Encode()
	req, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
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

func (c *Client) ClearAll(ctx context.Context) error {
	req, err := c.request(ctx, http.MethodDelete, "/api/display/draw", nil)
	if err != nil {
		return err
	}
	return c.send(req, nil)
}

// StreamInputs receives physical BUSY Bar input events until the context is
// canceled or the WebSocket connection closes.
func (c *Client) StreamInputs(ctx context.Context, onInput func(InputEvent)) error {
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
		query.Set("x-api-token", c.token)
	}
	endpoint.RawQuery = query.Encode()

	connection, _, err := websocket.Dial(ctx, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("connect BUSY Bar input stream: %w", err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"enable":true}`)); err != nil {
		return fmt.Errorf("enable BUSY Bar input stream: %w", err)
	}
	for {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("read BUSY Bar input stream: %w", err)
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		for _, event := range DecodeInputEvents(payload) {
			onInput(event)
		}
	}
}

// DecodeInputEvents extracts encoder deltas from BSB_State.State protobuf
// frames. The decoder intentionally understands only the tiny input subset we
// consume and safely skips every other protobuf field.
func DecodeInputEvents(payload []byte) []InputEvent {
	var events []InputEvent
	for len(payload) > 0 {
		field, wire, value, rest, ok := nextProtoField(payload)
		if !ok {
			break
		}
		payload = rest
		if field != 2 || wire != 2 {
			continue
		}
		for len(value) > 0 {
			updateField, updateWire, input, updateRest, valid := nextProtoField(value)
			if !valid {
				break
			}
			value = updateRest
			if updateField != 11 || updateWire != 2 {
				continue
			}
			for len(input) > 0 {
				inputField, inputWire, encoder, inputRest, valid := nextProtoField(input)
				if !valid {
					break
				}
				input = inputRest
				if inputField != 3 || inputWire != 2 {
					continue
				}
				if delta, found := decodeEncoderDelta(encoder); found {
					events = append(events, InputEvent{EncoderDelta: delta})
				}
			}
		}
	}
	return events
}

func decodeEncoderDelta(payload []byte) (int, bool) {
	for len(payload) > 0 {
		field, wire, value, rest, ok := nextProtoField(payload)
		if !ok {
			return 0, false
		}
		payload = rest
		if field == 1 && wire == 0 {
			encoded, _, ok := consumeVarint(value)
			if !ok {
				return 0, false
			}
			return int(encoded>>1) ^ -int(encoded&1), true
		}
	}
	return 0, false
}

func nextProtoField(payload []byte) (field int, wire int, value, rest []byte, ok bool) {
	key, keyBytes, ok := consumeVarint(payload)
	if !ok {
		return 0, 0, nil, nil, false
	}
	field, wire = int(key>>3), int(key&7)
	payload = payload[keyBytes:]
	switch wire {
	case 0:
		_, size, valid := consumeVarint(payload)
		if !valid {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:size], payload[size:], true
	case 1:
		if len(payload) < 8 {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:8], payload[8:], true
	case 2:
		length, size, valid := consumeVarint(payload)
		if !valid || length > uint64(len(payload)-size) {
			return 0, 0, nil, nil, false
		}
		start, end := size, size+int(length)
		return field, wire, payload[start:end], payload[end:], true
	case 5:
		if len(payload) < 4 {
			return 0, 0, nil, nil, false
		}
		return field, wire, payload[:4], payload[4:], true
	default:
		return 0, 0, nil, nil, false
	}
}

func consumeVarint(payload []byte) (uint64, int, bool) {
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
	req.Close = true
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
