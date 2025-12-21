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

	L.DoString("print(GetNum1());")
	L.DoString("Set(100);")
	L.DoString("print(GetNum1());")
	L.DoString("print(GetNum2());")
	L.DoFile("t.lua")
	fmt.Println("main.go", t.GetNum3())
}
