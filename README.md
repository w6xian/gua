# gua

[![Go Report Card](https://goreportcard.com/badge/github.com/w6xian/gua)](https://goreportcard.com/report/github.com/w6xian/gua)

## 项目简介

`gua` 是一个 Go 语言与 Lua 脚本交互的桥接库，通过反射机制实现 Go 函数和 Lua 函数的相互调用。它保持与当前 Golang 调用方式一致，允许在 Lua 中调用 Go 函数并直接返回 Go 函数的返回值，同时支持改变运行时的上下文环境。

## 特性

- 支持 Go 与 Lua 之间的无缝函数调用
- 支持全局函数注册与调用
- 支持全局模块注册与 require 调用
- 支持通过实例注册全局状态
- 支持运行时上下文环境的修改
- 支持加载 Lua 模块文件（LoadFile）
- 支持从 Go 调用 Lua 模块函数（Call/Call2/CallN）
- 支持监听 Lua 文件变化并自动重载（WatchFile/WatchDir）

## 稳定性保证

- **并发安全**：`Luax` 所有方法内部串行化对 `*lua.LState` 的访问，可被多个 goroutine 同时使用
- **不会崩溃宿主进程**：注册给 Lua 的 Go 函数发生 panic 时会被转换成 Lua 错误；`DoString`/`DoFile`/`LoadFile`/`Call*` 内部统一 recover
- **关闭后安全**：`Close()` 幂等且可重复调用；关闭之后所有方法返回 `ErrClosed`（`errors.Is` 可判定），不再 panic
- **类型安全**：Go 与 Lua 的值转换不再 panic，不支持的类型返回明确的错误

### 类型转换支持

| 方向 | 支持的类型 |
| --- | --- |
| Go -> Lua | bool、所有（无符号）整数、float32/64、string、slice/array、map、struct（仅导出字段）、指针/interface（自动解引用，nil 为 `nil`） |
| Lua -> Go | bool、所有（无符号）整数、float32/64、string（数字字符串可转数字）、slice/array、map、struct、指针、空 interface |
| 返回值 | 若返回值是 `error` 且非 nil，会转换为 Lua 错误；nil error 对应 `nil` |

### 使用注意

- 不要在注册给 Lua 的 Go 函数内部回调同一个 `Luax`（内部锁不可重入，会死锁），需要回调 Lua 时请使用入参里的 `*lua.LState`
- 直接读写导出字段 `L` 不会加锁，需由调用方保证串行
- 用 Option 指定 `RegistrySize` 时请同时指定 `RegistryMaxSize`：gopher-lua 在传入 Options 时默认 `RegistryMaxSize=0`（即禁止 registry 自动增长），高并发或深调用可能触发 `registry overflow`

## 安装

```bash
go get github.com/w6xian/gua
```

## 快速开始

### 基本使用

```go
package main

import (
	"fmt"
	"github.com/w6xian/gua"
)

// 定义一个Go函数
func GetNum(a int) int {
	fmt.Println("GetNum:", a)
	return 1000 + a
}

func main() {
	// 创建Lua状态机
	L := gua.NewState(gua.CallStackSize(1024))
	defer L.Close()
	
	// 注册全局函数
	L.SetFunction(GetNum)
	
	// 在Lua中调用Go函数
	L.DoString("print(GetNum(100));") // 输出: 1100
}
```

## 高级用法

### 1. 全局函数

```go
func GetNum(a int) int {
	fmt.Println("GetNum:", a)
	return 1000 + a
}

L.SetFunction(GetNum) // 注册全局函数
L.DoString("print(GetNum(100));") // 调用Lua函数GetNum
```

### 2. 全局状态

```go
type Call struct {
	Num1 int
	Num2 int
}

func (c *Call) GetSub(a int) string {
	return fmt.Sprintf("%d-%d-%d", c.Num1, c.Num2, a)
}

func (c *Call) Set(a int) {
	c.Num1 = a
}

func (c *Call) GetNum1() int {
	return c.Num1
}

// 注册全局状态
call := &Call{Num1: 10, Num2: 20}
L.SetGlobal(call)

// 在Lua中调用
L.DoString("print(GetNum1());") // 输出: 10
L.DoString("Set(100);")
L.DoString("print(GetNum1());") // 输出: 100
```

### 3. 全局模块

通过实例注册全局模块，可在lua中通过require调用，模块中的函数可直接调用，同时改变运行时的上下文环境。

```go
type Test struct {
	Num3 int
}

func (t *Test) GetNum3() int {
	return t.Num3
}

func (t *Test) SetNum3(a int) {
	t.Num3 = a
}

// 注册全局模块（自动生成模块名）
t := &Test{Num3: 305}
L.Modules(t)

// 在Lua中通过require调用
// 模块名由包路径与类型名组合生成，并会在日志中打印 preload module: [xxx]
// 例如 main 包下的 Test 类型，模块名一般为 "main/test"
L.DoString("local test = require('main/test')")
L.DoString("print(test.GetNum3());") // 输出: 305
L.DoString("test.SetNum3(400);")
L.DoString("print(test.GetNum3());") // 输出: 400
```

也可以显式指定模块名，方便 require：

```go
// 指定模块名为 "tt"
L.Module("tt", t)
L.DoString("local test = require('tt')")
L.DoString("print(test.GetNum3());")
```

### 4. 执行Lua文件

```go
// 执行Lua文件
L.DoFile("script.lua")
```

### 5. 加载 Lua 模块并调用函数

Lua 模块文件需要 `return` 一个表（模块表），Go 侧通过 `LoadFile` 注册模块，然后用 `Call/Call2/CallN` 调用其中的函数。

`m.lua` 示例：

```lua
local m = {}

function m.Test(a, b, c)
  return a + b + c, 1
end

return m
```

Go 调用示例：

```go
L := gua.NewState(gua.CallStackSize(1024))
defer L.Close()

_, err := L.LoadFile("m.lua")
if err != nil {
    panic(err)
}

ret, err := L.Call("m.Test", "100", "200", "300")
if err != nil {
    panic(err)
}
fmt.Println(ret)

ret1, ret2, err := L.Call2("m.Test", "100", "200", "300")
if err != nil {
    panic(err)
}
fmt.Println(ret1, ret2)

rets, err := L.CallN("m.Test", 2, "100", "200", "300")
if err != nil {
    panic(err)
}
fmt.Println(rets)
```

### 6. 监听 Lua 文件变化并自动重载

如果希望修改 `.lua` 文件后自动刷新缓存，可以使用 `WatchFile` 或 `WatchDir`。监听到文件创建、写入、重命名后，`gua` 会自动重新执行 `LoadFile`，后续 `Call` 会使用最新版本的 Lua 模块。

```go
L := gua.NewState(gua.CallStackSize(1024))
defer L.Close()

if err := L.WatchFile("m.lua"); err != nil {
    panic(err)
}

ret, err := L.Call("m.Test", "100", "200", "300")
if err != nil {
    panic(err)
}
fmt.Println(ret)
```

也可以监听整个目录：

```go
if err := L.WatchDir("modal"); err != nil {
    panic(err)
}
```

注意：
- 仅监听 `.lua` 文件
- 自动重载会更新 `LoadFile/LoadDir` 注册到内存中的模块缓存
- 关闭 `Luax` 时会自动停止监听

## API 文档

### 核心方法

#### NewState
返回进程内共享的 Lua 状态机（单例）。首次调用时创建；实例被 Close 后再次调用会重新创建。

```go
func NewState(options ...Option) *Luax
```

#### New
创建一个相互隔离的全新 Lua 状态机（非单例），适合测试或需要多环境的场景。

```go
func New(options ...Option) *Luax
```

#### Closed
返回状态机是否已经关闭。

```go
func (l *Luax) Closed() bool
```

#### ErrClosed
状态机关闭后所有方法返回的错误，可用 `errors.Is(err, gua.ErrClosed)` 判定。

#### SetFunction
注册全局函数

```go
func (l *Luax) SetFunction(fns ...interface{})
```

#### SetGlobal
注册全局状态

```go
func (l *Luax) SetGlobal(objs ...interface{})
```

#### Module
注册全局模块

```go
func (l *Luax) Modules(objs ...interface{})
```

#### Module
注册一个指定名称的全局模块

```go
func (l *Luax) Module(name string, obj interface{})
```

#### DoString
执行Lua字符串

```go
func (l *Luax) DoString(str string) error
```

#### DoFile
执行Lua文件

```go
func (l *Luax) DoFile(file string) error
```

#### LoadFile
加载 Lua 模块文件（要求文件内 `return` 模块表），并以文件名（去掉 `.lua` 后缀）作为模块名注册到运行时。

```go
func (l *Luax) LoadFile(filename string) (*lua.LFunction, error)
```

#### WatchFile
监听单个 Lua 文件，文件变化后自动重新加载。

```go
func (l *Luax) WatchFile(filename string) error
```

#### WatchDir
监听目录下的 Lua 文件，文件变化后自动重新加载。

```go
func (l *Luax) WatchDir(dir string) error
```

#### Call
调用 Lua 模块函数，函数名格式为 `"模块名.函数名"`，默认返回 1 个值（以字符串形式返回）。

```go
func (l *Luax) Call(mn string, args ...string) (string, error)
```

#### Call2
调用 Lua 模块函数并返回 2 个值（以字符串形式返回）。

```go
func (l *Luax) Call2(mn string, args ...string) (string, string, error)
```

#### CallN
调用 Lua 模块函数并返回 N 个值（以字符串切片形式返回）。

```go
func (l *Luax) CallN(mn string, nret int, args ...string) ([]string, error)
```

#### Close
关闭Lua状态机并停止文件监听。可重复调用（幂等），关闭后再调用其他方法会返回 `ErrClosed`。

```go
func (l *Luax) Close()
```

### 并发使用示例

```go
L := gua.New(gua.CallStackSize(1024))
defer L.Close()

L.LoadFile("m.lua")

var wg sync.WaitGroup
for i := 0; i < 8; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := L.DoString("n = (n or 0) + 1"); err != nil {
            log.Println(err)
        }
        if ret, err := L.Call("m.Test", "1", "2", "3"); err == nil {
            _ = ret
        }
    }()
}
wg.Wait()
```

## 性能基准

环境：Windows / Intel i5-9400F / Go 1.24 / `go test -cpu=1 -count=3 -benchtime=2s`，取 3 轮最小值。
旧版 = `git HEAD` 的 `gua.go`，新版 = 当前实现，同一份 `bench_test.go` 在两版目录下各跑一次。

| 基准 | 旧版 ns/op | 新版 ns/op | 变化 |
| --- | --- | --- | --- |
| `DoString("x = 1 + 2")` | 8,544 | 8,587 | +0.5% |
| DoString 调用 Go 方法 | 10,198 | 10,512 | +3.1% |
| DoString 调用 Go 函数 | 不可用（旧版注册名错误） | 10,909 | — |
| DoString + require 调用 Go 模块 | 16,729 | 13,450 | **-19.6%（1.24x）** |
| `Call`（1 个返回值） | 2,289 | 2,384 | +4.2% |
| `CallN`（2 个返回值） | 2,583 | 2,806 | +8.6% |
| `DoFile` | 123,738 | 120,836 | -2.3% |
| `LoadFile` | 96,289 | 114,196 | +18.6% |
| 裸 `DoString`（绕过封装，噪声对照） | 11,211 | 9,484 | -15.4% |
| 裸 `LoadFile`（绕过封装，噪声对照） | 102,102 | 112,901 | +10.6% |

结论：加锁、recover、关闭检查这些稳定性加固**没有可测量的性能损失**——
两版差异都落在噪声区间内（裸调用对照组显示本机噪声下限约 ±10~20%），`require` 模块调用反而快约 20%。

新增类型转换能力（新版独有，无旧版基线，`-count=3 -cpu=1` 最小值）：

| 转换 | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| 标量 int | 21 | 8 | 1 |
| struct → table | 1,123 | 3,392 | 16 |
| map（5 个键）→ table | 2,347 | 4,024 | 43 |
| `[]int`（100 元素）→ table | 4,203 | 7,248 | 107 |
| table → struct | 245 | 48 | 3 |
| Lua 调用返回 struct 的 Go 方法（端到端） | 13,023 | 35,944 | 80 |
| Lua 传 table 给 Go 方法（端到端） | 16,884 | 34,176 | 117 |
| Lua 调用返回 100 元素切片的 Go 方法 | 15,128 | 39,800 | 171 |

### 基准测出并修复的缺陷：Lua 栈泄漏

gopher-lua 的 `DoString`/`DoFile` 内部是 `PCall(0, MultRet)`，脚本的返回值会残留在 Lua 栈上。
旧版反复调用会不断抬高栈顶，最终触发 `registry overflow`：

```
旧版：第 1022 次 DoString("return 42") → registry overflow
新版：2000 次后栈顶仍为 0（runScript 在执行后恢复栈高度）
```

### 复现方式

```bash
# 当前实现
go test -run=^$ -bench='Benchmark(DoString|DoFile|Call|Load)' -benchmem -count=3 -cpu=1 .
go test -run=^$ -bench='Benchmark(Convert|LuaCall)' -benchmem -count=3 -cpu=1 .

# 旧版基线：把 HEAD 版本还原到临时目录，跑同一份 bench_test.go
mkdir -p /tmp/gua-old
git show HEAD:gua.go > /tmp/gua-old/gua.go
git show HEAD:options.go > /tmp/gua-old/options.go
cp go.mod go.sum bench_test.go leak_test.go /tmp/gua-old/ && cp -r testdata /tmp/gua-old/
cd /tmp/gua-old && go test -run=^$ -bench='Benchmark(DoString|DoFile|Call|Load)' -benchmem -count=3 -cpu=1 .
```

## 示例

完整的示例代码请查看 [examples](examples/) 目录。

## 测试

```bash
go test ./...
go test -race ./...   # 并发安全
```

## 贡献

欢迎提交 Issue 和 Pull Request 来帮助改进这个项目！

## 许可证

MIT License
