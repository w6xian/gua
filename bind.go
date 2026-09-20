package gua

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// guard 把注册到 Lua 的 Go 函数包裹起来：
// Go 侧的 panic 会被转换成 Lua 错误（而不是让宿主进程崩溃），
// Lua 自身的错误（*lua.ApiError）原样向上抛，交给 gopher-lua 处理。
func guard(name string, fn lua.LGFunction) lua.LGFunction {
	return func(L *lua.LState) (n int) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(*lua.ApiError); ok {
					panic(r)
				}
				L.RaiseError("gua: %s: %v", name, r)
			}
		}()
		return fn(L)
	}
}

// bindFunc 把一个 Go 函数绑定为 Lua 函数
func bindFunc(name string, fn reflect.Value) (lua.LGFunction, error) {
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.IsNil() {
		return nil, fmt.Errorf("%s is not a function", name)
	}
	t := fn.Type()
	return guard(name, func(L *lua.LState) int {
		args, err := collectArgs(L, t, 0)
		if err != nil {
			L.RaiseError("gua: %s: %v", name, err)
		}
		return pushResults(L, name, fn.Call(args))
	}), nil
}

// bindMethod 把一个 Go 方法绑定为 Lua 函数，recv 为方法接收器
func bindMethod(recv reflect.Value, m reflect.Method) (lua.LGFunction, error) {
	if !recv.IsValid() {
		return nil, fmt.Errorf("%s: invalid receiver", m.Name)
	}
	if !recv.Type().AssignableTo(m.Type.In(0)) {
		return nil, fmt.Errorf("%s: receiver type mismatch (%s vs %s)", m.Name, recv.Type(), m.Type.In(0))
	}
	mt := m.Type
	return guard(m.Name, func(L *lua.LState) int {
		args, err := collectArgs(L, mt, 1)
		if err != nil {
			L.RaiseError("gua: %s: %v", m.Name, err)
		}
		args = append([]reflect.Value{recv}, args...)
		return pushResults(L, m.Name, m.Func.Call(args))
	}), nil
}

// collectArgs 按 Go 函数签名从 Lua 栈上取参数。
// from 表示第一个 Go 参数在函数类型中的下标：普通函数为 0，方法为 1（0 是接收器）。
func collectArgs(L *lua.LState, t reflect.Type, from int) ([]reflect.Value, error) {
	if t.NumIn() < from {
		return nil, nil
	}
	fixed := t.NumIn()
	if t.IsVariadic() {
		fixed = t.NumIn() - 1
	}
	args := make([]reflect.Value, 0, t.NumIn()-from)
	for i := from; i < fixed; i++ {
		pos := i - from + 1 // Lua 参数下标从 1 开始
		v, err := fromLValue(L, L.Get(pos), t.In(i))
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", pos, err)
		}
		args = append(args, v)
	}
	if t.IsVariadic() {
		et := t.In(t.NumIn() - 1).Elem()
		for pos := fixed - from + 1; ; pos++ {
			lv := L.Get(pos)
			if isNilLValue(lv) {
				break
			}
			v, err := fromLValue(L, lv, et)
			if err != nil {
				return nil, fmt.Errorf("argument %d: %w", pos, err)
			}
			args = append(args, v)
		}
	}
	return args, nil
}

// pushResults 把 Go 返回值压入 Lua 栈，返回返回值个数。
// 若某个返回值是 error 且非 nil，则转换为 Lua 错误。
func pushResults(L *lua.LState, name string, rst []reflect.Value) int {
	for i, v := range rst {
		if !v.IsValid() {
			L.Push(lua.LNil)
			continue
		}
		if isErrorValue(v) {
			if v.IsNil() {
				L.Push(lua.LNil)
				continue
			}
			L.RaiseError("gua: %s: %v", name, v.Interface())
		}
		lv, err := toLValue(L, v)
		if err != nil {
			L.RaiseError("gua: %s: return value %d: %v", name, i+1, err)
		}
		L.Push(lv)
	}
	return len(rst)
}

// moduleLoader 生成 require 使用的模块加载函数
func moduleLoader(mod *lua.LTable) lua.LGFunction {
	return guard("require", func(L *lua.LState) int {
		L.Push(mod)
		return 1
	})
}

// funcName 从 Go 函数指针推导出 Lua 全局函数名
func funcName(fn reflect.Value) (string, error) {
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.IsNil() {
		return "", errors.New("not a function")
	}
	name := runtime.FuncForPC(fn.Pointer()).Name()
	if name == "" {
		return "", errors.New("cannot resolve function name")
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	// 方法值：main.(*T).Get-fm / 泛型实例化：Foo[int]
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	if !isValidLuaName(name) {
		return "", fmt.Errorf("invalid lua function name %q", name)
	}
	return name, nil
}

// isValidLuaName 判断字符串能否作为 Lua 全局标识符
func isValidLuaName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// newService 反射分析待注册的值，生成服务描述
func newService(rcvr any) (*ServiceFuncs, error) {
	if rcvr == nil {
		return nil, errors.New("cannot register nil value")
	}
	v := reflect.ValueOf(rcvr)
	if !v.IsValid() {
		return nil, errors.New("cannot register invalid value")
	}
	name := typeName(v.Type())
	if name == "" {
		return nil, fmt.Errorf("cannot resolve type name of %T", rcvr)
	}
	return &ServiceFuncs{N: name, V: v, M: suitableMethods(v.Type())}, nil
}

// typeName 生成 "包路径.类型名" 形式的名字
func typeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Name() == "" {
		return ""
	}
	return t.PkgPath() + "." + t.Name()
}

// suitableMethods 收集类型的所有导出方法
func suitableMethods(typ reflect.Type) map[string]reflect.Method {
	methods := make(map[string]reflect.Method)
	if typ == nil {
		return methods
	}
	for m := 0; m < typ.NumMethod(); m++ {
		m := typ.Method(m)
		if !m.IsExported() || m.PkgPath != "" {
			continue
		}
		methods[m.Name] = m
	}
	return methods
}

// moduleNameFromType 把 "包路径.类型名" 转换为 Lua 模块名（类型名小写）
func moduleNameFromType(name string) string {
	parts := strings.Split(name, "/")
	last := parts[len(parts)-1]
	names := strings.Split(last, ".")
	names[len(names)-1] = strings.ToLower(names[len(names)-1])
	if i := strings.Index(names[len(names)-1], "["); i >= 0 {
		names[len(names)-1] = names[len(names)-1][:i]
	}
	out := make([]string, 0, len(parts)+len(names))
	out = append(out, parts[:len(parts)-1]...)
	out = append(out, names...)
	return strings.Join(out, "/")
}
