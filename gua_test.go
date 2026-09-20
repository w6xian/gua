package gua

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

/* ------------------------------ 测试辅助 ------------------------------ */

func newLua(t *testing.T) *Luax {
	t.Helper()
	l := New(CallStackSize(256), RegistrySize(256))
	t.Cleanup(l.Close)
	return l
}

func globalNumber(t *testing.T, l *Luax, name string) float64 {
	t.Helper()
	lv := l.L.GetGlobal(name)
	n, ok := lv.(lua.LNumber)
	if !ok {
		t.Fatalf("global %s = %v (%s), want number", name, lv, lv.Type())
	}
	return float64(n)
}

func globalString(t *testing.T, l *Luax, name string) string {
	t.Helper()
	lv := l.L.GetGlobal(name)
	s, ok := lv.(lua.LString)
	if !ok {
		t.Fatalf("global %s = %v (%s), want string", name, lv, lv.Type())
	}
	return string(s)
}

/* ------------------------------ 测试类型 ------------------------------ */

func addAll(a, b int) int { return a + b }

func sayHello(name string) string { return "Hello, " + name }

func joinAll(prefix string, parts ...string) string { return prefix + strings.Join(parts, "|") }

type counter struct {
	Value int
}

func (c *counter) Increment() int          { c.Value++; return c.Value }
func (c *counter) GetValue() int           { return c.Value }
func (c *counter) Add(a int) int           { c.Value += a; return c.Value }
func (c *counter) unexported() int         { return 0 }
func (c *counter) Sum(a, b int) (int, int) { return a + b, a - b }

type service struct{}

func (s *service) Conf() Conf          { return Conf{Name: "leo", Age: 18} }
func (s *service) List() []int         { return []int{1, 2, 3} }
func (s *service) Map() map[string]int { return map[string]int{"a": 1} }
func (s *service) Flag() bool          { return true }
func (s *service) Rate() float64       { return 0.25 }
func (s *service) MaybeErr(fail bool) (int, error) {
	if fail {
		return 0, errors.New("boom")
	}
	return 7, nil
}
func (s *service) Unsupported() chan int   { return nil }
func (s *service) NeedChan(c chan int) int { return 0 }
func (s *service) Echo(in Conf) string     { return in.Name }

type Conf struct {
	Name string
	Age  int
}

func boom() { panic("boom") }

/* ------------------------------ 基础能力 ------------------------------ */

func TestDoStringAndGlobals(t *testing.T) {
	l := newLua(t)

	if err := l.DoString("x = 10 + 20"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "x"); got != 30 {
		t.Fatalf("x = %v, want 30", got)
	}

	if err := l.DoString("s = 'hello'"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalString(t, l, "s"); got != "hello" {
		t.Fatalf("s = %q, want %q", got, "hello")
	}

	if err := l.DoString("this is not lua"); err == nil {
		t.Fatal("syntax error should be reported")
	}
}

func TestSetFunction(t *testing.T) {
	l := newLua(t)
	l.SetFunction(addAll, sayHello, joinAll)

	if err := l.DoString("r = addAll(3, 4)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 7 {
		t.Fatalf("addAll = %v, want 7", got)
	}

	if err := l.DoString("r2 = sayHello('lua')"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalString(t, l, "r2"); got != "Hello, lua" {
		t.Fatalf("sayHello = %q", got)
	}

	// 变参函数
	if err := l.DoString("r3 = joinAll('p', 'a', 'b', 'c')"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalString(t, l, "r3"); got != "pa|b|c" {
		t.Fatalf("joinAll = %q, want %q", got, "pa|b|c")
	}
}

func TestSetFunctionIgnoresInvalidInput(t *testing.T) {
	l := newLua(t)
	// 非函数、nil 不应导致 panic
	l.SetFunction(nil, 123, "not a func")
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestSetGlobalState(t *testing.T) {
	l := newLua(t)
	c := &counter{Value: 0}
	l.SetGlobal(c)

	if err := l.DoString("r = Increment()"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 1 {
		t.Fatalf("Increment = %v, want 1", got)
	}
	if err := l.DoString("Add(10)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if c.Value != 11 {
		t.Fatalf("counter.Value = %d, want 11", c.Value)
	}

	// 多返回值
	if err := l.DoString("a, b = Sum(10, 3)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "a"); got != 13 {
		t.Fatalf("Sum a = %v, want 13", got)
	}
	if got := globalNumber(t, l, "b"); got != 7 {
		t.Fatalf("Sum b = %v, want 7", got)
	}

	// 未导出方法不应被注册
	if err := l.DoString("return unexported()"); err == nil {
		t.Fatal("unexported method should not be registered")
	}
}

func TestModuleRegistration(t *testing.T) {
	l := newLua(t)
	c := &counter{Value: 5}
	l.Module("cnt", c)

	if err := l.DoString("local cnt = require('cnt'); r = cnt.GetValue(); cnt.Add(2); r2 = cnt.GetValue()"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 5 {
		t.Fatalf("GetValue = %v, want 5", got)
	}
	if got := globalNumber(t, l, "r2"); got != 7 {
		t.Fatalf("GetValue after Add = %v, want 7", got)
	}

	// 空模块名不应 panic
	l.Module("", c)
	l.Modules(nil, "legacy string", c)
}

func TestModuleNameFromType(t *testing.T) {
	cases := map[string]string{
		"main.Test":                  "main/test",
		"github.com/w6xian/gua.Call": "github.com/w6xian/gua/call",
		"Test":                       "test",
	}
	for in, want := range cases {
		if got := moduleNameFromType(in); got != want {
			t.Fatalf("moduleNameFromType(%q) = %q, want %q", in, got, want)
		}
	}
}

/* ------------------------------ 类型转换 ------------------------------ */

func TestGoToLuaConversion(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(&service{})

	if err := l.DoString("c = Conf()"); err != nil {
		t.Fatalf("Conf: %v", err)
	}
	tb, ok := l.L.GetGlobal("c").(*lua.LTable)
	if !ok {
		t.Fatalf("Conf() = %v, want table", l.L.GetGlobal("c"))
	}
	if got := l.L.GetField(tb, "Name").String(); got != "leo" {
		t.Fatalf("Conf.Name = %q, want %q", got, "leo")
	}
	if got, _ := l.L.GetField(tb, "Age").(lua.LNumber); float64(got) != 18 {
		t.Fatalf("Conf.Age = %v, want 18", got)
	}

	if err := l.DoString("l = List()"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if l.L.GetGlobal("l").(*lua.LTable).RawGetInt(3).String() != "3" {
		t.Fatalf("List()[3] wrong: %v", l.L.GetGlobal("l"))
	}

	if err := l.DoString("m = Map()"); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if l.L.GetField(l.L.GetGlobal("m").(*lua.LTable), "a").String() != "1" {
		t.Fatalf("Map()['a'] wrong")
	}

	if err := l.DoString("f = Flag()"); err != nil {
		t.Fatalf("Flag: %v", err)
	}
	if l.L.GetGlobal("f") != lua.LTrue {
		t.Fatalf("Flag() = %v, want true", l.L.GetGlobal("f"))
	}

	if err := l.DoString("r = Rate()"); err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 0.25 {
		t.Fatalf("Rate() = %v, want 0.25", got)
	}
}

func TestLuaToGoConversion(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(&service{})

	// Lua 表 -> Go struct
	if err := l.DoString("r = Echo({Name = 'bob', Age = 3})"); err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if got := globalString(t, l, "r"); got != "bob" {
		t.Fatalf("Echo = %q, want %q", got, "bob")
	}
}

func TestErrorReturnBecomesLuaError(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(&service{})

	if err := l.DoString("r = MaybeErr(false)"); err != nil {
		t.Fatalf("MaybeErr(false): %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 7 {
		t.Fatalf("MaybeErr = %v, want 7", got)
	}

	err := l.DoString("MaybeErr(true)")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("MaybeErr(true) error = %v, want error containing boom", err)
	}
}

func TestUnsupportedTypesReturnError(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(&service{})

	if err := l.DoString("Unsupported()"); err == nil {
		t.Fatal("unsupported return type should raise a lua error")
	}
	if err := l.DoString("NeedChan(1)"); err == nil {
		t.Fatal("unsupported parameter type should raise a lua error")
	}
	// 状态仍然可用
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestGoPanicConvertedToLuaError(t *testing.T) {
	l := newLua(t)
	l.SetFunction(boom)

	err := l.DoString("boom()")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic should become lua error, got %v", err)
	}
	// panic 之后状态依然可用
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

/* ------------------------------ 模块加载与调用 ------------------------------ */

func TestLoadFileAndCall(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadFile(filepath.Join("testdata", "m.lua")); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	got, err := l.Call("m.Test", "100", "200", "300")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "600" {
		t.Fatalf("m.Test = %q, want 600", got)
	}

	r1, r2, err := l.Call2("m.Test", "1", "2", "3")
	if err != nil {
		t.Fatalf("Call2: %v", err)
	}
	if r1 != "6" || r2 != "ok" {
		t.Fatalf("Call2 = %q, %q", r1, r2)
	}

	rets, err := l.CallN("m.Test2", 1, "a", "b")
	if err != nil {
		t.Fatalf("CallN: %v", err)
	}
	if len(rets) != 1 || rets[0] != "a-b" {
		t.Fatalf("CallN = %v", rets)
	}
}

func TestLoadString(t *testing.T) {
	l := newLua(t)
	code := "return {Test = function(a, b) return a .. b end}"
	if _, err := l.LoadString("c", code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	got, err := l.Call("c.Test", "a", "b")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "ab" {
		t.Fatalf("c.Test = %q, want ab", got)
	}

	if _, err := l.LoadString("", code); err == nil {
		t.Fatal("empty module name should be rejected")
	}
}

func TestLoadFileErrors(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadFile(filepath.Join("testdata", "bad.lua")); err == nil {
		t.Fatal("bad lua file should fail")
	}
	if _, err := l.LoadFile(filepath.Join("testdata", "missing.lua")); err == nil {
		t.Fatal("missing lua file should fail")
	}
	if err := l.LoadDir(filepath.Join("testdata", "missing-dir")); err == nil {
		t.Fatal("missing dir should fail")
	}
}

func TestCallErrors(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadFile(filepath.Join("testdata", "m.lua")); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if _, err := l.LoadFile(filepath.Join("testdata", "notable.lua")); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	cases := []string{"m", "m.Test.X", ".Test", "m.", "nope.Test"}
	for _, mn := range cases {
		if _, err := l.Call(mn); err == nil {
			t.Fatalf("Call(%q) should fail", mn)
		}
	}
	// notable.lua 没有返回模块表
	if _, err := l.Call("notable.Test"); err == nil {
		t.Fatal("module without table return should fail")
	}
	// 不是函数
	if _, err := l.Call("m.Missing"); err == nil {
		t.Fatal("missing function should fail")
	}
	// nret 非法
	if _, err := l.CallN("m.Test", -1); err == nil {
		t.Fatal("negative nret should fail")
	}
}

/* ------------------------------ 生命周期与并发 ------------------------------ */

func TestClosedStateIsSafe(t *testing.T) {
	l := New()
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	l.Close()
	l.Close() // 幂等

	if !l.Closed() {
		t.Fatal("Closed() should be true")
	}
	if err := l.DoString("x = 1"); !errors.Is(err, ErrClosed) {
		t.Fatalf("DoString after close = %v, want ErrClosed", err)
	}
	if _, err := l.Call("m.Test"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Call after close = %v, want ErrClosed", err)
	}
	if _, err := l.LoadFile("testdata/m.lua"); !errors.Is(err, ErrClosed) {
		t.Fatalf("LoadFile after close = %v, want ErrClosed", err)
	}
	if err := l.WatchFile("testdata/m.lua"); !errors.Is(err, ErrClosed) {
		t.Fatalf("WatchFile after close = %v, want ErrClosed", err)
	}
	// 无返回值的注册方法关闭后也不应 panic
	l.SetGlobal(&counter{})
	l.SetFunction(addAll)
	l.Modules(&counter{})
	l.Module("x", &counter{})
}

func TestNewStateSingleton(t *testing.T) {
	a := NewState()
	b := NewState()
	if a != b {
		t.Fatal("NewState should return the same instance")
	}
	a.Close()
	c := NewState()
	if c == a || c.Closed() {
		t.Fatal("NewState should recreate the instance after Close")
	}
	c.Close()
}

func TestConcurrentUse(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadFile(filepath.Join("testdata", "m.lua")); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := l.DoString("n = (n or 0) + 1"); err != nil {
					t.Errorf("DoString: %v", err)
					return
				}
				if _, err := l.Call("m.Test", "1", "2", "3"); err != nil {
					t.Errorf("Call: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := globalNumber(t, l, "n"); got != 160 {
		t.Fatalf("n = %v, want 160", got)
	}
}

/* ------------------------------ 文件监听 ------------------------------ */

func TestWatchFileReload(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "w.lua")
	write := func(v string) {
		t.Helper()
		code := fmt.Sprintf("local m = {}\nfunction m.V() return %q end\nreturn m\n", v)
		if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
			t.Fatalf("write lua: %v", err)
		}
	}

	write("v1")
	l := newLua(t)
	if err := l.WatchFile(file); err != nil {
		t.Fatalf("WatchFile: %v", err)
	}

	got, err := l.Call("w.V")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "v1" {
		t.Fatalf("w.V = %q, want v1", got)
	}

	write("v2")

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := l.Call("w.V")
		if err != nil {
			t.Fatalf("Call after reload: %v", err)
		}
		if got == "v2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lua file was not reloaded, still %q", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatchDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.lua"), []byte("return {V=function() return 'd' end}\n"), 0o644); err != nil {
		t.Fatalf("write lua: %v", err)
	}
	l := newLua(t)
	if err := l.LoadAndWatchDir(dir); err != nil {
		t.Fatalf("LoadAndWatchDir: %v", err)
	}
	got, err := l.Call("d.V")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "d" {
		t.Fatalf("d.V = %q, want d", got)
	}
}

func TestWatchFileRejectsNonLua(t *testing.T) {
	l := newLua(t)
	if err := l.WatchFile("testdata/m.txt"); err == nil {
		t.Fatal("non .lua file should be rejected")
	}
}
