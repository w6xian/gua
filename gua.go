// Package gua 提供了与Lua脚本交互的功能，基于gopher-lua库
// 主要功能包括：创建Lua状态、注册全局变量和函数、注册模块、执行Lua代码
package gua

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	lua "github.com/yuin/gopher-lua"
)

// _luax 全局Luax实例
var _luax *Luax

// once 确保Luax实例只被创建一次
var once sync.Once

// Option Lua选项函数类型
// 用于配置lua.Options
// TODO: 定义Option类型，当前代码中使用了但未定义

// Luax Lua执行环境的封装结构体
// 包含了lua.LState实例，提供了更便捷的方法来操作Lua

type Luax struct {
	L *lua.LState // Lua状态实例

	Fn map[string]*lua.LFunction // 存储注册的Go函数到Lua函数的映射

	logMode      LogMode
	mu           sync.Mutex
	watcher      *fsnotify.Watcher
	watchedFiles map[string]string
	watchedDirs  map[string]struct{}
	lastReload   map[string]time.Time
	stopWatch    chan struct{}
	watchOnce    sync.Once
}

const reloadDebounce = 200 * time.Millisecond

type LogMode int

const (
	LogModeSilent LogMode = iota
	LogModeDebug
)

// ServiceFuncs 服务函数结构体
// 存储服务的名称、接收器和注册的方法

type ServiceFuncs struct {
	N string                    // name of service - 服务名称
	V reflect.Value             // receiver of methods for the service - 服务方法的接收器
	M map[string]reflect.Method // registered methods - 注册的方法映射
}

// NewState 创建一个新的Luax实例
// 使用sync.Once确保实例只被创建一次（单例模式）
// 参数：
//
//	opts ...Option - 可选的Lua选项配置函数
//
// 返回值：
//
//	*Luax - 创建的Luax实例
func NewState(opts ...Option) *Luax {
	once.Do(func() {
		opt := lua.Options{}
		for _, o := range opts {
			o(&opt)
		}
		_luax = &Luax{L: lua.NewState(opt)}
		_luax.Fn = make(map[string]*lua.LFunction)
		_luax.watchedFiles = make(map[string]string)
		_luax.watchedDirs = make(map[string]struct{})
		_luax.lastReload = make(map[string]time.Time)
		_luax.stopWatch = make(chan struct{})
	})
	return _luax
}

func (l *Luax) LogMode(mode LogMode) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logMode = mode
}

func (l *Luax) debugf(format string, args ...any) {
	if l.logMode == LogModeDebug {
		log.Printf(format, args...)
	}
}

// Close 关闭Lua状态
// 释放Lua状态占用的资源
func (l *Luax) Close() {
	l.closeWatcher()
	l.L.Close()
}

// SetGlobal 注册全局变量到Lua环境
// 将Go结构体的方法注册为Lua全局函数
// 参数：
//
//	v ...any - 可变参数，要注册的Go值
func (l *Luax) SetGlobal(v ...any) {
	for _, v := range v {
		// 注册全局变量，获取ServiceFuncs实例
		ms := register_global(v)
		// 遍历所有方法，注册为Lua全局函数
		for _, m := range ms.M {
			// 获取Lua函数实例
			var i = getIns()
			// 绑定方法到Lua函数
			makeSum(&i, m, ms.V)
			// 设置为Lua全局函数
			l.L.SetGlobal(m.Name, l.L.NewFunction(i))
		}
	}
}

// SetFunction 注册Go函数到Lua环境
// 将Go函数注册为Lua全局函数
// 参数：
//
//	v ...any - 可变参数，要注册的Go函数
func (l *Luax) SetFunction(v ...any) {
	for _, v := range v {
		// 获取Lua函数实例
		var i = getIns()
		// 绑定Go函数到Lua函数
		make_fun(&i, v)
		// 获取函数指针，用于获取函数名称
		pc := reflect.ValueOf(v).Pointer()
		// 获取函数名称
		funcName := runtime.FuncForPC(pc).Name()
		// 提取函数名称（去掉包名部分）
		funcName = strings.Split(funcName, ".")[1]
		// 打印函数名称和指针
		l.debugf("funcName: %s %v", funcName, i)
		// 设置为Lua全局函数
		l.L.SetGlobal(funcName, l.L.NewFunction(i))
	}
}

// Module 注册Go结构体为Lua模块
// 将Go结构体的方法注册为Lua模块的函数
// 参数：
//
//	v ...any - 可变参数，要注册的Go值
func (l *Luax) Modules(v ...any) {
	for _, v := range v {
		k := reflect.ValueOf(v).Kind()
		if k == reflect.String {
			// 只处理string类型,为兼容旧版代码
			continue
		}
		// 注册全局变量，获取ServiceFuncs实例
		ms := register_global(v)
		// 转换方法为Lua函数映射
		lgfuncs := method_lgfunc(ms)
		// 创建Lua表并设置函数
		mod := l.L.SetFuncs(l.L.NewTable(), lgfuncs)
		// 获取Lua函数实例
		i := getIns()
		// 绑定模块到Lua函数
		make_mod(&i, mod)
		// 解析包路径，生成模块名称
		pkPath := strings.Split(ms.N, "/")
		pkName := pkPath[len(pkPath)-1]
		names := strings.Split(pkName, ".")
		names[len(names)-1] = strings.ToLower(names[len(names)-1])
		pkPath = pkPath[:len(pkPath)-1]
		pkPath = append(pkPath, names...)
		mname := strings.Join(pkPath, "/")
		// 打印预加载模块信息
		l.debugf("preload module: [%s]", mname)
		// 预加载模块到Lua环境
		l.L.PreloadModule(mname, i)
	}
}

func (l *Luax) Module(name string, v any) {
	// 注册全局变量，获取ServiceFuncs实例
	ms := register_global(v)
	// 转换方法为Lua函数映射
	lgfuncs := method_lgfunc(ms)
	// 创建Lua表并设置函数
	mod := l.L.SetFuncs(l.L.NewTable(), lgfuncs)
	// 获取Lua函数实例
	i := getIns()
	// 绑定模块到Lua函数
	make_mod(&i, mod)
	// 解析包路径，生成模块名称
	// 预加载模块到Lua环境
	l.L.PreloadModule(name, i)
}

// make_mod 创建模块加载函数
// 将Lua表绑定到一个函数，该函数返回模块表
// 参数：
//
//	fptr any - 函数指针，用于存储创建的函数
//	mod *lua.LTable - 模块表
func make_mod(fptr any, mod *lua.LTable) {
	// 检查fptr是否是指针类型
	fn := reflect.ValueOf(fptr)
	k := fn.Kind()
	if k == reflect.Pointer {
		fn = fn.Elem()
	}
	// 使用反射创建函数，该函数返回模块表
	res := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		// 获取Lua状态
		L := args[0].Interface().(*lua.LState)
		// 将模块表压入栈
		L.Push(mod)
		// 返回值数量为1
		return []reflect.Value{reflect.ValueOf(1)}
	})
	// 设置函数值
	fn.Set(res)
}

// method_lgfunc 将ServiceFuncs的方法转换为Lua函数映射
// 参数：
//
//	ms *ServiceFuncs - ServiceFuncs实例
//
// 返回值：
//
//	map[string]lua.LGFunction - Lua函数映射
func method_lgfunc(ms *ServiceFuncs) map[string]lua.LGFunction {
	lgfuncs := map[string]lua.LGFunction{}
	// 遍历所有方法，转换为Lua函数
	for _, m := range ms.M {
		// 获取Lua函数实例
		var i = getIns()
		// 绑定方法到Lua函数
		makeSum(&i, m, ms.V)
		// 添加到映射
		lgfuncs[m.Name] = i
	}
	return lgfuncs
}

// DoString 执行Lua代码字符串
// 参数：
//
//	code string - Lua代码字符串
//
// 返回值：
//
//	error - 执行过程中的错误
func (l *Luax) DoString(code string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.L.DoString(code)
}

// DoFile 执行Lua代码文件
// 参数：
//
//	filename string - Lua代码文件路径
//
// 返回值：
//
//	error - 执行过程中的错误
func (l *Luax) DoFile(filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.L.DoFile(filename)
}

func (l *Luax) LoadFile(filename string) (*lua.LFunction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadFileLocked(filename)
}

func (l *Luax) loadFileLocked(filename string) (*lua.LFunction, error) {
	fn, err := l.L.LoadFile(filename)
	if err != nil {
		return nil, err
	}
	modName := moduleNameFromFilename(filename)
	l.Fn[modName] = fn
	return fn, nil
}

func (l *Luax) LoadDir(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadDirLocked(path)
}

func (l *Luax) loadDirLocked(dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".lua" {
			continue
		}
		filename := filepath.Join(dir, file.Name())
		if _, err := l.loadFileLocked(filename); err != nil {
			return err
		}
	}
	return nil
}

func (l *Luax) LoadAndWatchFile(filename string) error {
	_, err := l.LoadFile(filename)
	if err != nil {
		return err
	}
	return l.WatchFile(filename)
}

func (l *Luax) LoadAndWatchDir(dir string) error {
	err := l.LoadDir(dir)
	if err != nil {
		return err
	}
	return l.WatchDir(dir)
}

func (l *Luax) LoadString(m string, code string) (*lua.LFunction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadStringLocked(m, code)
}

func (l *Luax) loadStringLocked(m string, code string) (*lua.LFunction, error) {
	fn, err := l.L.LoadString(code)
	if err != nil {
		return nil, err
	}
	l.Fn[m] = fn
	return fn, nil
}

func (l *Luax) WatchFile(filename string) error {
	absFile, err := filepath.Abs(filename)
	if err != nil {
		return err
	}
	if filepath.Ext(absFile) != ".lua" {
		return fmt.Errorf("watch file only supports .lua files: %s", filename)
	}

	if err := l.ensureWatcher(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := l.loadFileLocked(absFile); err != nil {
		return err
	}
	l.watchedFiles[absFile] = moduleNameFromFilename(absFile)

	dir := filepath.Dir(absFile)
	if _, ok := l.watchedDirs[dir]; ok {
		return nil
	}
	if err := l.watcher.Add(dir); err != nil {
		return err
	}
	l.watchedDirs[dir] = struct{}{}
	return nil
}

func (l *Luax) WatchDir(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := l.ensureWatcher(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.loadDirLocked(absDir); err != nil {
		return err
	}

	files, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".lua" {
			continue
		}
		fullPath := filepath.Join(absDir, file.Name())
		l.watchedFiles[fullPath] = moduleNameFromFilename(fullPath)
	}

	if _, ok := l.watchedDirs[absDir]; ok {
		return nil
	}
	if err := l.watcher.Add(absDir); err != nil {
		return err
	}
	l.watchedDirs[absDir] = struct{}{}
	return nil
}

func (l *Luax) ensureWatcher() error {
	var err error
	l.watchOnce.Do(func() {
		var watcher *fsnotify.Watcher
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			return
		}
		l.watcher = watcher
		go l.watchLoop()
	})
	return err
}

func (l *Luax) watchLoop() {
	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			l.handleWatchEvent(event)
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("lua watcher error: %v", err)
		case <-l.stopWatch:
			return
		}
	}
}

func (l *Luax) handleWatchEvent(event fsnotify.Event) {
	if filepath.Ext(event.Name) != ".lua" {
		return
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}

	fullPath, err := filepath.Abs(event.Name)
	if err != nil {
		log.Printf("lua watcher path error: %v", err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Dir(fullPath)
	if _, watchingDir := l.watchedDirs[dir]; watchingDir {
		l.watchedFiles[fullPath] = moduleNameFromFilename(fullPath)
	}

	if _, ok := l.watchedFiles[fullPath]; !ok {
		return
	}

	now := time.Now()
	if last, ok := l.lastReload[fullPath]; ok && now.Sub(last) < reloadDebounce {
		return
	}

	if _, err := l.loadFileLocked(fullPath); err != nil {
		log.Printf("reload lua file %s error: %v", fullPath, err)
		return
	}
	l.lastReload[fullPath] = now
	l.debugf("reloaded lua file: %s", fullPath)
}

func (l *Luax) closeWatcher() {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-l.stopWatch:
	default:
		close(l.stopWatch)
	}

	if l.watcher != nil {
		_ = l.watcher.Close()
		l.watcher = nil
	}
}

func (l *Luax) Call(mn string, args ...string) (string, error) {
	res, err := l.CallN(mn, 1, args...)
	if err != nil {
		return "", err
	}
	if len(res) < 1 {
		return "", fmt.Errorf("call %s returned %d values, want at least 1", mn, len(res))
	}
	return res[0], nil
}
func (l *Luax) Call2(mn string, args ...string) (string, string, error) {
	res, err := l.CallN(mn, 2, args...)
	if err != nil {
		return "", "", err
	}
	if len(res) < 2 {
		return "", "", fmt.Errorf("call %s returned %d values, want at least 2", mn, len(res))
	}
	return res[0], res[1], nil
}

// Call 调用Lua函数
// 参数：
//
//	mn string - Lua函数名，格式为"模块名.函数名"
//	nret int - 返回值数量
//	args ...string - 可变参数，要传递给Lua函数的参数
//
// 返回值：
//
//	string - Lua函数返回的字符串
//	error - 调用过程中的错误
func (l *Luax) CallN(mn string, nret int, args ...string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// m: module name
	ns := strings.Split(mn, ".")
	if len(ns) != 2 {
		return nil, fmt.Errorf("invalid lua function name %q, want module.function", mn)
	}
	modname := ns[0]
	method := ns[1]

	fn, ok := l.Fn[modname]
	if !ok {
		return nil, fmt.Errorf("module not found: %s", modname)
	}
	l.L.Push(fn)
	l.L.Call(0, 1)
	mod := l.L.Get(-1)
	l.L.Pop(1)
	modTable, ok := mod.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("%s.lua did not return a module table", modname)
	}

	testFn := l.L.GetField(modTable, method)
	if testFn.Type() != lua.LTFunction {
		return nil, fmt.Errorf("%s.%s is not a function", modname, method)
	}
	// 转换参数为Lua值
	params := []lua.LValue{}
	for _, arg := range args {
		params = append(params, lua.LString(arg))
	}

	if err := l.L.CallByParam(lua.P{
		Fn:      testFn,
		NRet:    nret,
		Protect: true,
	}, params...); err != nil {
		fmt.Println("call ", modname+"."+method+" error:", err)
		return nil, err
	}

	// 读N个返回值
	rst := []string{}
	for i := 1; i <= nret; i++ {
		rst = append(rst, l.L.Get(-nret+i-1).String())
	}
	l.L.Pop(nret)
	return rst, nil
}

func moduleNameFromFilename(filename string) string {
	filename = strings.TrimSuffix(filename, ".lua")
	return filepath.Base(filename)
}

// make_fun 将Go函数绑定到Lua函数
// 创建一个Lua函数，该函数调用指定的Go函数
// 参数：
//
//	fptr any - 函数指针，用于存储创建的函数
//	f any - Go函数
func make_fun(fptr any, f any) {
	// 检查fptr是否是指针类型
	fn := reflect.ValueOf(fptr)
	k := fn.Kind()
	if k == reflect.Pointer {
		fn = fn.Elem()
	}
	// 使用反射创建函数，该函数调用指定的Go函数
	res := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		// 获取Lua状态
		L := args[0].Interface().(*lua.LState)
		// 获取Go函数的反射值和类型
		vn := reflect.ValueOf(f)
		tn := reflect.TypeOf(f)
		// 准备参数
		params := []reflect.Value{}
		// 遍历所有输入参数，从Lua获取参数值
		for i := 0; i < tn.NumIn(); i++ {
			n := tn.In(i)
			isPtr := n.Kind() == reflect.Pointer
			if isPtr {
				n = n.Elem()
			}
			// lua索引从1开始
			params = append(params, getParam(L, n.Kind(), i+1))
		}
		// 调用Go函数，获取返回值
		rst := vn.Call(params)
		// 遍历所有返回值，转换为Lua值并压入栈
		for _, v := range rst {
			L.Push(getResult(v))
		}
		// 返回值数量
		return []reflect.Value{reflect.ValueOf(len(rst))}
	})
	// 设置函数值
	fn.Set(res)
}

// makeSum 将Go方法绑定到Lua函数
// 创建一个Lua函数，该函数调用指定的Go方法
// 参数：
//
//	fptr any - 函数指针，用于存储创建的函数
//	m reflect.Method - Go方法
//	v reflect.Value - 方法接收器
func makeSum(fptr any, m reflect.Method, v reflect.Value) {
	// 检查fptr是否是指针类型
	fn := reflect.ValueOf(fptr)
	k := fn.Kind()
	if k == reflect.Pointer {
		fn = fn.Elem()
	}
	// 使用反射创建函数，该函数调用指定的Go方法
	res := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		// 获取Lua状态
		L := args[0].Interface().(*lua.LState)
		// 准备参数，第一个参数是接收器
		params := []reflect.Value{v}
		// 遍历所有输入参数（从1开始，因为0是接收器），从Lua获取参数值
		for i := 1; i < m.Type.NumIn(); i++ {
			n := m.Type.In(i)
			isPtr := n.Kind() == reflect.Pointer
			if isPtr {
				n = n.Elem()
			}
			// 添加参数
			params = append(params, getParam(L, n.Kind(), i))
		}
		// 调用Go方法，获取返回值
		rst := m.Func.Call(params)
		// 遍历所有返回值，转换为Lua值并压入栈
		for _, v := range rst {
			L.Push(getResult(v))
		}
		// 返回值数量
		return []reflect.Value{reflect.ValueOf(len(rst))}
	})
	// 设置函数值
	fn.Set(res)
}

// getResult 将Go值转换为Lua值
// 根据Go值的类型，转换为对应的Lua值
// 参数：
//
//	v reflect.Value - Go值的反射值
//
// 返回值：
//
//	lua.LValue - 转换后的Lua值
func getResult(v reflect.Value) lua.LValue {
	switch v.Kind() {
	case reflect.Int:
		// 转换int为lua.LNumber
		return lua.LNumber(v.Interface().(int))
	case reflect.String:
		// 转换string为lua.LString
		return lua.LString(v.Interface().(string))
	default:
		// 不支持的类型，抛出异常
		panic("unsupported type")
	}
}

// getParam 从Lua获取参数值，转换为Go值
// 根据指定的类型，从Lua获取参数值并转换为对应的Go值
// 参数：
//
//	L *lua.LState - Lua状态
//	k reflect.Kind - Go类型
//	pos int - Lua参数位置（从1开始）
//
// 返回值：
//
//	reflect.Value - 转换后的Go值的反射值
func getParam(L *lua.LState, k reflect.Kind, pos int) reflect.Value {
	switch k {
	case reflect.Int:
		// 从Lua获取int值
		return reflect.ValueOf(L.ToInt(pos))
	case reflect.String:
		// 从Lua获取string值
		return reflect.ValueOf(L.ToString(pos))
	default:
		// 不支持的类型，抛出异常
		panic("unsupported type")
	}
}

// getIns 获取一个空的Lua函数实例
// 返回值：
//
//	lua.LGFunction - 空的Lua函数
func getIns() lua.LGFunction {
	var l func(*lua.LState) int
	return l
}

// register_global 注册全局变量
// 创建一个ServiceFuncs实例，存储服务的名称、接收器和方法
// 参数：
//
//	rcvr any - 要注册的Go值
//
// 返回值：
//
//	*ServiceFuncs - 创建的ServiceFuncs实例
func register_global(rcvr any) *ServiceFuncs {
	service := new(ServiceFuncs)
	// 获取类型和值
	getType := reflect.TypeOf(rcvr)
	service.V = reflect.ValueOf(rcvr)
	k := getType.Kind()
	// 处理指针类型
	if k == reflect.Pointer {
		el := getType.Elem()
		// 生成服务名称：包路径.类型名称
		sname := fmt.Sprintf("%s.%s", el.PkgPath(), el.Name())
		service.N = sname
	} else {
		// 生成服务名称：包路径.类型名称
		sname := fmt.Sprintf("%s.%s", getType.PkgPath(), getType.Name())
		service.N = sname
	}
	// 安装方法
	service.M = suitableMethods(getType)
	return service
}

// suitableMethods 获取类型的所有导出方法
// 遍历类型的所有方法，过滤出导出的方法（首字母大写）
// 参数：
//
//	typ reflect.Type - 类型
//
// 返回值：
//
//	map[string]reflect.Method - 导出方法的映射
func suitableMethods(typ reflect.Type) map[string]reflect.Method {
	methods := make(map[string]reflect.Method)
	// 遍历所有方法
	for m := 0; m < typ.NumMethod(); m++ {
		m := typ.Method(m)
		// 跳过非导出方法（有包路径的方法）
		if m.PkgPath != "" {
			continue
		}
		// 跳过非导出方法（首字母小写）
		if !m.IsExported() {
			continue
		}
		// 添加到映射
		methods[m.Name] = m
	}
	return methods
}
