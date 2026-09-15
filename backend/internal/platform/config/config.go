package config

import (
	"encoding"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

type Lookup func(key string) (string, bool)

func Load[T any]() (T, error) { return LoadFrom[T](os.LookupEnv) }

func LoadFrom[T any](lookup Lookup) (T, error) {
	var cfg T
	v := reflect.ValueOf(&cfg).Elem()
	if v.Kind() != reflect.Struct {
		return cfg, fmt.Errorf("config: %T is not a struct", cfg)
	}

	var problems []string
	bind(v, lookup, &problems)

	if len(problems) > 0 {
		return cfg, errors.Internal("configuration is invalid").With(
			"problems", problems,
		).Wrapping(fmt.Errorf("config: %s", strings.Join(problems, "; ")))
	}
	return cfg, nil
}

func bind(v reflect.Value, lookup Lookup, problems *[]string) {
	t := v.Type()
	for i := range t.NumField() {
		field, value := t.Field(i), v.Field(i)
		if !field.IsExported() {
			continue
		}

		name, ok := field.Tag.Lookup("env")
		if !ok {
			// An untagged nested struct is a grouping, not a value.
			if field.Type.Kind() == reflect.Struct && !implementsUnmarshaler(field.Type) {
				bind(value, lookup, problems)
			}
			continue
		}

		raw, present := lookup(name)
		if !present || raw == "" {
			if def, hasDefault := field.Tag.Lookup("default"); hasDefault {
				raw = def
			} else {
				if field.Tag.Get("required") == "true" {
					*problems = append(*problems, name+" is required but not set")
				}
				continue
			}
		}

		if err := set(value, raw); err != nil {
			// Never echo raw: it may be the secret itself.
			*problems = append(*problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
}

func implementsUnmarshaler(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

func set(v reflect.Value, raw string) error {
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(raw))
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("expected a boolean")
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// time.Duration is an int64 kind, so it must be handled before the
		// plain integer path or "20s" fails to parse.
		if v.Type() == reflect.TypeFor[time.Duration]() {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("expected a duration such as 20s or 1m30s")
			}
			v.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("expected an integer")
		}
		v.SetInt(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("expected a number")
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice element type %s", v.Type().Elem())
		}
		parts := strings.Split(raw, ",")
		out := reflect.MakeSlice(v.Type(), 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = reflect.Append(out, reflect.ValueOf(p).Convert(v.Type().Elem()))
			}
		}
		v.Set(out)
	default:
		return fmt.Errorf("unsupported config type %s", v.Type())
	}
	return nil
}
