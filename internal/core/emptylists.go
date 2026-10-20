package core

import "reflect"

// emptyLists returns v with every nil slice — inside maps, structs, pointers
// and other slices — replaced by an empty one, so an empty answer marshals as
// `[]`, never `null` (RS-09: memory search answered `"hits": null` and a client
// doing len(hits) crashed). Nil scalars (pointers to strings, nil interfaces)
// stay null: those mean "unknown", which is a different answer from "none".
func emptyLists(v any) any {
	if v == nil {
		return nil
	}
	out := fill(reflect.ValueOf(v))
	if !out.IsValid() {
		return v
	}
	return out.Interface()
}

func fill(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		return fill(v.Elem())
	case reflect.Pointer:
		if v.IsNil() || v.Elem().Kind() != reflect.Struct {
			return v
		}
		cp := reflect.New(v.Elem().Type())
		cp.Elem().Set(fill(v.Elem()))
		return cp
	case reflect.Slice:
		if v.IsNil() {
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return v // []byte marshals as a string
			}
			return reflect.MakeSlice(v.Type(), 0, 0)
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			setFilled(cp.Index(i), v.Index(i))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			val := fill(iter.Value())
			if !val.IsValid() {
				val = iter.Value()
			}
			cp.SetMapIndex(iter.Key(), convertTo(val, v.Type().Elem()))
		}
		return cp
	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		cp.Set(v)
		for i := range v.NumField() {
			if f := cp.Field(i); f.CanSet() {
				setFilled(f, v.Field(i))
			}
		}
		return cp
	default:
		return v
	}
}

// setFilled stores fill(src) into dst when the types allow it.
func setFilled(dst, src reflect.Value) {
	val := fill(src)
	if !val.IsValid() {
		return
	}
	if c := convertTo(val, dst.Type()); c.IsValid() && c.Type().AssignableTo(dst.Type()) {
		dst.Set(c)
	}
}

func convertTo(val reflect.Value, t reflect.Type) reflect.Value {
	if val.Type().AssignableTo(t) {
		return val
	}
	if t.Kind() == reflect.Interface && val.Type().Implements(t) {
		out := reflect.New(t).Elem()
		out.Set(val)
		return out
	}
	return val
}
