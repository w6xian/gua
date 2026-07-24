package main

import (
	"fmt"

	"github.com/w6xian/gua"
	"github.com/w6xian/gua/examples/tm"
	"github.com/w6xian/gua/examples/tt"
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
	t4 := &tt.Test4{Num4: 405}

	// lua run time VM
	L := gua.NewState(gua.CallStackSize(1024))
	defer L.Close()
	// global faction
	L.SetGlobal(call, callx)
	// global module "require"
	L.Modules(t)
	// named module name
	L.Module("tt", t4)
	// global function
	L.SetFunction(GetNum)

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
	fmt.Println("main.go", t.GetNum3())
	// load module m.lua
	L.LoadDir("modal")
	_, err := L.LoadFile("n.lua")
	if err != nil {
		fmt.Println("LoadFile error:", err)
		return
	}
	_, loadErr := L.LoadString("c", "return {Test = function(a, b, c) return a, b, c end, Test2 = function(a, b) return a, b end}")
	if loadErr != nil {
		fmt.Println("LoadString error:", loadErr)
		return
	}

	ret, err := L.Call("m.Test", "100", "200", "300")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(ret)
	ret1, ret2, err := L.Call2("m.Test", "100", "200", "300")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(ret1, ret2)
	rets, err := L.CallN("m.Test", 2, "100", "200", "300")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(rets)
	ret, err = L.Call("m.Test2", "100", "200")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(ret)
	ret, err = L.Call("n.Max", "100", "200")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(ret)
	ret3, err := L.CallN("c.Test", 3, "100", "200", "300")
	if err != nil {
		fmt.Println("Call error:", err)
		return
	}
	fmt.Println(ret3)

}
