package binary

import (
	"bytes"
	"errors"
	"reflect"
)

// ContentType is Neo's protobuf-like binary media type.
//
// The wire format is a self-describing TLV stream: each value starts with a
// compact kind byte, integers use varints, lists carry an item count, and
// objects carry field numbers plus JSON-tagged field names. Field numbers make
// the format stable for Go-to-Go typed clients, while names keep it usable
// without generated .proto files.
const ContentType = "application/x-neo-bin"

var binaryMagic = []byte{'N', 'E', 'O', '1'}

const (
	binaryKindNull byte = iota
	binaryKindFalse
	binaryKindTrue
	binaryKindInt
	binaryKindUint
	binaryKindFloat
	binaryKindString
	binaryKindBytes
	binaryKindList
	binaryKindObject
)

// Codec encodes Neo request and response envelopes using Neo's compact
// protobuf-like TLV format.
type Codec struct{}

// Default is the shared binary codec used by the server and by clients
// configured with WithBinaryCodec.
var Default Codec

func (Codec) Marshal(value any) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(binaryMagic)
	if err := writeBinaryValue(&buf, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (Codec) Unmarshal(raw []byte, value any) error {
	if len(raw) < len(binaryMagic) || !bytes.Equal(raw[:len(binaryMagic)], binaryMagic) {
		return errors.New("invalid neo binary message")
	}

	out := reflect.ValueOf(value)
	if !out.IsValid() || out.Kind() != reflect.Pointer || out.IsNil() {
		return errors.New("binary unmarshal target must be a non-nil pointer")
	}

	decoder := binaryValueDecoder{raw: raw[len(binaryMagic):]}
	decoded, err := decoder.readValue()
	if err != nil {
		return err
	}
	if decoder.off != len(decoder.raw) {
		return errors.New("trailing bytes in neo binary message")
	}

	return assignBinaryValue(out.Elem(), decoded)
}
