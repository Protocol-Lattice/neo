package neo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

func decodeZigZag(value uint64) int64 {
	return int64(value>>1) ^ -int64(value&1)
}

type binaryValueDecoder struct {
	raw []byte
	off int
}

func (decoder *binaryValueDecoder) readValue() (any, error) {
	kind, err := decoder.readByte()
	if err != nil {
		return nil, err
	}

	switch kind {
	case binaryKindNull:
		return nil, nil
	case binaryKindFalse:
		return false, nil
	case binaryKindTrue:
		return true, nil
	case binaryKindInt:
		value, err := decoder.readUvarint()
		if err != nil {
			return nil, err
		}
		return decodeZigZag(value), nil
	case binaryKindUint:
		return decoder.readUvarint()
	case binaryKindFloat:
		raw, err := decoder.readBytesOfLen(8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(raw)), nil
	case binaryKindString:
		raw, err := decoder.readBytes()
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	case binaryKindBytes:
		raw, err := decoder.readBytes()
		if err != nil {
			return nil, err
		}
		return raw, nil
	case binaryKindList:
		count, err := decoder.readUvarint()
		if err != nil {
			return nil, err
		}
		if count > uint64(len(decoder.raw)-decoder.off) {
			return nil, errors.New("neo binary list is too large")
		}
		values := make([]any, 0, count)
		for i := uint64(0); i < count; i++ {
			value, err := decoder.readValue()
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case binaryKindObject:
		count, err := decoder.readUvarint()
		if err != nil {
			return nil, err
		}
		if count > uint64(len(decoder.raw)-decoder.off) {
			return nil, errors.New("neo binary object is too large")
		}
		fields := make(binaryObject, 0, count)
		for i := uint64(0); i < count; i++ {
			number, err := decoder.readUvarint()
			if err != nil {
				return nil, err
			}
			nameRaw, err := decoder.readBytes()
			if err != nil {
				return nil, err
			}
			value, err := decoder.readValue()
			if err != nil {
				return nil, err
			}
			fields = append(fields, binaryField{
				number: number,
				name:   string(nameRaw),
				value:  value,
			})
		}
		return fields, nil
	default:
		return nil, fmt.Errorf("unknown neo binary kind %d", kind)
	}
}

func (decoder *binaryValueDecoder) readByte() (byte, error) {
	if decoder.off >= len(decoder.raw) {
		return 0, errors.New("unexpected end of neo binary message")
	}
	value := decoder.raw[decoder.off]
	decoder.off++
	return value, nil
}

func (decoder *binaryValueDecoder) readUvarint() (uint64, error) {
	value, n := binary.Uvarint(decoder.raw[decoder.off:])
	if n <= 0 {
		return 0, errors.New("invalid neo binary varint")
	}
	decoder.off += n
	return value, nil
}

func (decoder *binaryValueDecoder) readBytes() ([]byte, error) {
	size, err := decoder.readUvarint()
	if err != nil {
		return nil, err
	}
	if size > uint64(len(decoder.raw)-decoder.off) {
		return nil, errors.New("neo binary bytes exceed message size")
	}
	return decoder.readBytesOfLen(int(size))
}

func (decoder *binaryValueDecoder) readBytesOfLen(size int) ([]byte, error) {
	if size < 0 || decoder.off+size > len(decoder.raw) {
		return nil, errors.New("neo binary bytes exceed message size")
	}
	value := decoder.raw[decoder.off : decoder.off+size]
	decoder.off += size
	return value, nil
}
