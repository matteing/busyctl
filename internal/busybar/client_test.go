package busybar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestClientConnectAndDraw(t *testing.T) {
	t.Parallel()

	var drawRequest DisplayElements
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"result":"ok"}`
		switch r.URL.Path {
		case "/api/version":
			body = `{"api_semver":"25.0.0"}`
		case "/api/display/draw":
			if got := r.Header.Get(apiVersionHeader); got != "25.0.0" {
				t.Errorf("API version header = %q", got)
			}
			if got := r.Header.Get("X-API-Token"); got != "1234" {
				t.Errorf("token header = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&drawRequest); err != nil {
				t.Errorf("decode draw request: %v", err)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	client := New("busybar.test", "1234")
	client.http.Transport = transport
	if _, err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := DisplayElements{
		ApplicationName: "test_app",
		Priority:        100,
		Elements: []Element{
			{ID: "title", Type: "text", X: 18, Y: 1, Text: "Song", Font: "tiny", Display: "front"},
		},
	}
	if err := client.Draw(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if drawRequest.ApplicationName != want.ApplicationName || len(drawRequest.Elements) != 1 {
		t.Fatalf("unexpected draw request: %#v", drawRequest)
	}
}

func TestClearAllOmitsApplicationQuery(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/display/draw" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"result":"ok"}`)),
			Request:    r,
		}, nil
	})

	client := New("busybar.test", "")
	client.http.Transport = transport
	if err := client.ClearAll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeInputEventsExtractsSignedEncoderDelta(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		encoded byte
		want    int
	}{
		{name: "clockwise", encoded: 2, want: 1},
		{name: "counterclockwise", encoded: 1, want: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoder := []byte{0x08, test.encoded}
			input := append([]byte{0x1a, byte(len(encoder))}, encoder...)
			update := append([]byte{0x5a, byte(len(input))}, input...)
			state := append([]byte{0x12, byte(len(update))}, update...)
			events := DecodeInputEvents(state)
			if len(events) != 1 || events[0].EncoderDelta != test.want {
				t.Fatalf("events = %#v, want delta %d", events, test.want)
			}
		})
	}
}
