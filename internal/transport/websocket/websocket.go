package websocket

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	guid        = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	OpcodeText  = 0x1
	OpcodeClose = 0x8
)

type UpgradeResponse struct {
	StatusCode int
	Header     map[string][]string
}

func HTTPToURL(raw string) string {
	if rest, ok := strings.CutPrefix(raw, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(raw, "http://"); ok {
		return "ws://" + rest
	}
	return raw
}

func Dial(ctx context.Context, rawURL string, headers http.Header) (net.Conn, *bufio.Reader, error) {
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
		addr += defaultPort(parsed.Scheme)
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
			_ = rawConn.Close()
			return nil, nil, err
		}
		conn = tlsConn
	}

	key, err := newKey()
	if err != nil {
		_ = conn.Close()
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
	for name, values := range headers {
		if !IsValidHTTPHeaderName(name) {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("invalid websocket request header name %q", name)
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				_ = conn.Close()
				return nil, nil, fmt.Errorf("invalid websocket request header value for %q", name)
			}
			fmt.Fprintf(&req, "%s: %s\r\n", name, value)
		}
	}
	req.WriteString("\r\n")

	if _, err := conn.Write(req.Bytes()); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	reader := bufio.NewReader(conn)
	res, err := ReadUpgradeResponse(reader)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	if res.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("websocket upgrade failed with status %d", res.StatusCode)
	}
	if !HeaderMapContains(res.Header, "Upgrade", "websocket") || !HeaderMapContains(res.Header, "Connection", "upgrade") {
		_ = conn.Close()
		return nil, nil, errors.New("websocket upgrade response missing upgrade headers")
	}
	if got, want := HeaderMapGet(res.Header, "Sec-WebSocket-Accept"), Accept(key); got != want {
		_ = conn.Close()
		return nil, nil, errors.New("websocket upgrade response has invalid accept key")
	}

	return conn, reader, nil
}

func defaultPort(scheme string) string {
	if scheme == "wss" {
		return ":443"
	}
	return ":80"
}

func ReadUpgradeResponse(reader *bufio.Reader) (UpgradeResponse, error) {
	const (
		maxStatusLineBytes = 8 << 10
		maxHeaderLineBytes = 8 << 10
		maxHeaderBytes     = 64 << 10
		maxHeaderLines     = 100
	)

	statusLine, n, err := readLimitedHTTPLine(reader, maxStatusLineBytes)
	if err != nil {
		return UpgradeResponse{}, fmt.Errorf("read websocket upgrade status: %w", err)
	}
	if !strings.HasPrefix(statusLine, "HTTP/1.") {
		return UpgradeResponse{}, errors.New("websocket upgrade response has invalid http status line")
	}

	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return UpgradeResponse{}, errors.New("websocket upgrade response has malformed http status line")
	}
	statusCode, err := parseHTTPStatusCode(parts[1])
	if err != nil {
		return UpgradeResponse{}, err
	}

	res := UpgradeResponse{
		StatusCode: statusCode,
		Header:     make(map[string][]string),
	}

	total := n
	for lines := 0; lines < maxHeaderLines; lines++ {
		line, n, err := readLimitedHTTPLine(reader, maxHeaderLineBytes)
		if err != nil {
			return UpgradeResponse{}, fmt.Errorf("read websocket upgrade header: %w", err)
		}
		total += n
		if total > maxHeaderBytes {
			return UpgradeResponse{}, errors.New("websocket upgrade response headers too large")
		}
		if line == "" {
			return res, nil
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return UpgradeResponse{}, errors.New("websocket upgrade response contains folded header line")
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return UpgradeResponse{}, errors.New("websocket upgrade response contains malformed header line")
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return UpgradeResponse{}, errors.New("websocket upgrade response contains invalid header name")
		}

		key := strings.ToLower(name)
		res.Header[key] = append(res.Header[key], strings.TrimSpace(value))
	}

	return UpgradeResponse{}, errors.New("websocket upgrade response has too many headers")
}

func readLimitedHTTPLine(reader *bufio.Reader, limit int) (string, int, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		line = append(line, fragment...)
		if len(line) > limit {
			return "", len(line), errors.New("http line too long")
		}
		if err == nil {
			break
		}
		if err != bufio.ErrBufferFull {
			return "", len(line), err
		}
	}
	if !bytes.HasSuffix(line, []byte("\r\n")) {
		return "", len(line), errors.New("http line missing crlf terminator")
	}
	return string(bytes.TrimSuffix(line, []byte("\r\n"))), len(line), nil
}

func parseHTTPStatusCode(value string) (int, error) {
	if len(value) != 3 {
		return 0, errors.New("websocket upgrade response has invalid http status code")
	}
	code := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, errors.New("websocket upgrade response has invalid http status code")
		}
		code = code*10 + int(ch-'0')
	}
	return code, nil
}

func HeaderMapGet(header map[string][]string, name string) string {
	values := header[strings.ToLower(name)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func HeaderMapContains(header map[string][]string, name string, value string) bool {
	for _, field := range header[strings.ToLower(name)] {
		for _, part := range strings.Split(field, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return true
			}
		}
	}
	return false
}

func IsValidHTTPHeaderName(name string) bool {
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

func IsRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerContains(r.Header, "Connection", "upgrade") &&
		r.Header.Get("Sec-WebSocket-Key") != ""
}

func WriteUpgrade(rw *bufio.ReadWriter, accept string) error {
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return err
	}
	if _, err := rw.WriteString("Upgrade: websocket\r\n"); err != nil {
		return err
	}
	if _, err := rw.WriteString("Connection: Upgrade\r\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rw, "Sec-WebSocket-Accept: %s\r\n", accept); err != nil {
		return err
	}
	if _, err := rw.WriteString("\r\n"); err != nil {
		return err
	}
	return rw.Flush()
}

func newKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate websocket key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw[:]), nil
}

func Accept(key string) string {
	sum := sha1.Sum([]byte(key + guid))
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

func WriteFrame(w io.Writer, opcode byte, payload []byte, mask bool) error {
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

func ReadFrame(r *bufio.Reader, expectMasked bool) ([]byte, byte, error) {
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
	if !expectMasked && masked {
		return nil, 0, errors.New("unexpected masked websocket frame")
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
