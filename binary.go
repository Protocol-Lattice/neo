package neo

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// BinaryContentType is Neo's protobuf-like binary media type.
//
// The wire format is a self-describing TLV stream: each value starts with a
// compact kind byte, integers use varints, lists carry an item count, and
// objects carry field numbers plus JSON-tagged field names. Field numbers make
// the format stable for Go-to-Go typed clients, while names keep it usable
// without generated .proto files.
const BinaryContentType = "application/x-neo-bin"

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

// BinaryCodec encodes Neo request and response envelopes using Neo's compact
// protobuf-like TLV format.
type BinaryCodec struct{}

// NeoBinaryCodec is the shared binary codec used by the server and by clients
// configured with WithBinaryCodec.
var NeoBinaryCodec BinaryCodec

func (BinaryCodec) Marshal(value any) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(binaryMagic)
	if err := writeBinaryValue(&buf, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (BinaryCodec) Unmarshal(raw []byte, value any) error {
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

type binaryObject []binaryField

type binaryField struct {
	number uint64
	name   string
	value  any
}

type binaryStructField struct {
	index         []int
	name          string
	number        uint64
	omitEmpty     bool
	stringEncoded bool
}

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

func assignBinaryValue(dst reflect.Value, src any) error {
	if !dst.CanSet() {
		return nil
	}

	if src == nil {
		dst.SetZero()
		return nil
	}

	if dst.Kind() == reflect.Pointer {
		target := reflect.New(dst.Type().Elem())
		if err := assignBinaryValue(target.Elem(), src); err != nil {
			return err
		}
		dst.Set(target)
		return nil
	}

	if ok, err := assignBinaryTextValue(dst, src); ok || err != nil {
		return err
	}
	if ok, err := assignBinaryBytesValue(dst, src); ok || err != nil {
		return err
	}

	switch dst.Kind() {
	case reflect.Interface:
		value := reflect.ValueOf(binaryToAny(src))
		if !value.IsValid() {
			dst.SetZero()
			return nil
		}
		if value.Type().AssignableTo(dst.Type()) {
			dst.Set(value)
			return nil
		}
		if value.Type().ConvertibleTo(dst.Type()) {
			dst.Set(value.Convert(dst.Type()))
			return nil
		}
		return fmt.Errorf("cannot assign %s to %s", value.Type(), dst.Type())

	case reflect.Struct:
		object, ok := src.(binaryObject)
		if !ok {
			return assignBinaryScalar(dst, src)
		}
		return assignBinaryStruct(dst, object)

	case reflect.Map:
		object, ok := src.(binaryObject)
		if !ok {
			return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
		}
		out := reflect.MakeMapWithSize(dst.Type(), len(object))
		for _, field := range object {
			key := reflect.New(dst.Type().Key()).Elem()
			if err := assignBinaryMapKey(key, field.name); err != nil {
				return err
			}
			value := reflect.New(dst.Type().Elem()).Elem()
			if err := assignBinaryValue(value, field.value); err != nil {
				return err
			}
			out.SetMapIndex(key, value)
		}
		dst.Set(out)
		return nil

	case reflect.Slice:
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			if raw, ok := src.([]byte); ok {
				dst.SetBytes(append([]byte(nil), raw...))
				return nil
			}
		}
		values, ok := src.([]any)
		if !ok {
			return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
		}
		out := reflect.MakeSlice(dst.Type(), len(values), len(values))
		for i, value := range values {
			if err := assignBinaryValue(out.Index(i), value); err != nil {
				return err
			}
		}
		dst.Set(out)
		return nil

	case reflect.Array:
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			raw, ok := src.([]byte)
			if !ok {
				return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
			}
			reflect.Copy(dst, reflect.ValueOf(raw))
			return nil
		}
		values, ok := src.([]any)
		if !ok {
			return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
		}
		for i := 0; i < dst.Len() && i < len(values); i++ {
			if err := assignBinaryValue(dst.Index(i), values[i]); err != nil {
				return err
			}
		}
		return nil

	default:
		return assignBinaryScalar(dst, src)
	}
}

func assignBinaryStruct(dst reflect.Value, object binaryObject) error {
	fields := binaryStructFields(dst.Type())
	byName := make(map[string]binaryStructField, len(fields))
	byNumber := make(map[uint64]binaryStructField, len(fields))
	for _, field := range fields {
		byName[field.name] = field
		byNumber[field.number] = field
	}

	for _, item := range object {
		field, ok := byName[item.name]
		if !ok && item.number != 0 {
			field, ok = byNumber[item.number]
		}
		if !ok {
			continue
		}
		if err := assignBinaryValue(dst.FieldByIndex(field.index), item.value); err != nil {
			return fmt.Errorf("decode field %q: %w", field.name, err)
		}
	}
	return nil
}

func assignBinaryScalar(dst reflect.Value, src any) error {
	switch dst.Kind() {
	case reflect.Bool:
		value, ok := src.(bool)
		if !ok {
			if text, ok := src.(string); ok {
				parsed, err := strconv.ParseBool(text)
				if err != nil {
					return err
				}
				dst.SetBool(parsed)
				return nil
			}
			return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
		}
		dst.SetBool(value)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := binaryInt64(src)
		if err != nil {
			return err
		}
		if dst.OverflowInt(value) {
			return fmt.Errorf("value %d overflows %s", value, dst.Type())
		}
		dst.SetInt(value)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := binaryUint64(src)
		if err != nil {
			return err
		}
		if dst.OverflowUint(value) {
			return fmt.Errorf("value %d overflows %s", value, dst.Type())
		}
		dst.SetUint(value)
		return nil

	case reflect.Float32, reflect.Float64:
		value, err := binaryFloat64(src)
		if err != nil {
			return err
		}
		if dst.OverflowFloat(value) {
			return fmt.Errorf("value %f overflows %s", value, dst.Type())
		}
		dst.SetFloat(value)
		return nil

	case reflect.String:
		switch value := src.(type) {
		case string:
			dst.SetString(value)
		case []byte:
			dst.SetString(string(value))
		default:
			dst.SetString(fmt.Sprint(value))
		}
		return nil
	}

	value := reflect.ValueOf(src)
	if value.IsValid() && value.Type().AssignableTo(dst.Type()) {
		dst.Set(value)
		return nil
	}
	if value.IsValid() && value.Type().ConvertibleTo(dst.Type()) {
		dst.Set(value.Convert(dst.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
}

func assignBinaryTextValue(dst reflect.Value, src any) (bool, error) {
	var target encoding.TextUnmarshaler
	if dst.CanAddr() {
		if unmarshaler, ok := dst.Addr().Interface().(encoding.TextUnmarshaler); ok {
			target = unmarshaler
		}
	}
	if target == nil && dst.CanInterface() {
		if unmarshaler, ok := dst.Interface().(encoding.TextUnmarshaler); ok {
			target = unmarshaler
		}
	}
	if target == nil {
		return false, nil
	}

	switch value := src.(type) {
	case string:
		return true, target.UnmarshalText([]byte(value))
	case []byte:
		return true, target.UnmarshalText(value)
	default:
		return true, fmt.Errorf("cannot text-unmarshal %T into %s", src, dst.Type())
	}
}

func assignBinaryBytesValue(dst reflect.Value, src any) (bool, error) {
	raw, ok := src.([]byte)
	if !ok {
		return false, nil
	}

	var target encoding.BinaryUnmarshaler
	if dst.CanAddr() {
		if unmarshaler, ok := dst.Addr().Interface().(encoding.BinaryUnmarshaler); ok {
			target = unmarshaler
		}
	}
	if target == nil && dst.CanInterface() {
		if unmarshaler, ok := dst.Interface().(encoding.BinaryUnmarshaler); ok {
			target = unmarshaler
		}
	}
	if target == nil {
		return false, nil
	}
	return true, target.UnmarshalBinary(raw)
}

func assignBinaryMapKey(dst reflect.Value, key string) error {
	return assignBinaryScalar(dst, key)
}

func binaryInt64(src any) (int64, error) {
	switch value := src.(type) {
	case int64:
		return value, nil
	case uint64:
		if value > math.MaxInt64 {
			return 0, fmt.Errorf("value %d overflows int64", value)
		}
		return int64(value), nil
	case float64:
		if math.Trunc(value) != value {
			return 0, fmt.Errorf("value %f is not an integer", value)
		}
		return int64(value), nil
	case string:
		return strconv.ParseInt(value, 10, 64)
	default:
		return 0, fmt.Errorf("cannot assign %T to integer", src)
	}
}

func binaryUint64(src any) (uint64, error) {
	switch value := src.(type) {
	case int64:
		if value < 0 {
			return 0, fmt.Errorf("value %d is negative", value)
		}
		return uint64(value), nil
	case uint64:
		return value, nil
	case float64:
		if value < 0 || math.Trunc(value) != value {
			return 0, fmt.Errorf("value %f is not an unsigned integer", value)
		}
		return uint64(value), nil
	case string:
		return strconv.ParseUint(value, 10, 64)
	default:
		return 0, fmt.Errorf("cannot assign %T to unsigned integer", src)
	}
}

func binaryFloat64(src any) (float64, error) {
	switch value := src.(type) {
	case int64:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	case float64:
		return value, nil
	case string:
		return strconv.ParseFloat(value, 64)
	default:
		return 0, fmt.Errorf("cannot assign %T to float", src)
	}
}

func binaryStructFields(t reflect.Type) []binaryStructField {
	fields := make([]binaryStructField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}

		name, opts := parseJSONTag(field.Tag.Get("json"))
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}

		fields = append(fields, binaryStructField{
			index:         field.Index,
			name:          name,
			number:        uint64(i + 1),
			omitEmpty:     opts["omitempty"],
			stringEncoded: opts["string"],
		})
	}
	return fields
}

func parseJSONTag(tag string) (string, map[string]bool) {
	name, rest, _ := strings.Cut(tag, ",")
	options := make(map[string]bool)
	for rest != "" {
		var option string
		option, rest, _ = strings.Cut(rest, ",")
		if option != "" {
			options[option] = true
		}
	}
	return name, options
}

func binaryMapKey(value reflect.Value) string {
	if value.Kind() == reflect.String {
		return value.String()
	}
	if value.CanInterface() {
		return fmt.Sprint(value.Interface())
	}
	return fmt.Sprint(value)
}

func stringTaggedValue(value reflect.Value) (string, error) {
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", nil
		}
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Float32:
		return strconv.FormatFloat(value.Float(), 'g', -1, 32), nil
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64), nil
	case reflect.String:
		return value.String(), nil
	default:
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return "", err
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return text, nil
	}
}

func isBinaryEmptyValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return value.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	default:
		return false
	}
}

func binaryToAny(src any) any {
	switch value := src.(type) {
	case binaryObject:
		out := make(map[string]any, len(value))
		for _, field := range value {
			out[field.name] = binaryToAny(field.value)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = binaryToAny(item)
		}
		return out
	case []byte:
		return append([]byte(nil), value...)
	default:
		return value
	}
}
