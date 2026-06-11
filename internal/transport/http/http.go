package httptransport

import (
	"encoding/json"
	"errors"
	"strings"
)

func ReadQueryInput(rawQuery string) (json.RawMessage, error) {
	for rawQuery != "" {
		param := rawQuery
		if before, after, ok := strings.Cut(rawQuery, "&"); ok {
			param = before
			rawQuery = after
		} else {
			rawQuery = ""
		}

		key, value, hasValue := strings.Cut(param, "=")
		if key != "input" {
			continue
		}
		if !hasValue || value == "" {
			return nil, nil
		}

		return queryUnescapeBytes(value)
	}

	return nil, nil
}

func IsContentType(header string, contentType string) bool {
	mediaType, _, _ := strings.Cut(header, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), contentType)
}

func AcceptsContentType(header string, contentType string) bool {
	for header != "" {
		var part string
		part, header, _ = strings.Cut(header, ",")
		mediaType, _, _ := strings.Cut(part, ";")
		if strings.EqualFold(strings.TrimSpace(mediaType), contentType) {
			return true
		}
	}
	return false
}

func EndpointWithInput(endpoint string, rawInput []byte) string {
	const inputPrefix = "?input="

	out := make([]byte, 0, len(endpoint)+len(inputPrefix)+queryEscapedLen(rawInput))
	out = append(out, endpoint...)
	out = append(out, inputPrefix...)
	out = appendQueryEscaped(out, rawInput)
	return string(out)
}

func queryUnescapeBytes(value string) (json.RawMessage, error) {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '+', '%':
			return appendQueryUnescaped(make([]byte, 0, len(value)), value)
		}
	}

	return json.RawMessage(value), nil
}

func appendQueryUnescaped(dst []byte, value string) (json.RawMessage, error) {
	for i := 0; i < len(value); i++ {
		switch ch := value[i]; ch {
		case '+':
			dst = append(dst, ' ')
		case '%':
			if i+2 >= len(value) {
				return nil, errors.New("invalid query escape")
			}

			hi, ok := fromHex(value[i+1])
			if !ok {
				return nil, errors.New("invalid query escape")
			}
			lo, ok := fromHex(value[i+2])
			if !ok {
				return nil, errors.New("invalid query escape")
			}

			dst = append(dst, hi<<4|lo)
			i += 2
		default:
			dst = append(dst, ch)
		}
	}

	return dst, nil
}

func fromHex(ch byte) (byte, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0', true
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10, true
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10, true
	default:
		return 0, false
	}
}

func queryEscapedLen(value []byte) int {
	n := 0
	for _, ch := range value {
		if isQuerySafe(ch) || ch == ' ' {
			n++
			continue
		}
		n += 3
	}
	return n
}

func appendQueryEscaped(dst []byte, value []byte) []byte {
	const upperhex = "0123456789ABCDEF"

	for _, ch := range value {
		switch {
		case isQuerySafe(ch):
			dst = append(dst, ch)
		case ch == ' ':
			dst = append(dst, '+')
		default:
			dst = append(dst, '%', upperhex[ch>>4], upperhex[ch&0x0f])
		}
	}
	return dst
}

func isQuerySafe(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z':
		return true
	case ch >= 'A' && ch <= 'Z':
		return true
	case ch >= '0' && ch <= '9':
		return true
	case ch == '-' || ch == '_' || ch == '.' || ch == '~':
		return true
	default:
		return false
	}
}
