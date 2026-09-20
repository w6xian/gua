package gua

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// errorType 用于识别实现了 error 接口的返回值
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// toLValue 将 Go 值转换为 Lua 值。
// 支持 bool、整数、无符号整数、浮点数、string、slice/array、map、struct（仅导出字段）
// 以及 interface/pointer 的解引用；其余类型返回错误而不是 panic。
func toLValue(L *lua.LState, v reflect.Value) (lua.LValue, error) {
	if !v.IsValid() {
		return lua.LNil, nil
	}
	if isErrorValue(v) {
		if v.IsNil() {
			return lua.LNil, nil
		}
		return nil, fmt.Errorf("%v", v.Interface())
	}

	switch v.Kind() {
	case reflect.Bool:
		return lua.LBool(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return lua.LNumber(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return lua.LNumber(v.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return lua.LNumber(v.Float()), nil
	case reflect.String:
		return lua.LString(v.String()), nil
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return lua.LNil, nil
		}
		return toLValue(L, v.Elem())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return lua.LNil, nil
		}
		tb := L.NewTable()
		for i := 0; i < v.Len(); i++ {
			lv, err := toLValue(L, v.Index(i))
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			tb.RawSetInt(i+1, lv)
		}
		return tb, nil
	case reflect.Map:
		if v.IsNil() {
			return lua.LNil, nil
		}
		tb := L.NewTable()
		for _, k := range v.MapKeys() {
			klv, err := toLValue(L, k)
			if err != nil {
				return nil, fmt.Errorf("map key: %w", err)
			}
			vlv, err := toLValue(L, v.MapIndex(k))
			if err != nil {
				return nil, fmt.Errorf("map value: %w", err)
			}
			tb.RawSet(klv, vlv)
		}
		return tb, nil
	case reflect.Struct:
		t := v.Type()
		tb := L.NewTable()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue // 跳过未导出字段
			}
			fv, err := toLValue(L, v.Field(i))
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name, err)
			}
			tb.RawSetString(f.Name, fv)
		}
		return tb, nil
	}
	return nil, fmt.Errorf("unsupported Go type %s", v.Type())
}

// fromLValue 将 Lua 值转换为 t 类型的 Go 值。
// 转换失败时返回错误，由调用方转换为 Lua 错误，绝不会 panic。
func fromLValue(L *lua.LState, lv lua.LValue, t reflect.Type) (reflect.Value, error) {
	out := reflect.New(t).Elem()
	if isNilLValue(lv) {
		return out, nil // 保持零值
	}

	switch t.Kind() {
	case reflect.Bool:
		out.SetBool(lua.LVAsBool(lv))
		return out, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f, ok := lvToFloat(lv)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		i := int64(f)
		if out.OverflowInt(i) {
			return out, fmt.Errorf("value %v overflows %s", f, t)
		}
		out.SetInt(i)
		return out, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		f, ok := lvToFloat(lv)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		if f < 0 {
			return out, fmt.Errorf("value %v overflows %s", f, t)
		}
		u := uint64(f)
		if out.OverflowUint(u) {
			return out, fmt.Errorf("value %v overflows %s", f, t)
		}
		out.SetUint(u)
		return out, nil
	case reflect.Float32, reflect.Float64:
		f, ok := lvToFloat(lv)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		out.SetFloat(f)
		return out, nil
	case reflect.String:
		out.SetString(lvString(lv))
		return out, nil
	case reflect.Interface:
		if t.NumMethod() != 0 {
			return out, fmt.Errorf("unsupported parameter type %s", t)
		}
		v, err := lvalueToAny(lv)
		if err != nil {
			return out, err
		}
		rv := reflect.ValueOf(v)
		if rv.Type().AssignableTo(t) {
			return rv, nil
		}
		if rv.Type().ConvertibleTo(t) {
			return rv.Convert(t), nil
		}
		return out, typeMismatch(lv, t)
	case reflect.Pointer:
		ev, err := fromLValue(L, lv, t.Elem())
		if err != nil {
			return out, err
		}
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(ev)
		return ptr, nil
	case reflect.Slice:
		tb, ok := lv.(*lua.LTable)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		n := tb.Len()
		sl := reflect.MakeSlice(t, n, n)
		for i := 1; i <= n; i++ {
			ev, err := fromLValue(L, tb.RawGetInt(i), t.Elem())
			if err != nil {
				return out, fmt.Errorf("[%d]: %w", i, err)
			}
			sl.Index(i - 1).Set(ev)
		}
		return sl, nil
	case reflect.Array:
		tb, ok := lv.(*lua.LTable)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		n := tb.Len()
		if n > t.Len() {
			n = t.Len()
		}
		for i := 1; i <= n; i++ {
			ev, err := fromLValue(L, tb.RawGetInt(i), t.Elem())
			if err != nil {
				return out, fmt.Errorf("[%d]: %w", i, err)
			}
			out.Index(i - 1).Set(ev)
		}
		return out, nil
	case reflect.Map:
		tb, ok := lv.(*lua.LTable)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		m := reflect.MakeMapWithSize(t, tb.Len())
		var convErr error
		tb.ForEach(func(k, v lua.LValue) {
			if convErr != nil {
				return
			}
			kv, err := fromLValue(L, k, t.Key())
			if err != nil {
				convErr = fmt.Errorf("map key: %w", err)
				return
			}
			vv, err := fromLValue(L, v, t.Elem())
			if err != nil {
				convErr = fmt.Errorf("map value: %w", err)
				return
			}
			m.SetMapIndex(kv, vv)
		})
		if convErr != nil {
			return out, convErr
		}
		return m, nil
	case reflect.Struct:
		tb, ok := lv.(*lua.LTable)
		if !ok {
			return out, typeMismatch(lv, t)
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := tb.RawGetString(f.Name)
			if isNilLValue(fv) {
				continue
			}
			ev, err := fromLValue(L, fv, f.Type)
			if err != nil {
				return out, fmt.Errorf("field %s: %w", f.Name, err)
			}
			out.Field(i).Set(ev)
		}
		return out, nil
	}
	return out, fmt.Errorf("unsupported parameter type %s", t)
}

// lvalueToAny 将 Lua 值转换为 Go 通用类型（nil/bool/float64/string/[]any/map[string]any）
func lvalueToAny(lv lua.LValue) (any, error) {
	switch v := lv.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(v), nil
	case lua.LNumber:
		return float64(v), nil
	case lua.LString:
		return string(v), nil
	case *lua.LTable:
		if !isNilLValue(v.RawGetInt(1)) {
			out := make([]any, 0, v.Len())
			for i := 1; ; i++ {
				item := v.RawGetInt(i)
				if isNilLValue(item) {
					break
				}
				cv, err := lvalueToAny(item)
				if err != nil {
					return nil, err
				}
				out = append(out, cv)
			}
			return out, nil
		}
		out := make(map[string]any, v.Len())
		var convErr error
		v.ForEach(func(k, val lua.LValue) {
			if convErr != nil {
				return
			}
			cv, err := lvalueToAny(val)
			if err != nil {
				convErr = err
				return
			}
			out[lvString(k)] = cv
		})
		if convErr != nil {
			return nil, convErr
		}
		return out, nil
	}
	return nil, fmt.Errorf("cannot convert Lua value of type %s to Go", lv.Type())
}

// lvToFloat 将 Lua 数字或数字字符串转换为 float64
func lvToFloat(lv lua.LValue) (float64, bool) {
	switch v := lv.(type) {
	case lua.LNumber:
		return float64(v), true
	case lua.LString:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// lvString 取 Lua 值的字符串表示，nil 返回空串
func lvString(lv lua.LValue) string {
	if lv == nil {
		return ""
	}
	if s, ok := lv.(lua.LString); ok {
		return string(s)
	}
	if isNilLValue(lv) {
		return ""
	}
	return lv.String()
}

// isNilLValue 判断 Lua 值是否为 nil
func isNilLValue(lv lua.LValue) bool {
	return lv == nil || lv == lua.LNil
}

// isErrorValue 判断 Go 值是否为 error 类型
func isErrorValue(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	t := v.Type()
	return t != nil && t.Implements(errorType)
}

// typeMismatch 生成类型不匹配的错误信息
func typeMismatch(lv lua.LValue, t reflect.Type) error {
	if lv == nil {
		return fmt.Errorf("cannot convert nil to %s", t)
	}
	return fmt.Errorf("cannot convert Lua %s to %s", lv.Type(), t)
}
