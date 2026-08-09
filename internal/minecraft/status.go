package minecraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultMinecraftPort = 25565
	defaultStatusTimeout = 5 * time.Second
	maxStatusPacketSize  = 1 << 20
	maxServerAddressSize = 255
	// The status state reports its real protocol version even when the client
	// announces a different one. -1 is the conventional version-independent
	// value used by status-only clients.
	statusProtocolVersion int32 = -1
)

type Status struct {
	Online        bool
	PlayersOnline int
	PlayersMax    int
}

type statusFetcher interface {
	Fetch(context.Context, string) (Status, error)
}

type contextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type srvResolver interface {
	LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error)
}

type statusClient struct {
	timeout  time.Duration
	dialer   contextDialer
	resolver srvResolver
}

func newStatusClient(timeout time.Duration) statusFetcher {
	if timeout <= 0 {
		timeout = defaultStatusTimeout
	}
	return &statusClient{
		timeout:  timeout,
		dialer:   &net.Dialer{},
		resolver: net.DefaultResolver,
	}
}

// parseServerAddress accepts a hostname or IP with an optional port. Raw and
// bracketed IPv6 literals without a port are accepted; IPv6 ports must use the
// standard [address]:port form.
func parseServerAddress(address string) (host string, port uint16, explicitPort bool, err error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false, errors.New("Minecraft server address is empty")
	}

	port = defaultMinecraftPort
	switch {
	case strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]"):
		host = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
		if _, parseErr := netip.ParseAddr(host); parseErr != nil {
			return "", 0, false, fmt.Errorf("invalid bracketed IPv6 server address %q", address)
		}
	case strings.Count(address, ":") == 0:
		host = address
	case strings.Count(address, ":") == 1 || strings.HasPrefix(address, "["):
		var portText string
		host, portText, err = net.SplitHostPort(address)
		if err != nil {
			return "", 0, false, fmt.Errorf("invalid Minecraft server address %q: %w", address, err)
		}
		parsedPort, parseErr := strconv.ParseUint(portText, 10, 16)
		if parseErr != nil || parsedPort == 0 {
			return "", 0, false, fmt.Errorf("invalid Minecraft server port %q", portText)
		}
		port = uint16(parsedPort)
		explicitPort = true
	default:
		if _, parseErr := netip.ParseAddr(address); parseErr != nil {
			return "", 0, false, fmt.Errorf("invalid unbracketed IPv6 server address %q", address)
		}
		host = address
	}

	if err := validateServerHost(host); err != nil {
		return "", 0, false, err
	}
	return host, port, explicitPort, nil
}

func validateServerHost(host string) error {
	if host == "" {
		return errors.New("Minecraft server hostname is empty")
	}
	if len(host) > maxServerAddressSize {
		return fmt.Errorf("Minecraft server hostname is longer than %d bytes", maxServerAddressSize)
	}
	if strings.ContainsAny(host, "/\\") {
		return fmt.Errorf("invalid Minecraft server hostname %q", host)
	}
	for _, character := range host {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return fmt.Errorf("invalid Minecraft server hostname %q", host)
		}
	}
	return nil
}

func (client *statusClient) Fetch(ctx context.Context, address string) (Status, error) {
	queryCtx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	host, port, explicitPort, err := parseServerAddress(address)
	if err != nil {
		return Status{}, err
	}
	endpoint, err := client.endpoint(queryCtx, host, port, explicitPort)
	if err != nil {
		return Status{}, err
	}

	connection, err := client.dialer.DialContext(queryCtx, "tcp", endpoint)
	if err != nil {
		return Status{}, contextAwareError(queryCtx, fmt.Errorf("connect to Minecraft server %s: %w", address, err))
	}
	defer connection.Close()

	// DialContext only observes cancellation while dialing. Moving the socket
	// deadline when the context ends also interrupts an in-progress read/write.
	stopCancellation := context.AfterFunc(queryCtx, func() {
		_ = connection.SetDeadline(time.Now())
	})
	defer stopCancellation()

	if err := writeStatusRequest(connection, host, port); err != nil {
		return Status{}, contextAwareError(queryCtx, fmt.Errorf("request Minecraft status from %s: %w", address, err))
	}
	status, err := readStatusResponse(connection)
	if err != nil {
		return Status{}, contextAwareError(queryCtx, fmt.Errorf("read Minecraft status from %s: %w", address, err))
	}
	return status, nil
}

func (client *statusClient) endpoint(ctx context.Context, host string, port uint16, explicitPort bool) (string, error) {
	if explicitPort || isIPAddress(host) {
		return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
	}

	_, records, err := client.resolver.LookupSRV(ctx, "minecraft", "tcp", host)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("resolve Minecraft server %s: %w", host, ctxErr)
	}
	if err == nil {
		for _, record := range records {
			target := strings.TrimSuffix(record.Target, ".")
			if target != "" && record.Port != 0 {
				return net.JoinHostPort(target, strconv.Itoa(int(record.Port))), nil
			}
		}
	}

	// Missing, malformed, and temporarily unavailable SRV data all fall back to
	// the normal Java Edition port. The subsequent dial still resolves host A/
	// AAAA records and produces the useful connection error when it cannot.
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func isIPAddress(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

func contextAwareError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Minecraft status query: %w", err)
	}
	return fallback
}

func writeStatusRequest(writer io.Writer, host string, port uint16) error {
	handshake := appendVarInt(nil, 0)
	handshake = appendVarInt(handshake, statusProtocolVersion)
	handshake = appendVarInt(handshake, int32(len(host)))
	handshake = append(handshake, host...)
	handshake = binary.BigEndian.AppendUint16(handshake, port)
	handshake = appendVarInt(handshake, 1)

	request := appendVarInt(nil, int32(len(handshake)))
	request = append(request, handshake...)
	// A status request contains only packet ID zero, so its frame is 01 00.
	request = append(request, 1, 0)
	if err := writeFull(writer, request); err != nil {
		return err
	}
	return nil
}

func readStatusResponse(reader io.Reader) (Status, error) {
	packetLength, err := readVarInt(reader)
	if err != nil {
		return Status{}, fmt.Errorf("read packet length: %w", err)
	}
	if packetLength <= 0 || packetLength > maxStatusPacketSize {
		return Status{}, fmt.Errorf("invalid status packet length %d", packetLength)
	}

	packet := make([]byte, int(packetLength))
	if _, err := io.ReadFull(reader, packet); err != nil {
		return Status{}, fmt.Errorf("read %d-byte status packet: %w", packetLength, err)
	}
	contents := bytes.NewReader(packet)
	packetID, err := readVarInt(contents)
	if err != nil {
		return Status{}, fmt.Errorf("read status packet ID: %w", err)
	}
	if packetID != 0 {
		return Status{}, fmt.Errorf("unexpected status packet ID %d", packetID)
	}
	jsonLength, err := readVarInt(contents)
	if err != nil {
		return Status{}, fmt.Errorf("read status JSON length: %w", err)
	}
	if jsonLength < 0 || jsonLength > maxStatusPacketSize {
		return Status{}, fmt.Errorf("invalid status JSON length %d", jsonLength)
	}
	if int(jsonLength) > contents.Len() {
		return Status{}, fmt.Errorf("status JSON declares %d bytes but packet has %d", jsonLength, contents.Len())
	}

	payload := make([]byte, int(jsonLength))
	if _, err := io.ReadFull(contents, payload); err != nil {
		return Status{}, fmt.Errorf("read status JSON: %w", err)
	}
	if contents.Len() != 0 {
		return Status{}, fmt.Errorf("status packet has %d trailing bytes", contents.Len())
	}

	var response struct {
		Players *struct {
			Online int `json:"online"`
			Max    int `json:"max"`
		} `json:"players"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return Status{}, fmt.Errorf("decode status JSON: %w", err)
	}
	if response.Players == nil {
		return Status{}, errors.New("status JSON does not contain player counts")
	}
	if response.Players.Online < 0 || response.Players.Max < 0 {
		return Status{}, fmt.Errorf("status JSON contains negative player counts: %d/%d", response.Players.Online, response.Players.Max)
	}
	return Status{
		Online:        true,
		PlayersOnline: response.Players.Online,
		PlayersMax:    response.Players.Max,
	}, nil
}

func appendVarInt(destination []byte, value int32) []byte {
	remaining := uint32(value)
	for {
		current := byte(remaining & 0x7f)
		remaining >>= 7
		if remaining != 0 {
			current |= 0x80
		}
		destination = append(destination, current)
		if remaining == 0 {
			return destination
		}
	}
}

func readVarInt(reader io.Reader) (int32, error) {
	var value uint32
	for index := 0; index < 5; index++ {
		var encoded [1]byte
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return 0, err
		}
		current := encoded[0]
		if index == 4 && current&0xf0 != 0 {
			return 0, errors.New("VarInt overflows 32 bits")
		}
		value |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			decoded := int32(value)
			if varIntSize(decoded) != index+1 {
				return 0, errors.New("VarInt uses a non-canonical encoding")
			}
			return decoded, nil
		}
	}
	return 0, errors.New("VarInt is longer than 5 bytes")
}

func varIntSize(value int32) int {
	size := 1
	for remaining := uint32(value) >> 7; remaining != 0; remaining >>= 7 {
		size++
	}
	return size
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) != 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
