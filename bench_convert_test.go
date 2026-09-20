package gua

import (
	"reflect"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// 本文件只针对重构后新增的能力做基准测量（旧版不支持这些转换，没有对比基线）

type benchConf struct {
	Name string
	Age  int
}

type benchSvc struct{}

func (s *benchSvc) Slice() []int            { return benchSlice }
func (s *benchSvc) Map() map[string]int     { return benchMap }
func (s *benchSvc) Struct() benchConf       { return benchConf{Name: "leo", Age: 18} }
func (s *benchSvc) Echo(c benchConf) string { return c.Name }
func (s *benchSvc) Sum(a, b int) (int, int) { return a + b, a - b }

var benchSlice = func() []int {
	s := make([]int, 100)
	for i := range s {
		s[i] = i
	}
	return s
}()

var benchMap = map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}

func newBenchLuaNew(b *testing.B) *Luax {
	b.Helper()
	l := New(CallStackSize(1024))
	b.Cleanup(l.Close)
	return l
}

// BenchmarkConvertInt Go -> Lua：标量
func BenchmarkConvertInt(b *testing.B) {
	l := newBenchLuaNew(b)
	v := reflect.ValueOf(42)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := toLValue(l.L, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvertGoSliceToLua Go -> Lua：100 元素切片
func BenchmarkConvertGoSliceToLua(b *testing.B) {
	l := newBenchLuaNew(b)
	v := reflect.ValueOf(benchSlice)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := toLValue(l.L, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvertGoMapToLua Go -> Lua：5 个键的 map
func BenchmarkConvertGoMapToLua(b *testing.B) {
	l := newBenchLuaNew(b)
	v := reflect.ValueOf(benchMap)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := toLValue(l.L, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvertGoStructToLua Go -> Lua：struct
func BenchmarkConvertGoStructToLua(b *testing.B) {
	l := newBenchLuaNew(b)
	v := reflect.ValueOf(benchConf{Name: "leo", Age: 18})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := toLValue(l.L, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvertLuaTableToStruct Lua -> Go：表转 struct
func BenchmarkConvertLuaTableToStruct(b *testing.B) {
	l := newBenchLuaNew(b)
	tb := l.L.NewTable()
	tb.RawSetString("Name", lua.LString("leo"))
	tb.RawSetString("Age", lua.LNumber(18))
	t := reflect.TypeOf(benchConf{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fromLValue(l.L, tb, t); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLuaCallStructMethod 端到端：Lua 调用返回 struct 的 Go 方法
func BenchmarkLuaCallStructMethod(b *testing.B) {
	l := newBenchLuaNew(b)
	l.SetGlobal(&benchSvc{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("r = Struct()"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLuaCallStructArg 端到端：Lua 传 table 给 Go 方法
func BenchmarkLuaCallStructArg(b *testing.B) {
	l := newBenchLuaNew(b)
	l.SetGlobal(&benchSvc{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("r = Echo({Name = 'bob', Age = 3})"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLuaCallSliceMethod 端到端：Lua 调用返回 100 元素切片的 Go 方法
func BenchmarkLuaCallSliceMethod(b *testing.B) {
	l := newBenchLuaNew(b)
	l.SetGlobal(&benchSvc{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.DoString("r = Slice()"); err != nil {
			b.Fatal(err)
		}
	}
}
