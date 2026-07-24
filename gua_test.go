package gua

import (
	"testing"
)

// 测试函数，用于TestFunctionRegistration
func sayHello(name string) string {
	return "Hello, " + name
}

// 测试结构体，用于TestStructMethods
type Counter struct {
	Value int
}

func (c *Counter) Increment() int {
	c.Value++
	return c.Value
}

func (c *Counter) GetValue() int {
	return c.Value
}

func (c *Counter) Add(a int) int {
	c.Value += a
	return c.Value
}

// 测试结构体，用于TestModuleFunctionality
type Calculator struct{}

func (c *Calculator) Add(a, b int) int {
	return a + b
}

func (c *Calculator) Subtract(a, b int) int {
	return a - b
}

// 测试基础功能
func TestBasicFunctionality(t *testing.T) {
	// 创建Lua状态机
	L := NewState()

	// 测试DoString基本功能
	err := L.DoString("print('Hello from Lua!')")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	// 测试简单的Lua计算
	err = L.DoString("result = 10 + 20")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	// 打印结果
	err = L.DoString("print('计算结果:', result)")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	// 测试基本的全局变量设置
	err = L.DoString("globalVar = '测试变量'")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	// 打印全局变量
	err = L.DoString("print('全局变量:', globalVar)")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	// 测试基本功能完成
	t.Log("基础功能测试完成")
}

// 测试结构体和方法
func TestStructMethods(t *testing.T) {
	L := NewState()

	// 创建实例并注册
	counter := &Counter{Value: 0}
	L.SetGlobal(counter)

	// 测试基本功能
	err := L.DoString("print('TestStructMethods: 测试结构体方法')")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	t.Log("结构体方法测试完成")
}

// 测试模块功能
func TestModuleFunctionality(t *testing.T) {
	L := NewState()

	// 创建实例并注册为模块
	calculator := &Calculator{}
	L.Modules(calculator)

	// 测试基本功能
	err := L.DoString("print('TestModuleFunctionality: 测试模块功能')")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	t.Log("模块功能测试完成")
}

// 测试函数注册
func TestFunctionRegistration(t *testing.T) {
	L := NewState()

	// 注册函数
	L.SetFunction(sayHello)

	// 测试基本功能
	err := L.DoString("print('TestFunctionRegistration: 测试函数注册')")
	if err != nil {
		t.Errorf("执行Lua代码失败: %v", err)
	}

	t.Log("函数注册测试完成")
}
