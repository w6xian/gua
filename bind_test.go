package gua

import (
	"reflect"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// 本文件针对 bind.go 的绑定与反射辅助逻辑做白盒单元测试。

// bindSumVariadic 用于验证变参函数的参数收集
func bindSumVariadic(base int, rest ...int) int {
	total := base
	for _, v := range rest {
		total += v
	}
	return total
}

func TestFuncName(t *testing.T) {
	c := &counter{}
	cases := []struct {
		name string
		fn   reflect.Value
		want string
	}{
		{"package level func", reflect.ValueOf(addAll), "addAll"},
		{"method value", reflect.ValueOf(c.GetValue), "GetValue"},
	}
	for _, c := range cases {
		got, err := funcName(c.fn)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	var nilFunc func()
	for _, c := range []struct {
		name string
		fn   reflect.Value
	}{
		{"invalid value", reflect.Value{}},
		{"not a func", reflect.ValueOf(123)},
		{"nil func", reflect.ValueOf(nilFunc)},
	} {
		if _, err := funcName(c.fn); err == nil {
			t.Fatalf("%s: expected an error", c.name)
		}
	}
}

func TestIsValidLuaName(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"a":     true,
		"A":     true,
		"_":     true,
		"_a1":   true,
		"a_1":   true,
		"1a":    false,
		"a-b":   false,
		"a.b":   false,
		"a b":   false,
		"中文":    false,
		"get2X": true,
	}
	for in, want := range cases {
		if got := isValidLuaName(in); got != want {
			t.Fatalf("isValidLuaName(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewService(t *testing.T) {
	svc, err := newService(&counter{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	if want := typeName(reflect.TypeOf(&counter{})); svc.N != want {
		t.Fatalf("svc.N = %q, want %q", svc.N, want)
	}
	if !strings.HasSuffix(svc.N, ".counter") {
		t.Fatalf("svc.N = %q, want it to end with .counter", svc.N)
	}
	for _, name := range []string{"Increment", "GetValue", "Add", "Sum"} {
		if _, ok := svc.M[name]; !ok {
			t.Fatalf("method %s is missing", name)
		}
	}
	if len(svc.M) != 4 {
		t.Fatalf("len(svc.M) = %d, want 4", len(svc.M))
	}

	if _, err := newService(nil); err == nil {
		t.Fatal("nil value should be rejected")
	}
	if _, err := newService(struct{ A int }{}); err == nil {
		t.Fatal("anonymous type should be rejected")
	}
}

func TestTypeName(t *testing.T) {
	if got := typeName(nil); got != "" {
		t.Fatalf("typeName(nil) = %q, want empty", got)
	}
	if got := typeName(reflect.TypeOf(struct{}{})); got != "" {
		t.Fatalf("typeName(anonymous) = %q, want empty", got)
	}
	want := "github.com/w6xian/gua.counter"
	if got := typeName(reflect.TypeOf(&counter{})); got != want {
		t.Fatalf("typeName(*counter) = %q, want %q", got, want)
	}
	if got := typeName(reflect.TypeOf(counter{})); got != want {
		t.Fatalf("typeName(counter) = %q, want %q", got, want)
	}
}

func TestSuitableMethods(t *testing.T) {
	if got := suitableMethods(nil); len(got) != 0 {
		t.Fatalf("suitableMethods(nil) = %v, want empty", got)
	}
	m := suitableMethods(reflect.TypeOf(&counter{}))
	for _, name := range []string{"Increment", "GetValue", "Add", "Sum"} {
		if _, ok := m[name]; !ok {
			t.Fatalf("method %s is missing", name)
		}
	}
	if _, ok := m["unexported"]; ok {
		t.Fatal("unexported method should be filtered out")
	}
	if len(m) != 4 {
		t.Fatalf("len(m) = %d, want 4", len(m))
	}
}

func TestModuleNameFromTypeEdgeCases(t *testing.T) {
	cases := map[string]string{
		"gua.Svc[int]":                  "gua/svc",
		"pkg.Svc[int]":                  "pkg/svc",
		"a/b/c.Svc":                     "a/b/c/svc",
		"single":                        "single",
		"github.com/w6xian/gua.Counter": "github.com/w6xian/gua/counter",
	}
	for in, want := range cases {
		if got := moduleNameFromType(in); got != want {
			t.Fatalf("moduleNameFromType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBindFuncRejectsNonFunction(t *testing.T) {
	if _, err := bindFunc("x", reflect.Value{}); err == nil {
		t.Fatal("invalid value should be rejected")
	}
	if _, err := bindFunc("x", reflect.ValueOf(123)); err == nil {
		t.Fatal("non function should be rejected")
	}
}

func TestBindMethodRejectsBadReceiver(t *testing.T) {
	if _, err := bindMethod(reflect.Value{}, reflect.Method{}); err == nil {
		t.Fatal("invalid receiver should be rejected")
	}

	// 接收器类型与方法所属类型不一致
	other := reflect.TypeOf(&service{}).Method(0)
	if _, err := bindMethod(reflect.ValueOf(&counter{}), other); err == nil {
		t.Fatal("receiver type mismatch should be rejected")
	}

	// 匹配的接收器应当绑定成功
	same := reflect.TypeOf(&counter{}).Method(0)
	if _, err := bindMethod(reflect.ValueOf(&counter{}), same); err != nil {
		t.Fatalf("bindMethod: %v", err)
	}
}

func TestGuardConvertsPanicToLuaError(t *testing.T) {
	L := newTestLState(t)
	L.SetGlobal("f", L.NewFunction(guard("f", func(*lua.LState) int { panic("kaboom") })))

	err := L.DoString("f()")
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("panic should become a lua error, got %v", err)
	}
	// panic 之后状态依然可用
	if err := L.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestGuardReraisesLuaApiError(t *testing.T) {
	L := newTestLState(t)
	L.SetGlobal("g", L.NewFunction(guard("g", func(L *lua.LState) int {
		L.RaiseError("raised from go")
		return 0
	})))

	err := L.DoString("g()")
	if err == nil || !strings.Contains(err.Error(), "raised from go") {
		t.Fatalf("lua api error should surface as an error, got %v", err)
	}
	if err := L.DoString("y = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestBindFuncVariadic(t *testing.T) {
	L := newTestLState(t)
	fn, err := bindFunc("bindSumVariadic", reflect.ValueOf(bindSumVariadic))
	if err != nil {
		t.Fatalf("bindFunc: %v", err)
	}
	L.SetGlobal("bindSumVariadic", L.NewFunction(fn))

	if err := L.DoString("r = bindSumVariadic(1, 2, 3)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := L.GetGlobal("r"); got != lua.LNumber(6) {
		t.Fatalf("bindSumVariadic(1,2,3) = %v, want 6", got)
	}

	// 显式的 nil 会终止变参收集
	if err := L.DoString("r2 = bindSumVariadic(5, nil)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := L.GetGlobal("r2"); got != lua.LNumber(5) {
		t.Fatalf("bindSumVariadic(5, nil) = %v, want 5", got)
	}
}

func TestBindFuncNilTerminatesVariadic(t *testing.T) {
	L := newTestLState(t)
	fn, err := bindFunc("joinAll", reflect.ValueOf(joinAll))
	if err != nil {
		t.Fatalf("bindFunc: %v", err)
	}
	L.SetGlobal("joinAll", L.NewFunction(fn))

	if err := L.DoString("r = joinAll('p', nil, 'ignored')"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := L.GetGlobal("r"); got != lua.LString("p") {
		t.Fatalf("joinAll('p', nil, 'ignored') = %v, want p", got)
	}
}

func TestPushResultsMultipleValues(t *testing.T) {
	L := newTestLState(t)
	c := &counter{}
	m, ok := reflect.TypeOf(c).MethodByName("Sum")
	if !ok {
		t.Fatal("counter has no method Sum")
	}
	fn, err := bindMethod(reflect.ValueOf(c), m)
	if err != nil {
		t.Fatalf("bindMethod: %v", err)
	}
	L.SetGlobal("Sum", L.NewFunction(fn))

	if err := L.DoString("a, b = Sum(10, 3)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := L.GetGlobal("a"); got != lua.LNumber(13) {
		t.Fatalf("a = %v, want 13", got)
	}
	if got := L.GetGlobal("b"); got != lua.LNumber(7) {
		t.Fatalf("b = %v, want 7", got)
	}
}

func TestModuleLoaderReturnsTheSameTable(t *testing.T) {
	L := newTestLState(t)
	mod := L.NewTable()
	mod.RawSetString("X", lua.LNumber(1))
	L.PreloadModule("ml", moduleLoader(mod))

	if err := L.DoString("local m = require('ml'); r = m.X; m.Y = 2"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := L.GetGlobal("r"); got != lua.LNumber(1) {
		t.Fatalf("r = %v, want 1", got)
	}
	// 模块表是共享的，Lua 侧的修改对 Go 可见
	if got := mod.RawGetString("Y"); got != lua.LNumber(2) {
		t.Fatalf("mod.Y = %v, want 2", got)
	}
}
