package gua

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// 本文件针对 options.go 的各个 Option 做单元测试。

func TestOptionsApplyToLuaOptions(t *testing.T) {
	var o lua.Options
	for _, opt := range []Option{
		CallStackSize(120),
		RegistrySize(256),
		RegistryMaxSize(4096),
		RegistryGrowStep(64),
		SkipOpenLibs(true),
		IncludeGoStackTrace(true),
		MinimizeStackMemory(true),
	} {
		opt(&o)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"CallStackSize", o.CallStackSize, 120},
		{"RegistrySize", o.RegistrySize, 256},
		{"RegistryMaxSize", o.RegistryMaxSize, 4096},
		{"RegistryGrowStep", o.RegistryGrowStep, 64},
		{"SkipOpenLibs", o.SkipOpenLibs, true},
		{"IncludeGoStackTrace", o.IncludeGoStackTrace, true},
		{"MinimizeStackMemory", o.MinimizeStackMemory, true},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestNewIgnoresNilOption(t *testing.T) {
	l := New(nil, CallStackSize(128), nil)
	defer l.Close()

	if err := l.DoString("x = 1 + 2"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestNewWithDefaultOptions(t *testing.T) {
	l := New()
	defer l.Close()

	if err := l.DoString("x = 1 + 2"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if l.Closed() {
		t.Fatal("a fresh Luax should not be closed")
	}
}

func TestSkipOpenLibs(t *testing.T) {
	l := New(SkipOpenLibs(true))
	defer l.Close()

	// 基本语法与赋值不依赖标准库
	if err := l.DoString("x = 1 + 2"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	// print 来自基础库，跳过加载后不可用
	if err := l.DoString("print(1)"); err == nil {
		t.Fatal("print should be unavailable when std libs are skipped")
	}
}

func TestMinimizeStackMemory(t *testing.T) {
	l := New(MinimizeStackMemory(true), CallStackSize(128))
	defer l.Close()

	l.SetFunction(addAll)
	if err := l.DoString("r = addAll(2, 3)"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if got := globalNumber(t, l, "r"); got != 5 {
		t.Fatalf("addAll = %v, want 5", got)
	}
}
