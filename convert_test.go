package gua

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// 本文件针对 convert.go 的 Go <-> Lua 值转换做白盒单元测试。

// newTestLState 返回一个仅供单元测试使用的裸 Lua 状态机
func newTestLState(t *testing.T) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	return L
}

// anyType 返回 interface{} 的 reflect.Type
func anyType() reflect.Type { return reflect.TypeOf((*any)(nil)).Elem() }

// convErr 用于验证实现了 error 接口的值会被转换成 Lua 错误
type convErr struct{ msg string }

func (e *convErr) Error() string { return e.msg }

// convStruct 同时含导出与未导出字段，用于验证未导出字段被跳过
type convStruct struct {
	Name string
	Age  int
	priv string
}

// convBadStruct 含无法转换的字段，用于验证转换错误的上下文信息
type convBadStruct struct {
	Ch chan int
}

/* ------------------------------ toLValue：Go -> Lua ------------------------------ */

func TestToLValueScalars(t *testing.T) {
	L := newTestLState(t)
	cases := []struct {
		name string
		in   any
		want lua.LValue
	}{
		{"bool true", true, lua.LTrue},
		{"bool false", false, lua.LFalse},
		{"int", 42, lua.LNumber(42)},
		{"int8", int8(-8), lua.LNumber(-8)},
		{"int16", int16(-16), lua.LNumber(-16)},
		{"int32", int32(-32), lua.LNumber(-32)},
		{"int64", int64(-64), lua.LNumber(-64)},
		{"uint", uint(1), lua.LNumber(1)},
		{"uint8", uint8(255), lua.LNumber(255)},
		{"uint16", uint16(65535), lua.LNumber(65535)},
		{"uint32", uint32(4294967295), lua.LNumber(4294967295)},
		{"uint64", uint64(1 << 60), lua.LNumber(1 << 60)},
		{"uintptr", uintptr(7), lua.LNumber(7)},
		{"float32", float32(1.5), lua.LNumber(1.5)},
		{"float64", 0.25, lua.LNumber(0.25)},
		{"string", "hi", lua.LString("hi")},
	}
	for _, c := range cases {
		got, err := toLValue(L, reflect.ValueOf(c.in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %v (%s), want %v", c.name, got, got.Type(), c.want)
		}
	}
}

func TestToLValueNilVariants(t *testing.T) {
	L := newTestLState(t)
	var nilSlice []int
	var nilMap map[string]int
	var nilPtr *Conf
	var nilErr error
	var nilTypedErr *convErr
	cases := []struct {
		name string
		in   any
	}{
		{"untyped nil", nil},
		{"nil slice", nilSlice},
		{"nil map", nilMap},
		{"nil pointer", nilPtr},
		{"nil error interface", nilErr},
		{"nil typed error", nilTypedErr},
	}
	for _, c := range cases {
		got, err := toLValue(L, reflect.ValueOf(c.in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if got != lua.LNil {
			t.Fatalf("%s: got %v (%s), want nil", c.name, got, got.Type())
		}
	}
}

func TestToLValueContainers(t *testing.T) {
	L := newTestLState(t)

	// slice
	got, err := toLValue(L, reflect.ValueOf([]int{1, 2, 3}))
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	tb, ok := got.(*lua.LTable)
	if !ok {
		t.Fatalf("slice -> %T, want *lua.LTable", got)
	}
	for i := 1; i <= 3; i++ {
		if tb.RawGetInt(i) != lua.LNumber(i) {
			t.Fatalf("slice[%d] = %v", i, tb.RawGetInt(i))
		}
	}
	if !isNilLValue(tb.RawGetInt(4)) {
		t.Fatalf("slice[4] = %v, want nil", tb.RawGetInt(4))
	}

	// array
	got, err = toLValue(L, reflect.ValueOf([2]string{"a", "b"}))
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	tb, ok = got.(*lua.LTable)
	if !ok {
		t.Fatalf("array -> %T, want *lua.LTable", got)
	}
	if tb.RawGetInt(2) != lua.LString("b") {
		t.Fatalf("array[2] = %v, want b", tb.RawGetInt(2))
	}

	// map
	got, err = toLValue(L, reflect.ValueOf(map[string]int{"a": 1}))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	tb, ok = got.(*lua.LTable)
	if !ok {
		t.Fatalf("map -> %T, want *lua.LTable", got)
	}
	if tb.RawGetString("a") != lua.LNumber(1) {
		t.Fatalf("map.a = %v, want 1", tb.RawGetString("a"))
	}

	// struct：只导出字段，未导出字段被跳过
	got, err = toLValue(L, reflect.ValueOf(convStruct{Name: "leo", Age: 18, priv: "secret"}))
	if err != nil {
		t.Fatalf("struct: %v", err)
	}
	tb, ok = got.(*lua.LTable)
	if !ok {
		t.Fatalf("struct -> %T, want *lua.LTable", got)
	}
	if tb.RawGetString("Name") != lua.LString("leo") || tb.RawGetString("Age") != lua.LNumber(18) {
		t.Fatalf("struct table = %v", tb)
	}
	if !isNilLValue(tb.RawGetString("priv")) {
		t.Fatal("unexported field should be skipped")
	}

	// pointer 解引用
	got, err = toLValue(L, reflect.ValueOf(&Conf{Name: "bob", Age: 3}))
	if err != nil {
		t.Fatalf("pointer: %v", err)
	}
	tb, ok = got.(*lua.LTable)
	if !ok {
		t.Fatalf("pointer -> %T, want *lua.LTable", got)
	}
	if tb.RawGetString("Name") != lua.LString("bob") {
		t.Fatalf("pointer.Name = %v", tb.RawGetString("Name"))
	}

	// interface 解引用
	var anyVal any = 5
	got, err = toLValue(L, reflect.ValueOf(anyVal))
	if err != nil {
		t.Fatalf("interface: %v", err)
	}
	if got != lua.LNumber(5) {
		t.Fatalf("interface -> %v, want 5", got)
	}

	// 嵌套容器
	got, err = toLValue(L, reflect.ValueOf(map[string][]int{"a": {1, 2}}))
	if err != nil {
		t.Fatalf("nested: %v", err)
	}
	tb, ok = got.(*lua.LTable)
	if !ok {
		t.Fatalf("nested -> %T, want *lua.LTable", got)
	}
	inner, ok := tb.RawGetString("a").(*lua.LTable)
	if !ok {
		t.Fatalf("nested.a -> %T, want *lua.LTable", tb.RawGetString("a"))
	}
	if inner.RawGetInt(2) != lua.LNumber(2) {
		t.Fatalf("nested.a[2] = %v", inner.RawGetInt(2))
	}
}

func TestToLValueUnsupportedTypes(t *testing.T) {
	L := newTestLState(t)
	cases := []struct {
		name string
		in   any
	}{
		{"chan", make(chan int)},
		{"func", func() {}},
		{"complex", complex(1, 2)},
	}
	for _, c := range cases {
		if _, err := toLValue(L, reflect.ValueOf(c.in)); err == nil {
			t.Fatalf("%s should not be convertible to a lua value", c.name)
		}
	}
}

func TestToLValueErrorContext(t *testing.T) {
	L := newTestLState(t)
	cases := []struct {
		name     string
		in       any
		contains string
	}{
		{"top level error", errors.New("top-boom"), "top-boom"},
		{"typed error", &convErr{msg: "typed-boom"}, "typed-boom"},
		{"slice element", []error{errors.New("slice-boom")}, "[0]: slice-boom"},
		{"map key", map[chan int]string{make(chan int): "x"}, "map key"},
		{"map value", map[string]chan int{"a": make(chan int)}, "map value"},
		{"struct field", convBadStruct{}, "field Ch"},
	}
	for _, c := range cases {
		_, err := toLValue(L, reflect.ValueOf(c.in))
		if err == nil {
			t.Fatalf("%s: expected an error", c.name)
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Fatalf("%s: error = %q, want it to contain %q", c.name, err, c.contains)
		}
	}
}

/* ------------------------------ fromLValue：Lua -> Go ------------------------------ */

func TestFromLValueScalars(t *testing.T) {
	L := newTestLState(t)
	cases := []struct {
		name string
		lv   lua.LValue
		typ  reflect.Type
		want any
	}{
		{"bool from true", lua.LTrue, reflect.TypeOf(true), true},
		{"bool from false", lua.LFalse, reflect.TypeOf(false), false},
		{"bool from nil", lua.LNil, reflect.TypeOf(true), false},
		{"int", lua.LNumber(42), reflect.TypeOf(0), 42},
		{"int from string", lua.LString("42"), reflect.TypeOf(0), 42},
		{"int from padded string", lua.LString(" 7 "), reflect.TypeOf(0), 7},
		{"int8", lua.LNumber(-8), reflect.TypeOf(int8(0)), int8(-8)},
		{"int64", lua.LNumber(1 << 40), reflect.TypeOf(int64(0)), int64(1 << 40)},
		{"uint8", lua.LNumber(200), reflect.TypeOf(uint8(0)), uint8(200)},
		{"uint64", lua.LNumber(1 << 50), reflect.TypeOf(uint64(0)), uint64(1 << 50)},
		{"float64", lua.LNumber(1.5), reflect.TypeOf(float64(0)), 1.5},
		{"float32 from string", lua.LString("2.5"), reflect.TypeOf(float32(0)), float32(2.5)},
		{"string", lua.LString("hi"), reflect.TypeOf(""), "hi"},
		{"string from number", lua.LNumber(42), reflect.TypeOf(""), "42"},
		{"string from nil", lua.LNil, reflect.TypeOf(""), ""},
		{"any from number", lua.LNumber(3), anyType(), float64(3)},
		{"any from string", lua.LString("s"), anyType(), "s"},
		{"any from bool", lua.LTrue, anyType(), true},
	}
	for _, c := range cases {
		got, err := fromLValue(L, c.lv, c.typ)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if !reflect.DeepEqual(got.Interface(), c.want) {
			t.Fatalf("%s: got %#v, want %#v", c.name, got.Interface(), c.want)
		}
	}

	// nil -> interface{} 保持 nil
	got, err := fromLValue(L, lua.LNil, anyType())
	if err != nil {
		t.Fatalf("any from nil: %v", err)
	}
	if !got.IsNil() {
		t.Fatalf("any from nil = %#v, want nil", got.Interface())
	}
}

func TestFromLValueErrors(t *testing.T) {
	L := newTestLState(t)
	cases := []struct {
		name     string
		lv       lua.LValue
		typ      reflect.Type
		contains string
	}{
		{"int from non numeric string", lua.LString("abc"), reflect.TypeOf(0), "cannot convert"},
		{"int from bool", lua.LTrue, reflect.TypeOf(0), "cannot convert"},
		{"int overflow", lua.LNumber(200), reflect.TypeOf(int8(0)), "overflows"},
		{"uint negative", lua.LNumber(-1), reflect.TypeOf(uint8(0)), "overflows"},
		{"uint overflow", lua.LNumber(300), reflect.TypeOf(uint8(0)), "overflows"},
		{"float from bool", lua.LTrue, reflect.TypeOf(0.0), "cannot convert"},
		{"chan param", lua.LNumber(1), reflect.TypeOf(make(chan int)), "unsupported parameter type"},
		{"interface with methods", lua.LNumber(1), reflect.TypeOf((*error)(nil)).Elem(), "unsupported parameter type"},
		{"slice from number", lua.LNumber(1), reflect.TypeOf([]int{}), "cannot convert"},
		{"array from number", lua.LNumber(1), reflect.TypeOf([2]int{}), "cannot convert"},
		{"map from number", lua.LNumber(1), reflect.TypeOf(map[string]int{}), "cannot convert"},
		{"struct from number", lua.LNumber(1), reflect.TypeOf(Conf{}), "cannot convert"},
	}
	for _, c := range cases {
		if _, err := fromLValue(L, c.lv, c.typ); err == nil {
			t.Fatalf("%s: expected an error", c.name)
		} else if !strings.Contains(err.Error(), c.contains) {
			t.Fatalf("%s: error = %q, want it to contain %q", c.name, err, c.contains)
		}
	}
}

func TestFromLValueContainers(t *testing.T) {
	L := newTestLState(t)

	// slice
	tb := L.NewTable()
	tb.RawSetInt(1, lua.LNumber(1))
	tb.RawSetInt(2, lua.LNumber(2))
	tb.RawSetInt(3, lua.LNumber(3))
	got, err := fromLValue(L, tb, reflect.TypeOf([]int{}))
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), []int{1, 2, 3}) {
		t.Fatalf("slice = %#v, want [1 2 3]", got.Interface())
	}

	// array：元素不足时其余保持零值
	tb2 := L.NewTable()
	tb2.RawSetInt(1, lua.LNumber(1))
	tb2.RawSetInt(2, lua.LNumber(2))
	got, err = fromLValue(L, tb2, reflect.TypeOf([3]int{}))
	if err != nil {
		t.Fatalf("array short: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), [3]int{1, 2, 0}) {
		t.Fatalf("array = %#v, want [1 2 0]", got.Interface())
	}

	// array：元素过多时截断
	tb3 := L.NewTable()
	for i := 1; i <= 4; i++ {
		tb3.RawSetInt(i, lua.LNumber(i))
	}
	got, err = fromLValue(L, tb3, reflect.TypeOf([3]int{}))
	if err != nil {
		t.Fatalf("array long: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), [3]int{1, 2, 3}) {
		t.Fatalf("array = %#v, want [1 2 3]", got.Interface())
	}

	// map
	mtb := L.NewTable()
	mtb.RawSetString("a", lua.LNumber(1))
	mtb.RawSetString("b", lua.LNumber(2))
	got, err = fromLValue(L, mtb, reflect.TypeOf(map[string]int{}))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), map[string]int{"a": 1, "b": 2}) {
		t.Fatalf("map = %#v", got.Interface())
	}

	// struct
	stb := L.NewTable()
	stb.RawSetString("Name", lua.LString("bob"))
	stb.RawSetString("Age", lua.LNumber(3))
	got, err = fromLValue(L, stb, reflect.TypeOf(Conf{}))
	if err != nil {
		t.Fatalf("struct: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), Conf{Name: "bob", Age: 3}) {
		t.Fatalf("struct = %#v", got.Interface())
	}

	// struct：缺省字段保持零值
	stb2 := L.NewTable()
	stb2.RawSetString("Name", lua.LString("only"))
	got, err = fromLValue(L, stb2, reflect.TypeOf(Conf{}))
	if err != nil {
		t.Fatalf("struct partial: %v", err)
	}
	if !reflect.DeepEqual(got.Interface(), Conf{Name: "only"}) {
		t.Fatalf("struct = %#v, want {only 0}", got.Interface())
	}

	// pointer
	got, err = fromLValue(L, stb, reflect.TypeOf(&Conf{}))
	if err != nil {
		t.Fatalf("pointer: %v", err)
	}
	cp, ok := got.Interface().(*Conf)
	if !ok {
		t.Fatalf("pointer -> %T, want *Conf", got.Interface())
	}
	if cp.Name != "bob" || cp.Age != 3 {
		t.Fatalf("*Conf = %+v, want {bob 3}", cp)
	}
}

func TestFromLValueNestedErrors(t *testing.T) {
	L := newTestLState(t)

	// slice 元素转换失败
	stb := L.NewTable()
	stb.RawSetInt(1, lua.LString("x"))
	_, err := fromLValue(L, stb, reflect.TypeOf([]int{}))
	if err == nil || !strings.Contains(err.Error(), "[1]") {
		t.Fatalf("slice element error = %v, want it to mention [1]", err)
	}

	// map value 转换失败
	mtb := L.NewTable()
	mtb.RawSetString("a", lua.LString("x"))
	_, err = fromLValue(L, mtb, reflect.TypeOf(map[string]int{}))
	if err == nil || !strings.Contains(err.Error(), "map value") {
		t.Fatalf("map value error = %v, want it to mention map value", err)
	}

	// map key 转换失败
	ktb := L.NewTable()
	ktb.RawSet(lua.LString("abc"), lua.LString("v"))
	_, err = fromLValue(L, ktb, reflect.TypeOf(map[int]string{}))
	if err == nil || !strings.Contains(err.Error(), "map key") {
		t.Fatalf("map key error = %v, want it to mention map key", err)
	}

	// struct 字段转换失败
	ftb := L.NewTable()
	ftb.RawSetString("Age", lua.LString("x"))
	_, err = fromLValue(L, ftb, reflect.TypeOf(Conf{}))
	if err == nil || !strings.Contains(err.Error(), "field Age") {
		t.Fatalf("struct field error = %v, want it to mention field Age", err)
	}
}

/* ------------------------------ 辅助函数 ------------------------------ */

func TestLValueToAny(t *testing.T) {
	L := newTestLState(t)

	cases := []struct {
		name string
		lv   lua.LValue
		want any
	}{
		{"nil", lua.LNil, nil},
		{"bool", lua.LTrue, true},
		{"number", lua.LNumber(3), float64(3)},
		{"string", lua.LString("s"), "s"},
	}
	for _, c := range cases {
		got, err := lvalueToAny(c.lv)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s = %#v, want %#v", c.name, got, c.want)
		}
	}

	// 数组表
	atb := L.NewTable()
	atb.RawSetInt(1, lua.LNumber(1))
	atb.RawSetInt(2, lua.LNumber(2))
	got, err := lvalueToAny(atb)
	if err != nil {
		t.Fatalf("array table: %v", err)
	}
	if !reflect.DeepEqual(got, []any{float64(1), float64(2)}) {
		t.Fatalf("array table = %#v, want [1 2]", got)
	}

	// 键值表
	mtb := L.NewTable()
	mtb.RawSetString("a", lua.LNumber(1))
	got, err = lvalueToAny(mtb)
	if err != nil {
		t.Fatalf("map table: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"a": float64(1)}) {
		t.Fatalf("map table = %#v", got)
	}

	// 嵌套表
	ntb := L.NewTable()
	ntb.RawSetString("a", atb)
	got, err = lvalueToAny(ntb)
	if err != nil {
		t.Fatalf("nested table: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"a": []any{float64(1), float64(2)}}) {
		t.Fatalf("nested table = %#v", got)
	}

	// 无法转换的类型
	if _, err := lvalueToAny(L.NewFunction(func(*lua.LState) int { return 0 })); err == nil {
		t.Fatal("function should not be convertible to a Go value")
	}

	// 数组表中含无法转换的元素
	btb := L.NewTable()
	btb.RawSetInt(1, L.NewFunction(func(*lua.LState) int { return 0 }))
	if _, err := lvalueToAny(btb); err == nil {
		t.Fatal("array table with function element should fail")
	}
}

func TestLvToFloat(t *testing.T) {
	L := newTestLState(t)
	if f, ok := lvToFloat(lua.LNumber(1.5)); !ok || f != 1.5 {
		t.Fatalf("lvToFloat(1.5) = %v, %v", f, ok)
	}
	if f, ok := lvToFloat(lua.LString(" 2.5 ")); !ok || f != 2.5 {
		t.Fatalf("lvToFloat(' 2.5 ') = %v, %v", f, ok)
	}
	for _, lv := range []lua.LValue{lua.LString("abc"), lua.LTrue, lua.LNil, L.NewTable()} {
		if _, ok := lvToFloat(lv); ok {
			t.Fatalf("lvToFloat(%v) should fail", lv)
		}
	}
}

func TestLvString(t *testing.T) {
	cases := []struct {
		name string
		lv   lua.LValue
		want string
	}{
		{"nil interface", nil, ""},
		{"lua nil", lua.LNil, ""},
		{"string", lua.LString("s"), "s"},
		{"number", lua.LNumber(42), "42"},
		{"bool", lua.LTrue, "true"},
	}
	for _, c := range cases {
		if got := lvString(c.lv); got != c.want {
			t.Fatalf("lvString(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsNilLValue(t *testing.T) {
	L := newTestLState(t)
	if !isNilLValue(nil) || !isNilLValue(lua.LNil) {
		t.Fatal("nil values should be detected")
	}
	for _, lv := range []lua.LValue{lua.LNumber(0), lua.LFalse, lua.LString(""), L.NewTable()} {
		if isNilLValue(lv) {
			t.Fatalf("%v should not be nil", lv)
		}
	}
}

func TestIsErrorValue(t *testing.T) {
	if isErrorValue(reflect.Value{}) {
		t.Fatal("invalid value should not be an error")
	}
	if isErrorValue(reflect.ValueOf(42)) {
		t.Fatal("int should not be an error")
	}
	if !isErrorValue(reflect.ValueOf(errors.New("x"))) {
		t.Fatal("errors.New should be an error")
	}
	if !isErrorValue(reflect.ValueOf(&convErr{})) {
		t.Fatal("*convErr should be an error")
	}
}

func TestTypeMismatch(t *testing.T) {
	err := typeMismatch(nil, reflect.TypeOf(0))
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("typeMismatch(nil) = %v", err)
	}
	err = typeMismatch(lua.LTrue, reflect.TypeOf(0))
	if err == nil || !strings.Contains(err.Error(), "boolean") {
		t.Fatalf("typeMismatch(bool) = %v", err)
	}
}
