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

## API 文档

### 核心方法

#### NewState
创建一个新的Lua状态机

```go
func NewState(options ...Option) *Luax
```

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
关闭Lua状态机

```go
func (l *Luax) Close()
```

## 示例

完整的示例代码请查看 [examples](examples/) 目录。

## 测试

```bash
go test ./...
```

## 贡献

欢迎提交 Issue 和 Pull Request 来帮助改进这个项目！

## 许可证

MIT License
