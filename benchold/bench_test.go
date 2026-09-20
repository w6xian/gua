package gua

import (
	"path/filepath"
	"testing"
)

// 本文件只使用新旧两版都存在的 API，
// 这样可以把同一份基准测试直接复制到旧版代码（git HEAD）目录下跑出基线，
// 用于对比重构前后的性能变化。
//
// 跑法（新旧两版都这样跑）：
//
//	go test -run=^$ -bench=. -benchmem -count=5 .

func benchAdd(a, b int) int { return a + b }

type benchCounter struct{ Value int }

func (c *benchCounter) Add(n int) int { c.Value += n; return c.Value }

var benchL *Luax

// benchLua 复用同一个状态机（旧版没有 New()，只能用单例 NewState）
func benchLua(b *testing.B) *Luax {
	b.Helper()
	if benchL == nil {
		// RegistryMaxSize/GrowStep 必须显式给出：gopher-lua 在传了 Options 时
		// 默认禁用 registry 增长（RegistryMaxSize=0），需要在两版里保持一致的基线
		benchL = NewState(CallStackSize(1024), RegistrySize(1024), RegistryMaxSize(1<<16), RegistryGrowStep(4096))
		benchL.SetFunction(benchAdd)
		benchL.SetGlobal(&benchCounter{})
		benchL.Module("cnt", &benchCounter{Value: 1})
		if _, err := benchL.LoadFile(benchFile()); err != nil {
			b.Fatalf("LoadFile: %v", err)
		}
	}
	return benchL
}

func benchFile() string { return filepath.Join("testdata", "m.lua") }

// BenchmarkDoStringSimple 纯 Lua 代码执行
func BenchmarkDoStringSimple(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("x = 1 + 2"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoStringCallGoFunc Lua 调用注册的 Go 函数（2 个 int 参数 + 1 个返回值）
func BenchmarkDoStringCallGoFunc(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("y = benchAdd(1, 2)"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoStringCallGoMethod Lua 调用注册的 Go 方法（指针接收器）
func BenchmarkDoStringCallGoMethod(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("z = Add(1)"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoStringCallModuleFunc Lua 通过 require 调用 Go 模块方法
func BenchmarkDoStringCallModuleFunc(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("local cnt = require('cnt'); w = cnt.Add(1)"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCall 通过 Call 调用 Lua 模块函数（1 个返回值）
func BenchmarkCall(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Call("m.Test2", "a", "b"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCallN2 通过 CallN 调用 Lua 模块函数（2 个返回值）
func BenchmarkCallN2(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.CallN("m.Test", 2, "1", "2", "3"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoFile 编译并执行 Lua 文件
func BenchmarkDoFile(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoFile(benchFile()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoStringRaw 直接调用 gopher-lua（绕过 gua 封装），用于分离封装自身的开销
func BenchmarkDoStringRaw(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.L.DoString("x = 1 + 2"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoadFileRaw 直接调用 gopher-lua（绕过 gua 封装），用于分离封装自身的开销
func BenchmarkLoadFileRaw(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.L.LoadFile(benchFile()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoadFile 编译 Lua 文件（不执行）
func BenchmarkLoadFile(b *testing.B) {
	l := benchLua(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.LoadFile(benchFile()); err != nil {
			b.Fatal(err)
		}
	}
}
