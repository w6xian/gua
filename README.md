
gua = go+lua
通过反射实现go函数和lua函数的转化，与当前Golang调用方式保持一致，可通过lua调用go函数,直接返回go函数的返回值，同时改变运行时的上下文环境

### 示例
* 实现全局VM
```
L := gua.NewState(gua.CallStackSize(1024))
```
#### 支持三种转化方式
* 全局函数
```
func GetNum(a int) int {
	fmt.Println("GetNum:", a)
	return 1000 + a
}
L.SetFunction(GetNum)// 支持数组
// 调用lua函数GetNum
L.DoString("print(GetNum(100));")
```
* 全局模块
通过实例注册全局模块，可在lua中通过require调用，模块中的函数可直接调用，同时改变运行时的上下文环境
L.Module(...)
```
package main

import (
	"fmt"

	"github.com/w6xian/gua"
	"github.com/w6xian/gua/examples/tm"
)

type Call struct {
	Num1 int
	Num2 int
}

func (c *Call) GetSub(a int) string {
	return fmt.Sprintf("%d-%d-%d-leo", c.Num1, c.Num2, a)
}

func (c *Call) GetMax() int {
	return max(c.Num1, c.Num2)
}
func (c *Call) Get(a, b int) (int, int) {
	return max(a, b), min(a, b)
}
func (c *Call) Set(a int) {
	c.Num1 = a
}
func (c *Call) GetNum1() int {
	return c.Num1
}

func GetNum(a int) int {
	fmt.Println("GetNum:", a)
	return 1000 + a
}

func main() {

	call := &Call{Num1: 10, Num2: 20}
	callx := &tm.Callx{Num2: 1001}
	t := &tm.Test{Num3: 305}
	// lua run time VM
	L := gua.NewState(gua.CallStackSize(1024))
	defer L.Close()
	// global faction
	L.SetGlobal(call, callx)
	// global module "require"
	L.Module(t)
	// global function
	L.SetFunction(GetNum)

	fmt.Println("-------")
	L.DoString("print(GetNum(100));")
	fmt.Println("-------")
	L.DoString("print(GetNum1());")
	L.DoString("Set(100);")
	L.DoString("print(GetNum1());")
	L.DoString("print(GetNum2());")
	L.DoFile("t.lua")
	// 被改变的上下文环境
	fmt.Println("main.go", t.GetNum3())
}


```
