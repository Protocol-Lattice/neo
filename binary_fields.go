package neo

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

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
