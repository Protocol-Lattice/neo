package neo

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

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
