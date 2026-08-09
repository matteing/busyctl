package minecraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseServerAddress(t *testing.T) {
	tests := []struct {
		name         string
		address      string
		host         string
		port         uint16
		explicitPort bool
	}{
		{name: "hostname", address: "mc.matteing.com", host: "mc.matteing.com", port: 25565},
		{name: "trim surrounding space", address: "  mc.matteing.com  ", host: "mc.matteing.com", port: 25565},
		{name: "hostname and port", address: "mc.matteing.com:25570", host: "mc.matteing.com", port: 25570, explicitPort: true},
		{name: "IPv4 and port", address: "127.0.0.1:12345", host: "127.0.0.1", port: 12345, explicitPort: true},
		{name: "raw IPv6", address: "::1", host: "::1", port: 25565},
		{name: "bracketed IPv6", address: "[::1]", host: "::1", port: 25565},
		{name: "IPv6 and port", address: "[::1]:25570", host: "::1", port: 25570, explicitPort: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, port, explicitPort, err := parseServerAddress(test.address)
			if err != nil {
				t.Fatal(err)
			}
			if host != test.host || port != test.port || explicitPort != test.explicitPort {
				t.Fatalf("parseServerAddress(%q) = %q, %d, %t; want %q, %d, %t", test.address, host, port, explicitPort, test.host, test.port, test.explicitPort)
			}
		})
	}
}

func TestParseServerAddressRejectsInvalidValues(t *testing.T) {
	tooLong := strings.Repeat("a", maxServerAddressSize+1)
	for _, address := range []string{
		"", " ", ":25565", "mc.matteing.com:", "mc.matteing.com:not-a-port",
		"mc.matteing.com:0", "mc.matteing.com:65536", "not:valid:ipv6",
		"[not-ip]", "[::1", "http://mc.matteing.com", "space host", tooLong,
	} {
		t.Run(address, func(t *testing.T) {
			if _, _, _, err := parseServerAddress(address); err == nil {
				t.Fatalf("parseServerAddress(%q) unexpectedly succeeded", address)
			}
		})
	}
}

func TestStatusClientFetchesStatusFromLocalServer(t *testing.T) {
	observed := make(chan observedStatusRequest, 1)
	address, done := listenOnce(t, func(connection net.Conn) error {
		request, err := readObservedStatusRequest(connection)
		if err != nil {
			return err
		}
		observed <- request
		return writeFull(connection, statusJSONPacket(`{
			"description":{"text":"Test server"},
			"players":{"max":5,"online":2},
			"favicon":"data:image/png;base64,ignored"
		}`))
	})

	status, err := newStatusClient(time.Second).Fetch(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if status != (Status{Online: true, PlayersOnline: 2, PlayersMax: 5}) {
		t.Fatalf("status = %#v", status)
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	request := <-observed
	if request.Protocol != statusProtocolVersion || request.Host != host || request.Port != uint16(port) || request.NextState != 1 {
		t.Fatalf("handshake = %#v", request)
	}
	awaitServer(t, done)
}

func TestStatusClientUsesSRVAndPreservesHandshakeHostname(t *testing.T) {
	observed := make(chan observedStatusRequest, 1)
	address, done := listenOnce(t, func(connection net.Conn) error {
		request, err := readObservedStatusRequest(connection)
		if err != nil {
			return err
		}
		observed <- request
		return writeFull(connection, statusJSONPacket(`{"players":{"max":12,"online":7}}`))
	})
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	var lookupService, lookupProto, lookupName string
	client := newStatusClient(time.Second).(*statusClient)
	client.resolver = lookupSRVFunc(func(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
		lookupService, lookupProto, lookupName = service, proto, name
		return "", []*net.SRV{{Target: "127.0.0.1.", Port: uint16(port)}}, nil
	})
	status, err := client.Fetch(context.Background(), "play.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if status.PlayersOnline != 7 || status.PlayersMax != 12 {
		t.Fatalf("status = %#v", status)
	}
	if lookupService != "minecraft" || lookupProto != "tcp" || lookupName != "play.example.test" {
		t.Fatalf("LookupSRV called with %q, %q, %q", lookupService, lookupProto, lookupName)
	}
	request := <-observed
	if request.Host != "play.example.test" || request.Port != defaultMinecraftPort {
		t.Fatalf("SRV handshake = %#v; want original hostname and port", request)
	}
	awaitServer(t, done)
}

func TestStatusClientFallsBackToDefaultPortWhenSRVIsMissing(t *testing.T) {
	observed := make(chan observedStatusRequest, 1)
	listenerAddress, done := listenOnce(t, func(connection net.Conn) error {
		request, err := readObservedStatusRequest(connection)
		if err != nil {
			return err
		}
		observed <- request
		return writeFull(connection, statusJSONPacket(`{"players":{"max":3,"online":1}}`))
	})

	dialed := make(chan string, 1)
	client := newStatusClient(time.Second).(*statusClient)
	client.resolver = lookupSRVFunc(func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	})
	client.dialer = dialContextFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed <- address
		return (&net.Dialer{}).DialContext(ctx, network, listenerAddress)
	})

	status, err := client.Fetch(context.Background(), "play.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if status.PlayersOnline != 1 || status.PlayersMax != 3 {
		t.Fatalf("status = %#v", status)
	}
	if address := <-dialed; address != "play.example.test:25565" {
		t.Fatalf("fallback dial address = %q", address)
	}
	request := <-observed
	if request.Host != "play.example.test" || request.Port != defaultMinecraftPort {
		t.Fatalf("fallback handshake = %#v", request)
	}
	awaitServer(t, done)
}

func TestStatusClientDeadlineInterruptsResponseRead(t *testing.T) {
	requestRead := make(chan struct{})
	address, done := listenOnce(t, func(connection net.Conn) error {
		if _, err := readObservedStatusRequest(connection); err != nil {
			return err
		}
		close(requestRead)
		_, err := io.Copy(io.Discard, connection)
		return err
	})

	started := time.Now()
	_, err := newStatusClient(75*time.Millisecond).Fetch(context.Background(), address)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline took %s", elapsed)
	}
	select {
	case <-requestRead:
	default:
		t.Fatal("server did not receive status request")
	}
	awaitServer(t, done)
}

func TestReadStatusResponseRejectsMalformedPackets(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{name: "zero packet length", packet: []byte{0}},
		{name: "non-canonical packet length", packet: []byte{0x81, 0x00, 0x00}},
		{name: "overflowing packet length", packet: []byte{0xff, 0xff, 0xff, 0xff, 0x10}},
		{name: "oversized packet", packet: appendVarInt(nil, maxStatusPacketSize+1)},
		{name: "truncated packet", packet: append(appendVarInt(nil, 4), 0, 1)},
		{name: "wrong packet ID", packet: framedPacket(appendVarInt(nil, 1))},
		{name: "declared JSON too long", packet: framedPacket([]byte{0, 10, '{'})},
		{name: "trailing packet bytes", packet: framedPacket([]byte{0, 2, '{', '}', 0})},
		{name: "invalid JSON", packet: statusJSONPacket(`{`)},
		{name: "missing players", packet: statusJSONPacket(`{"version":{"protocol":776}}`)},
		{name: "negative player count", packet: statusJSONPacket(`{"players":{"max":5,"online":-1}}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readStatusResponse(bytes.NewReader(test.packet)); err == nil {
				t.Fatal("readStatusResponse() unexpectedly succeeded")
			}
		})
	}
}

func TestReadVarIntRoundTripAndStrictness(t *testing.T) {
	for _, value := range []int32{-1, 0, 1, 127, 128, 25565, 2147483647, -2147483648} {
		encoded := appendVarInt(nil, value)
		decoded, err := readVarInt(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", value, err)
		}
		if decoded != value {
			t.Fatalf("readVarInt(%d) = %d", value, decoded)
		}
	}
	for _, invalid := range [][]byte{
		{0x80, 0x00},
		{0xff, 0xff, 0xff, 0xff, 0x10},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x00},
	} {
		if _, err := readVarInt(bytes.NewReader(invalid)); err == nil {
			t.Fatalf("readVarInt(%x) unexpectedly succeeded", invalid)
		}
	}
}

type lookupSRVFunc func(context.Context, string, string, string) (string, []*net.SRV, error)

func (function lookupSRVFunc) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return function(ctx, service, proto, name)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func (function dialContextFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return function(ctx, network, address)
}

type observedStatusRequest struct {
	Protocol  int32
	Host      string
	Port      uint16
	NextState int32
}

func listenOnce(t *testing.T, handle func(net.Conn) error) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		done <- handle(connection)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return listener.Addr().String(), done
}

func awaitServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fake status server: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fake status server did not finish")
	}
}

func readObservedStatusRequest(reader io.Reader) (observedStatusRequest, error) {
	var observed observedStatusRequest
	packet, err := readFramedPacket(reader)
	if err != nil {
		return observed, fmt.Errorf("read handshake: %w", err)
	}
	contents := bytes.NewReader(packet)
	packetID, err := readVarInt(contents)
	if err != nil || packetID != 0 {
		return observed, fmt.Errorf("handshake packet ID = %d, error = %v", packetID, err)
	}
	if observed.Protocol, err = readVarInt(contents); err != nil {
		return observed, err
	}
	hostLength, err := readVarInt(contents)
	if err != nil || hostLength < 0 || int(hostLength) > contents.Len() {
		return observed, fmt.Errorf("invalid handshake hostname length %d: %v", hostLength, err)
	}
	host := make([]byte, int(hostLength))
	if _, err := io.ReadFull(contents, host); err != nil {
		return observed, err
	}
	observed.Host = string(host)
	if err := binary.Read(contents, binary.BigEndian, &observed.Port); err != nil {
		return observed, err
	}
	if observed.NextState, err = readVarInt(contents); err != nil {
		return observed, err
	}
	if contents.Len() != 0 {
		return observed, fmt.Errorf("handshake has %d trailing bytes", contents.Len())
	}

	request, err := readFramedPacket(reader)
	if err != nil {
		return observed, fmt.Errorf("read status request: %w", err)
	}
	if !bytes.Equal(request, []byte{0}) {
		return observed, fmt.Errorf("status request = %x, want 00", request)
	}
	return observed, nil
}

func readFramedPacket(reader io.Reader) ([]byte, error) {
	length, err := readVarInt(reader)
	if err != nil {
		return nil, err
	}
	if length < 0 || length > maxStatusPacketSize {
		return nil, fmt.Errorf("invalid packet length %d", length)
	}
	packet := make([]byte, int(length))
	_, err = io.ReadFull(reader, packet)
	return packet, err
}

func statusJSONPacket(payload string) []byte {
	body := appendVarInt(nil, 0)
	body = appendVarInt(body, int32(len(payload)))
	body = append(body, payload...)
	return framedPacket(body)
}

func framedPacket(body []byte) []byte {
	packet := appendVarInt(nil, int32(len(body)))
	return append(packet, body...)
}
