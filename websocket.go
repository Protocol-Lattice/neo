package neo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	webSocketGUID        = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	webSocketOpcodeText  = 0x1
	webSocketOpcodeClose = 0x8
)

// SubscribeWebSocketTyped opens a WebSocket subscription and decodes each
// streamed result into Out. It is useful for browsers, CLIs, and long-lived
// subscriptions where a WebSocket transport is preferable to NDJSON over HTTP.
func SubscribeWebSocketTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (<-chan Out, error) {
	raw, err := procedure.SubscribeWebSocket(ctx, input)
	if err != nil {
		return nil, err
	}

	out := make(chan Out)
	go func() {
		defer close(out)
		for value := range raw {
			decoded, err := decodeClientValue[Out](value)
			if err != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case out <- decoded:
			}
		}
	}()

	return out, nil
}

// SubscribeWebSocket opens this procedure as a WebSocket subscription.
func (procedure *ClientProcedure) SubscribeWebSocket(ctx context.Context, input any) (<-chan any, error) {
	return procedure.client.subscribeWebSocket(ctx, procedure.key, input)
}

func (client *Client) subscribeWebSocket(ctx context.Context, key string, input any) (<-chan any, error) {
	httpURL, err := client.subscriptionURL(key, input)
	if err != nil {
		return nil, err
	}
	wsURL := httpToWebSocketURL(httpURL)

	conn, reader, err := client.dialWebSocket(ctx, wsURL)
	if err != nil {
		return nil, fmt.Errorf("subscribe websocket procedure %q: %w", key, err)
	}

	out := make(chan any)
	go func() {
		defer conn.Close()
		defer close(out)
		for {
			payload, opcode, err := readWebSocketFrame(reader, false)
			if err != nil {
				return
			}
			if opcode == webSocketOpcodeClose {
				return
			}
			if opcode != webSocketOpcodeText {
				continue
			}

			var rpcRes Response
			if err := json.Unmarshal(payload, &rpcRes); err != nil {
				return
			}
			if rpcRes.Error != "" || rpcRes.Code != "" {
				return
			}

			select {
			case <-ctx.Done():
				return
			case out <- rpcRes.Result:
			}
		}
	}()

	return out, nil
}

func (client *Client) subscriptionURL(key string, input any) (string, error) {
	endpoint := fmt.Sprintf("%s/%s", client.addr, strings.Trim(key, "/"))
	if input == nil {
		return endpoint, nil
	}

	rawInput, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal websocket subscription input: %w", err)
	}
	return endpoint + "?input=" + url.QueryEscape(string(rawInput)), nil
}

func httpToWebSocketURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	default:
		return raw
	}
}

func (client *Client) dialWebSocket(ctx context.Context, rawURL string) (net.Conn, *bufio.Reader, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse websocket URL: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, nil, fmt.Errorf("unsupported websocket scheme %q", parsed.Scheme)
	}

	host := parsed.Host
	addr := host
	if !strings.Contains(addr, ":") {
		if parsed.Scheme == "wss" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}

	var dialer net.Dialer
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	conn := rawConn
	if parsed.Scheme == "wss" {
		serverName := parsed.Hostname()
		tlsConn := tls.Client(rawConn, &tls.Config{ServerName: serverName})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, nil, err
		}
		conn = tlsConn
	}

	key, err := newWebSocketKey()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	path := parsed.RequestURI()
	if path == "" {
		path = "/"
	}

	var req bytes.Buffer
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&req, "Host: %s\r\n", host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", key)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range client.headers {
		if !isValidHTTPHeaderName(name) {
			conn.Close()
			return nil, nil, fmt.Errorf("invalid websocket request header name %q", name)
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				conn.Close()
				return nil, nil, fmt.Errorf("invalid websocket request header value for %q", name)
			}
			fmt.Fprintf(&req, "%s: %s\r\n", name, value)
		}
	}
	req.WriteString("\r\n")

	if _, err := conn.Write(req.Bytes()); err != nil {
		conn.Close()
		return nil, nil, err
	}

	reader := bufio.NewReader(conn)
	res, err := readWebSocketUpgradeResponse(reader)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	if res.statusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, nil, fmt.Errorf("websocket upgrade failed with status %d", res.statusCode)
	}
	if !headerMapContains(res.header, "Upgrade", "websocket") || !headerMapContains(res.header, "Connection", "upgrade") {
		conn.Close()
		return nil, nil, errors.New("websocket upgrade response missing upgrade headers")
	}
	if got, want := headerMapGet(res.header, "Sec-WebSocket-Accept"), webSocketAccept(key); got != want {
		conn.Close()
		return nil, nil, errors.New("websocket upgrade response has invalid accept key")
	}

	return conn, reader, nil
}

type webSocketUpgradeResponse struct {
	statusCode int
	header     map[string][]string
}

func readWebSocketUpgradeResponse(reader *bufio.Reader) (webSocketUpgradeResponse, error) {
	const (
		maxStatusLineBytes = 8 << 10
		maxHeaderLineBytes = 8 << 10
		maxHeaderBytes     = 64 << 10
		maxHeaderLines     = 100
	)

	statusLine, n, err := readLimitedHTTPLine(reader, maxStatusLineBytes)
	if err != nil {
		return webSocketUpgradeResponse{}, fmt.Errorf("read websocket upgrade status: %w", err)
	}
	if !strings.HasPrefix(statusLine, "HTTP/1.") {
		return webSocketUpgradeResponse{}, errors.New("websocket upgrade response has invalid HTTP status line")
	}

	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return webSocketUpgradeResponse{}, errors.New("websocket upgrade response has malformed HTTP status line")
	}
	statusCode, err := parseHTTPStatusCode(parts[1])
	if err != nil {
		return webSocketUpgradeResponse{}, err
	}

	res := webSocketUpgradeResponse{
		statusCode: statusCode,
		header:     make(map[string][]string),
	}

	total := n
	for lines := 0; lines < maxHeaderLines; lines++ {
		line, n, err := readLimitedHTTPLine(reader, maxHeaderLineBytes)
		if err != nil {
			return webSocketUpgradeResponse{}, fmt.Errorf("read websocket upgrade header: %w", err)
		}
		total += n
		if total > maxHeaderBytes {
			return webSocketUpgradeResponse{}, errors.New("websocket upgrade response headers too large")
		}
		if line == "" {
			return res, nil
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return webSocketUpgradeResponse{}, errors.New("websocket upgrade response contains folded header line")
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return webSocketUpgradeResponse{}, errors.New("websocket upgrade response contains malformed header line")
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return webSocketUpgradeResponse{}, errors.New("websocket upgrade response contains invalid header name")
		}

		key := strings.ToLower(name)
		res.header[key] = append(res.header[key], strings.TrimSpace(value))
	}

	return webSocketUpgradeResponse{}, errors.New("websocket upgrade response has too many headers")
}

func readLimitedHTTPLine(reader *bufio.Reader, limit int) (string, int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", len(line), err
	}
	if len(line) > limit {
		return "", len(line), errors.New("HTTP line too long")
	}
	if !strings.HasSuffix(line, "\r\n") {
		return "", len(line), errors.New("HTTP line missing CRLF terminator")
	}
	return strings.TrimSuffix(line, "\r\n"), len(line), nil
}

func parseHTTPStatusCode(value string) (int, error) {
	if len(value) != 3 {
		return 0, errors.New("websocket upgrade response has invalid HTTP status code")
	}
	code := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, errors.New("websocket upgrade response has invalid HTTP status code")
		}
		code = code*10 + int(ch-'0')
	}
	return code, nil
}

func headerMapGet(header map[string][]string, name string) string {
	values := header[strings.ToLower(name)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func headerMapContains(header map[string][]string, name string, value string) bool {
	for _, field := range header[strings.ToLower(name)] {
		for _, part := range strings.Split(field, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return true
			}
		}
	}
	return false
}

func isValidHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", ch):
		default:
			return false
		}
	}
	return true
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerContains(r.Header, "Connection", "upgrade") &&
		r.Header.Get("Sec-WebSocket-Key") != ""
}

func serveWebSocket(w http.ResponseWriter, r *http.Request, stream <-chan any) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeProcedureError(w, NewError(CodeInternal, "websocket upgrade is not supported"))
		return
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	accept := webSocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = fmt.Fprintf(rw, "Upgrade: websocket\r\n")
	_, _ = fmt.Fprintf(rw, "Connection: Upgrade\r\n")
	_, _ = fmt.Fprintf(rw, "Sec-WebSocket-Accept: %s\r\n", accept)
	_, _ = fmt.Fprintf(rw, "\r\n")
	if err := rw.Flush(); err != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case value, ok := <-stream:
			if !ok {
				_ = writeWebSocketFrame(rw, webSocketOpcodeClose, nil, false)
				_ = rw.Flush()
				return
			}

			payload, err := json.Marshal(Response{Result: value})
			if err != nil {
				return
			}
			if err := writeWebSocketFrame(rw, webSocketOpcodeText, payload, false); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
		}
	}
}

func newWebSocketKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate websocket key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw[:]), nil
}

func webSocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + webSocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContains(header http.Header, name string, value string) bool {
	for _, part := range strings.Split(header.Get(name), ",") {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return true
		}
	}
	return false
}

func writeWebSocketFrame(w io.Writer, opcode byte, payload []byte, mask bool) error {
	header := []byte{0x80 | opcode}
	payloadLen := len(payload)

	maskBit := byte(0)
	if mask {
		maskBit = 0x80
	}

	switch {
	case payloadLen < 126:
		header = append(header, maskBit|byte(payloadLen))
	case payloadLen <= 0xffff:
		header = append(header, maskBit|126, byte(payloadLen>>8), byte(payloadLen))
	default:
		header = append(header, maskBit|127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(payloadLen))
		header = append(header, size[:]...)
	}

	if _, err := w.Write(header); err != nil {
		return err
	}

	if !mask {
		_, err := w.Write(payload)
		return err
	}

	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	if _, err := w.Write(key[:]); err != nil {
		return err
	}
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= key[i%4]
	}
	_, err := w.Write(masked)
	return err
}

func readWebSocketFrame(r *bufio.Reader, expectMasked bool) ([]byte, byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	second, err := r.ReadByte()
	if err != nil {
		return nil, 0, err
	}

	opcode := first & 0x0f
	masked := second&0x80 != 0
	if expectMasked && !masked {
		return nil, 0, errors.New("expected masked websocket frame")
	}

	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var buf [2]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, 0, err
		}
		length = uint64(binary.BigEndian.Uint16(buf[:]))
	case 127:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, 0, err
		}
		length = binary.BigEndian.Uint64(buf[:])
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return nil, 0, err
		}
	}

	if length > 64<<20 {
		return nil, 0, errors.New("websocket frame too large")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return payload, opcode, nil
}
