package busybar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDecodeInputEvents(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		zigzag   byte
		expected int
	}{
		{name: "clockwise", zigzag: 2, expected: 1},
		{name: "counterclockwise", zigzag: 1, expected: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoder := []byte{0x08, test.zigzag}
			input := append([]byte{0x1a, byte(len(encoder))}, encoder...)
			update := append([]byte{0x5a, byte(len(input))}, input...)
			state := append([]byte{0x12, byte(len(update))}, update...)
			events := DecodeInputEvents(state)
			if len(events) != 1 || events[0].EncoderDelta != test.expected {
				t.Fatalf("events = %#v, want delta %d", events, test.expected)
			}
		})
	}
}

func TestStreamInputsAuthenticatesWebSocketHandshake(t *testing.T) {
	t.Parallel()
	type observation struct {
		header http.Header
		query  string
		kind   websocket.MessageType
		enable string
		err    error
	}
	observed := make(chan observation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			observed <- observation{err: err}
			return
		}
		defer connection.CloseNow()
		kind, payload, err := connection.Read(request.Context())
		observed <- observation{
			header: request.Header.Clone(),
			query:  request.URL.Query().Get("x-api-token"),
			kind:   kind,
			enable: string(payload),
			err:    err,
		}
	}))
	defer server.Close()

	client := New(server.URL, "secret-token")
	client.apiVersion = "25.0.0"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = client.StreamInputs(ctx, func(InputEvent) {})

	select {
	case result := <-observed:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if got := result.header.Get("X-API-Token"); got != "secret-token" {
			t.Fatalf("X-API-Token = %q, want secret-token", got)
		}
		if got := result.header.Get(apiVersionHeader); got != "25.0.0" {
			t.Fatalf("%s = %q, want 25.0.0", apiVersionHeader, got)
		}
		if result.query != "secret-token" {
			t.Fatalf("legacy query token = %q, want secret-token", result.query)
		}
		if result.kind != websocket.MessageText {
			t.Fatalf("enable message type = %d, want text", result.kind)
		}
		if result.enable != `{"enable":true}` {
			t.Fatalf("enable message = %q", result.enable)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for WebSocket handshake")
	}
}
