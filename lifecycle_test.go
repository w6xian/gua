package gua

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// 本文件补齐 gua.go 中尚未被覆盖的生命周期与执行路径：
// DoFile、LoadDir、日志模式、关闭后的行为、单例并发等。

func failNow() (int, error)       { return 0, errors.New("nope") }
func okNow() (string, error)      { return "fine", nil }
func errorOnly() error            { return errors.New("only-error") }
func takesAny(v any) string       { return fmt.Sprintf("%v", v) }
func takesPointer(c *Conf) string { return c.Name }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

/* ------------------------------ DoFile ------------------------------ */

func TestDoFileSuccess(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "s.lua")
	writeFile(t, file, "n = 7")

	l := newLua(t)
	if err := l.DoFile(file); err != nil {
		t.Fatalf("DoFile: %v", err)
	}
	if got := globalNumber(t, l, "n"); got != 7 {
		t.Fatalf("n = %v, want 7", got)
	}
}

func TestDoFileErrors(t *testing.T) {
	l := newLua(t)
	if err := l.DoFile(filepath.Join("testdata", "bad.lua")); err == nil {
		t.Fatal("broken lua file should fail")
	}
	if err := l.DoFile(filepath.Join(t.TempDir(), "missing.lua")); err == nil {
		t.Fatal("missing lua file should fail")
	}
	// 出错之后状态依然可用
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestDoFileRestoresStack(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "r.lua")
	writeFile(t, file, "return 1, 2, 3")

	l := New(RegistrySize(256), RegistryMaxSize(1<<16), RegistryGrowStep(4096))
	defer l.Close()

	const n = 500
	for i := 0; i < n; i++ {
		if err := l.DoFile(file); err != nil {
			t.Fatalf("iteration %d/%d: %v", i, n, err)
		}
	}
	if top := l.L.GetTop(); top != 0 {
		t.Fatalf("lua stack top = %d after %d DoFile calls, want 0", top, n)
	}
}

func TestDoStringRestoresStackWithMultipleReturns(t *testing.T) {
	l := newLua(t)
	const n = 500
	for i := 0; i < n; i++ {
		if err := l.DoString("return 1, 2, 3"); err != nil {
			t.Fatalf("iteration %d/%d: %v", i, n, err)
		}
	}
	if top := l.L.GetTop(); top != 0 {
		t.Fatalf("lua stack top = %d after %d DoString calls, want 0", top, n)
	}
}

/* ------------------------------ LoadDir / LoadString ------------------------------ */

func TestLoadDirLoadsTopLevelLuaFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.lua"), "return {V=function() return 'a' end}")
	writeFile(t, filepath.Join(dir, "b.lua"), "return {V=function() return 'b' end}")
	writeFile(t, filepath.Join(dir, "c.txt"), "not lua")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sub", "d.lua"), "return {V=function() return 'd' end}")

	l := newLua(t)
	if err := l.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for mod, want := range map[string]string{"a": "a", "b": "b"} {
		got, err := l.Call(mod + ".V")
		if err != nil {
			t.Fatalf("Call %s.V: %v", mod, err)
		}
		if got != want {
			t.Fatalf("%s.V = %q, want %q", mod, got, want)
		}
	}
	// 不递归子目录
	if _, err := l.Call("d.V"); err == nil {
		t.Fatal("sub directories should not be loaded")
	}
	// 非 .lua 文件被忽略
	if _, ok := l.Fn["c"]; ok {
		t.Fatal("non .lua files should be ignored")
	}
}

func TestLoadDirContinuesOnBadFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.lua"), "return {V=function() return 'g' end}")
	writeFile(t, filepath.Join(dir, "broken.lua"), "return (")

	l := newLua(t)
	if err := l.LoadDir(dir); err == nil {
		t.Fatal("LoadDir should report the broken file")
	}
	got, err := l.Call("good.V")
	if err != nil {
		t.Fatalf("Call good.V: %v", err)
	}
	if got != "g" {
		t.Fatalf("good.V = %q, want g", got)
	}
}

func TestLoadStringOverwritesModule(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadString("o", "return {V=function() return 'v1' end}"); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	got, err := l.Call("o.V")
	if err != nil || got != "v1" {
		t.Fatalf("o.V = %q, err = %v, want v1", got, err)
	}

	if _, err := l.LoadString("o", "return {V=function() return 'v2' end}"); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	got, err = l.Call("o.V")
	if err != nil || got != "v2" {
		t.Fatalf("o.V = %q, err = %v, want v2", got, err)
	}

	if _, err := l.LoadString("broken", "return ("); err == nil {
		t.Fatal("broken lua code should fail")
	}
}

func TestModuleNameFromFilename(t *testing.T) {
	cases := map[string]string{
		"m.lua":          "m",
		"testdata/m.lua": "m",
		"a/b/c.lua":      "c",
		"noext":          "noext",
		"a.b.lua":        "a.b",
	}
	for in, want := range cases {
		if got := moduleNameFromFilename(in); got != want {
			t.Fatalf("moduleNameFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

/* ------------------------------ 调用 ------------------------------ */

func TestCallNZeroReturnsEmptySlice(t *testing.T) {
	l := newLua(t)
	if _, err := l.LoadFile(filepath.Join("testdata", "m.lua")); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	rets, err := l.CallN("m.Test", 0, "1", "2", "3")
	if err != nil {
		t.Fatalf("CallN: %v", err)
	}
	if len(rets) != 0 {
		t.Fatalf("CallN(nret=0) = %v, want empty", rets)
	}
}

func TestSetFunctionErrorResults(t *testing.T) {
	l := newLua(t)
	l.SetFunction(failNow, okNow)

	if err := l.DoString("failNow()"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("failNow error = %v, want error containing nope", err)
	}
	if err := l.DoString("r = okNow()"); err != nil {
		t.Fatalf("okNow: %v", err)
	}
	if got := globalString(t, l, "r"); got != "fine" {
		t.Fatalf("okNow = %q, want fine", got)
	}
}

func TestSetFunctionErrorOnlyReturn(t *testing.T) {
	l := newLua(t)
	l.SetFunction(errorOnly)

	if err := l.DoString("errorOnly()"); err == nil || !strings.Contains(err.Error(), "only-error") {
		t.Fatalf("errorOnly error = %v, want error containing only-error", err)
	}
}

func TestSetFunctionAnyParameter(t *testing.T) {
	l := newLua(t)
	l.SetFunction(takesAny)

	cases := map[string]string{
		"takesAny(42)":          "42",
		"takesAny('s')":         "s",
		"takesAny(true)":        "true",
		"takesAny({1, 2})":      "[1 2]",
		"takesAny({a = 1})":     "map[a:1]",
		"takesAny(nil)":         "<nil>",
	}
	for code, want := range cases {
		if err := l.DoString("r = " + code); err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if got := globalString(t, l, "r"); got != want {
			t.Fatalf("%s = %q, want %q", code, got, want)
		}
	}
}

func TestSetFunctionPointerParameter(t *testing.T) {
	l := newLua(t)
	l.SetFunction(takesPointer)

	if err := l.DoString("r = takesPointer({Name = 'ptr', Age = 1})"); err != nil {
		t.Fatalf("takesPointer: %v", err)
	}
	if got := globalString(t, l, "r"); got != "ptr" {
		t.Fatalf("takesPointer = %q, want ptr", got)
	}
}

/* ------------------------------ 注册 ------------------------------ */

func TestSetGlobalInvalidValuesDoNotPanic(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(nil, 123, "abc", struct{}{}, &struct{}{})

	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestSetGlobalValueReceiverExposesNoMethods(t *testing.T) {
	l := newLua(t)
	l.SetGlobal(counter{Value: 1})

	// 指针接收器的方法不在值类型的方法集里，因此不会注册
	if err := l.DoString("GetValue()"); err == nil {
		t.Fatal("pointer receiver methods should not be registered for a value receiver")
	}
}

func TestModuleNilValueDoesNotPanic(t *testing.T) {
	l := newLua(t)
	l.Module("nilmod", nil)

	if err := l.DoString("local m = require('nilmod')"); err == nil {
		t.Fatal("requiring a module registered from nil should fail")
	}
	if err := l.DoString("x = 1"); err != nil {
		t.Fatalf("state should still be usable: %v", err)
	}
}

func TestModulesRegistersUnderDerivedName(t *testing.T) {
	l := newLua(t)
	c := &counter{Value: 4}
	l.Modules(c)

	name := moduleNameFromType(typeName(reflect.TypeOf(c)))
	code := fmt.Sprintf("local c = require('%s'); c.Add(6); r = c.GetValue()", name)
	if err := l.DoString(code); err != nil {
		t.Fatalf("DoString(%s): %v", code, err)
	}
	if got := globalNumber(t, l, "r"); got != 10 {
		t.Fatalf("r = %v, want 10", got)
	}
	if c.Value != 10 {
		t.Fatalf("counter.Value = %d, want 10", c.Value)
	}
}

/* ------------------------------ 日志 ------------------------------ */

func TestLogModeControlsDebugOutput(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	l := newLua(t)
	l.Module("quiet", &counter{})
	if buf.Len() != 0 {
		t.Fatalf("silent mode should not log, got %q", buf.String())
	}

	l.LogMode(LogModeDebug)
	l.Module("loud", &counter{})
	if !strings.Contains(buf.String(), "preload module") {
		t.Fatalf("debug mode should log module registration, got %q", buf.String())
	}

	buf.Reset()
	l.SetGlobal(nil)
	if !strings.Contains(buf.String(), "SetGlobal") {
		t.Fatalf("registration errors should be reported, got %q", buf.String())
	}
}

/* ------------------------------ 关闭后的行为 ------------------------------ */

func TestClosedStateEveryMethodReturnsErrClosed(t *testing.T) {
	l := New()
	l.Close()

	cases := []struct {
		name string
		err  error
	}{
		{"DoString", l.DoString("x = 1")},
		{"DoFile", l.DoFile(filepath.Join("testdata", "m.lua"))},
		{"LoadFile", func() error { _, e := l.LoadFile(filepath.Join("testdata", "m.lua")); return e }()},
		{"LoadString", func() error { _, e := l.LoadString("m", "return {}"); return e }()},
		{"LoadDir", l.LoadDir("testdata")},
		{"Call", func() error { _, e := l.Call("m.Test"); return e }()},
		{"Call2", func() error { _, _, e := l.Call2("m.Test"); return e }()},
		{"CallN", func() error { _, e := l.CallN("m.Test", 1); return e }()},
		{"WatchFile", l.WatchFile(filepath.Join("testdata", "m.lua"))},
		{"WatchDir", l.WatchDir("testdata")},
		{"LoadAndWatchFile", l.LoadAndWatchFile(filepath.Join("testdata", "m.lua"))},
		{"LoadAndWatchDir", l.LoadAndWatchDir("testdata")},
	}
	for _, c := range cases {
		if !errors.Is(c.err, ErrClosed) {
			t.Fatalf("%s after close = %v, want ErrClosed", c.name, c.err)
		}
	}

	// 其余方法关闭后也不应 panic
	l.LogMode(LogModeDebug)
	l.SetGlobal(&counter{})
	l.SetFunction(addAll)
	l.Modules(&counter{})
	l.Module("afterclose", &counter{})
	if !l.Closed() {
		t.Fatal("Closed() should be true")
	}
}

func TestCloseWhileWatching(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cw.lua")
	writeFile(t, file, "return {V=function() return 'v1' end}")

	l := newLua(t)
	if err := l.LoadAndWatchFile(file); err != nil {
		t.Fatalf("LoadAndWatchFile: %v", err)
	}
	l.Close()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watcher != nil {
		t.Fatal("watcher should be released on Close")
	}
	if len(l.watchedFiles) != 0 || len(l.watchedDirs) != 0 {
		t.Fatalf("watch state should be cleared on Close: files=%v dirs=%v", l.watchedFiles, l.watchedDirs)
	}
}

/* ------------------------------ 单例与并发 ------------------------------ */

func TestNewStateConcurrent(t *testing.T) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make([]*Luax, 0, 32)
	)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewState()
			if s == nil || s.Closed() {
				t.Errorf("NewState returned a nil or closed instance")
				return
			}
			mu.Lock()
			seen = append(seen, s)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != 32 {
		t.Fatalf("collected %d instances, want 32", len(seen))
	}
	for _, s := range seen {
		if s != seen[0] {
			t.Fatal("NewState returned different instances under concurrency")
		}
	}
}

func TestConcurrentSetGlobalSharesReceiver(t *testing.T) {
	l := newLua(t)
	c := &counter{}
	l.SetGlobal(c)

	const goroutines = 8
	const perGoroutine = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if err := l.DoString("Increment()"); err != nil {
					t.Errorf("DoString: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if c.Value != goroutines*perGoroutine {
		t.Fatalf("counter.Value = %d, want %d", c.Value, goroutines*perGoroutine)
	}
}

/* ------------------------------ 导出字段 ------------------------------ */

func TestExportedLuaStateIsUsable(t *testing.T) {
	l := newLua(t)
	if l.L == nil {
		t.Fatal("L should not be nil")
	}
	if err := l.L.DoString("x = 1"); err != nil {
		t.Fatalf("raw DoString: %v", err)
	}
	if l.L.GetGlobal("x") != lua.LNumber(1) {
		t.Fatalf("x = %v, want 1", l.L.GetGlobal("x"))
	}
}
