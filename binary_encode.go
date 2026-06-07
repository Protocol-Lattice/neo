package neo

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sort"
)

func writeBinaryValue(buf *bytes.Buffer, value reflect.Value) error {
	if !value.IsValid() {
		return buf.WriteByte(binaryKindNull)
	}

	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return buf.WriteByte(binaryKindNull)
		}
		value = value.Elem()
	}

	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return buf.WriteByte(binaryKindNull)
		}
		if value.CanInterface() {
			if marshaler, ok := value.Interface().(encoding.TextMarshaler); ok {
				return writeBinaryStringValue(buf, marshaler)
			}
			if marshaler, ok := value.Interface().(encoding.BinaryMarshaler); ok {
				return writeBinaryBytesValue(buf, marshaler)
			}
		}
		value = value.Elem()
	}

	if value.CanInterface() {
		if marshaler, ok := value.Interface().(encoding.TextMarshaler); ok {
			return writeBinaryStringValue(buf, marshaler)
		}
		if marshaler, ok := value.Interface().(encoding.BinaryMarshaler); ok {
			return writeBinaryBytesValue(buf, marshaler)
		}
	}

	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			return buf.WriteByte(binaryKindTrue)
		}
		return buf.WriteByte(binaryKindFalse)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if err := buf.WriteByte(binaryKindInt); err != nil {
			return err
		}
		writeBinaryUvarint(buf, encodeZigZag(value.Int()))
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if err := buf.WriteByte(binaryKindUint); err != nil {
			return err
		}
		writeBinaryUvarint(buf, value.Uint())
		return nil

	case reflect.Float32, reflect.Float64:
		if err := buf.WriteByte(binaryKindFloat); err != nil {
			return err
		}
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], math.Float64bits(value.Convert(reflect.TypeOf(float64(0))).Float()))
		_, err := buf.Write(raw[:])
		return err

	case reflect.String:
		if err := buf.WriteByte(binaryKindString); err != nil {
			return err
		}
		writeBinaryString(buf, value.String())
		return nil

	case reflect.Slice:
		if value.IsNil() {
			return buf.WriteByte(binaryKindNull)
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if err := buf.WriteByte(binaryKindBytes); err != nil {
				return err
			}
			writeBinaryBytes(buf, value.Bytes())
			return nil
		}
		return writeBinaryList(buf, value)

	case reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if err := buf.WriteByte(binaryKindBytes); err != nil {
				return err
			}
			raw := make([]byte, value.Len())
			for i := 0; i < value.Len(); i++ {
				raw[i] = byte(value.Index(i).Uint())
			}
			writeBinaryBytes(buf, raw)
			return nil
		}
		return writeBinaryList(buf, value)

	case reflect.Map:
		if value.IsNil() {
			return buf.WriteByte(binaryKindNull)
		}
		return writeBinaryMap(buf, value)

	case reflect.Struct:
		return writeBinaryStruct(buf, value)

	default:
		return fmt.Errorf("unsupported binary value %s", value.Type())
	}
}

func writeBinaryStringValue(buf *bytes.Buffer, marshaler encoding.TextMarshaler) error {
	raw, err := marshaler.MarshalText()
	if err != nil {
		return err
	}
	if err := buf.WriteByte(binaryKindString); err != nil {
		return err
	}
	writeBinaryBytes(buf, raw)
	return nil
}

func writeBinaryBytesValue(buf *bytes.Buffer, marshaler encoding.BinaryMarshaler) error {
	raw, err := marshaler.MarshalBinary()
	if err != nil {
		return err
	}
	if err := buf.WriteByte(binaryKindBytes); err != nil {
		return err
	}
	writeBinaryBytes(buf, raw)
	return nil
}

func writeBinaryList(buf *bytes.Buffer, value reflect.Value) error {
	if err := buf.WriteByte(binaryKindList); err != nil {
		return err
	}
	writeBinaryUvarint(buf, uint64(value.Len()))
	for i := 0; i < value.Len(); i++ {
		if err := writeBinaryValue(buf, value.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

func writeBinaryMap(buf *bytes.Buffer, value reflect.Value) error {
	if err := buf.WriteByte(binaryKindObject); err != nil {
		return err
	}

	keys := value.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return binaryMapKey(keys[i]) < binaryMapKey(keys[j])
	})

	writeBinaryUvarint(buf, uint64(len(keys)))
	for _, key := range keys {
		writeBinaryUvarint(buf, 0)
		writeBinaryString(buf, binaryMapKey(key))
		if err := writeBinaryValue(buf, value.MapIndex(key)); err != nil {
			return err
		}
	}
	return nil
}

func writeBinaryStruct(buf *bytes.Buffer, value reflect.Value) error {
	fields := binaryStructFields(value.Type())
	encoded := make([]binaryStructField, 0, len(fields))
	for _, field := range fields {
		fieldValue := value.FieldByIndex(field.index)
		if field.omitEmpty && isBinaryEmptyValue(fieldValue) {
			continue
		}
		encoded = append(encoded, field)
	}

	if err := buf.WriteByte(binaryKindObject); err != nil {
		return err
	}
	writeBinaryUvarint(buf, uint64(len(encoded)))
	for _, field := range encoded {
		fieldValue := value.FieldByIndex(field.index)
		writeBinaryUvarint(buf, field.number)
		writeBinaryString(buf, field.name)
		if field.stringEncoded {
			text, err := stringTaggedValue(fieldValue)
			if err != nil {
				return err
			}
			if err := buf.WriteByte(binaryKindString); err != nil {
				return err
			}
			writeBinaryString(buf, text)
			continue
		}
		if err := writeBinaryValue(buf, fieldValue); err != nil {
			return err
		}
	}
	return nil
}

func writeBinaryUvarint(buf *bytes.Buffer, value uint64) {
	var raw [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(raw[:], value)
	buf.Write(raw[:n])
}

func writeBinaryString(buf *bytes.Buffer, value string) {
	writeBinaryBytes(buf, []byte(value))
}

func writeBinaryBytes(buf *bytes.Buffer, value []byte) {
	writeBinaryUvarint(buf, uint64(len(value)))
	buf.Write(value)
}

func encodeZigZag(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}
