package gua

import lua "github.com/yuin/gopher-lua"

// Option 用于配置底层 lua.Options
type Option func(*lua.Options)

// CallStackSize 设置调用栈大小
func CallStackSize(size int) Option {
	return func(opt *lua.Options) { opt.CallStackSize = size }
}

// RegistrySize 设置数据栈（registry）初始大小
func RegistrySize(size int) Option {
	return func(opt *lua.Options) { opt.RegistrySize = size }
}

// RegistryMaxSize 设置 registry 可增长到的最大大小，0 表示不允许增长
func RegistryMaxSize(size int) Option {
	return func(opt *lua.Options) { opt.RegistryMaxSize = size }
}

// RegistryGrowStep 设置 registry 每次增长的步长
func RegistryGrowStep(size int) Option {
	return func(opt *lua.Options) { opt.RegistryGrowStep = size }
}

// SkipOpenLibs 是否跳过标准库的加载
func SkipOpenLibs(skip bool) Option {
	return func(opt *lua.Options) { opt.SkipOpenLibs = skip }
}

// IncludeGoStackTrace 发生 panic 时是否在 Lua 栈信息中包含 Go 的调用栈
func IncludeGoStackTrace(include bool) Option {
	return func(opt *lua.Options) { opt.IncludeGoStackTrace = include }
}

// MinimizeStackMemory 是否自动伸缩调用栈以节省内存（有轻微性能损耗）
func MinimizeStackMemory(minimize bool) Option {
	return func(opt *lua.Options) { opt.MinimizeStackMemory = minimize }
}
