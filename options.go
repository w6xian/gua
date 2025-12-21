package gua

import lua "github.com/yuin/gopher-lua"

type Option func(*lua.Options)

func CallStackSize(size int) Option {
	return func(opt *lua.Options) {
		opt.CallStackSize = size
	}
}

func RegistrySize(size int) Option {
	return func(opt *lua.Options) {
		opt.RegistrySize = size
	}
}

func RegistryMaxSize(size int) Option {
	return func(opt *lua.Options) {
		opt.RegistryMaxSize = size
	}
}

func RegistryGrowStep(size int) Option {
	return func(opt *lua.Options) {
		opt.RegistryGrowStep = size
	}
}

func SkipOpenLibs(skip bool) Option {
	return func(opt *lua.Options) {
		opt.SkipOpenLibs = skip
	}
}

// 	IncludeGoStackTrace bool
func IncludeGoStackTrace(include bool) Option {
	return func(opt *lua.Options) {
		opt.IncludeGoStackTrace = include
	}
}

// 	MinimizeStackMemory bool
func MinimizeStackMemory(minimize bool) Option {
	return func(opt *lua.Options) {
		opt.MinimizeStackMemory = minimize
	}
}

// // Call stack size. This defaults to `lua.CallStackSize`.
// 	CallStackSize int
// 	// Data stack size. This defaults to `lua.RegistrySize`.
// 	RegistrySize int
// 	// Allow the registry to grow from the registry size specified up to a value of RegistryMaxSize. A value of 0
// 	// indicates no growth is permitted. The registry will not shrink again after any growth.
// 	RegistryMaxSize int
// 	// If growth is enabled, step up by an additional `RegistryGrowStep` each time to avoid having to resize too often.
// 	// This defaults to `lua.RegistryGrowStep`
// 	RegistryGrowStep int
// 	// Controls whether or not libraries are opened by default
// 	SkipOpenLibs bool
// 	// Tells whether a Go stacktrace should be included in a Lua stacktrace when panics occur.
// 	IncludeGoStackTrace bool
// 	// If `MinimizeStackMemory` is set, the call stack will be automatically grown or shrank up to a limit of
// 	// `CallStackSize` in order to minimize memory usage. This does incur a slight performance penalty.
// 	MinimizeStackMemory bool
