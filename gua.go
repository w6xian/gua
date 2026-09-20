// Package gua 提供了与 Lua 脚本交互的能力，基于 gopher-lua 实现。
//
// 稳定性约定：
//   - 所有对外方法都持有内部互斥锁，同一个 Luax 可以被多个 goroutine 并发使用；
//   - Lua 状态关闭后，所有对外方法返回 ErrClosed（或忽略），不会 panic；
//   - 注册到 Lua 的 Go 函数/方法内部发生的 panic 会被转换为 Lua 错误，不会拖垮宿主进程；
//   - Go 与 Lua 之间的值转换支持 bool/整数/浮点/string/slice/map/struct/指针，
//     不支持的类型返回错误而不是 panic。
//
// 使用注意：
//   - 不要在注册给 Lua 的 Go 函数内部回调同一个 Luax（互斥锁不可重入，会死锁），
//     需要回调时请直接使用函数入参中的 *lua.LState；
//   - 直接读写导出字段 L 不会加锁，调用方需自行保证串行。
package gua

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	lua "github.com/yuin/gopher-lua"
)

// ErrClosed 表示 Lua 状态已经关闭，不能再使用
var ErrClosed = errors.New("lua state is closed")

// reloadDebounce 文件监听的防抖间隔，避免编辑器多次写入导致重复重载
const reloadDebounce = 200 * time.Millisecond

// LogMode 日志级别
type LogMode int

const (
	// LogModeSilent 静默模式（默认）
	LogModeSilent LogMode = iota
	// LogModeDebug 调试模式，输出注册与重载等调试日志
	LogModeDebug
)

// Luax Lua 执行环境的封装，是 goroutine 安全的。
type Luax struct {
	L  *lua.LState               // Lua 状态实例
	Fn map[string]*lua.LFunction // 已加载的 Lua 模块（模块名 -> 入口函数）

	logMode atomic.Int32

	mu        sync.Mutex // 保护 L、Fn 以及监听相关的所有状态
	closed    bool
	closeOnce sync.Once

	watcher      *fsnotify.Watcher
	watchedFiles map[string]string    // 文件绝对路径 -> 模块名
	watchedDirs  map[string]struct{}  // 已监听的目录
	lastReload   map[string]time.Time // 防抖用的上次重载时间
	stopWatch    chan struct{}        // 关闭监听循环的信号
}

// ServiceFuncs 服务描述：名称、接收器与导出方法
type ServiceFuncs struct {
	N string                    // name of service - 服务名称（包路径.类型名）
	V reflect.Value             // receiver of methods for the service - 方法接收器
	M map[string]reflect.Method // registered methods - 导出的方法
}

var (
	stateMu sync.Mutex
	_luax   *Luax
)

// New 创建一个全新的 Luax 实例（非单例）。
// 需要多个相互隔离的 Lua 环境时使用它。
func New(opts ...Option) *Luax {
	opt := lua.Options{}
	for _, o := range opts {
		if o != nil {
			o(&opt)
		}
	}
	return &Luax{
		L:            lua.NewState(opt),
		Fn:           make(map[string]*lua.LFunction),
		watchedFiles: make(map[string]string),
		watchedDirs:  make(map[string]struct{}),
		lastReload:   make(map[string]time.Time),
		stopWatch:    make(chan struct{}),
	}
}

// NewState 返回进程内共享的 Luax 实例（单例）。
// 首次调用时按 opts 创建；实例被 Close 后再次调用会重新创建，避免拿到已关闭的状态。
// 并发调用是安全的。
func NewState(opts ...Option) *Luax {
	stateMu.Lock()
	defer stateMu.Unlock()
	if _luax == nil || _luax.Closed() {
		_luax = New(opts...)
	}
	return _luax
}

// LogMode 设置日志级别
func (l *Luax) LogMode(mode LogMode) {
	l.logMode.Store(int32(mode))
}

// Closed 返回 Lua 状态是否已经关闭
func (l *Luax) Closed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// Close 关闭 Lua 状态并停止文件监听，可重复调用且不会 panic。
func (l *Luax) Close() {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.closed = true
		l.stopWatcherLocked()
		if l.L != nil && !l.L.IsClosed() {
			l.L.Close()
		}
	})
}

// SetGlobal 把 Go 值（通常是指针）的导出方法注册为 Lua 全局函数。
// 注册失败的值会被跳过并记录日志，不影响其他值。
func (l *Luax) SetGlobal(v ...any) {
	l.report(l.exec("SetGlobal", func() error { return l.setGlobalLocked(v) }))
}

// SetFunction 把 Go 函数注册为 Lua 全局函数，函数名由函数签名推导。
func (l *Luax) SetFunction(v ...any) {
	l.report(l.exec("SetFunction", func() error { return l.setFunctionLocked(v) }))
}

// Modules 把 Go 值的导出方法注册为 Lua 模块，模块名由包路径与类型名推导，
// 可在 Lua 中通过 require 调用。为兼容旧版本，字符串参数会被忽略。
func (l *Luax) Modules(v ...any) {
	l.report(l.exec("Modules", func() error { return l.modulesLocked(v) }))
}

// Module 以指定名称注册 Lua 模块，可在 Lua 中通过 require(name) 调用。
func (l *Luax) Module(name string, v any) {
	l.report(l.exec("Module", func() error {
		if strings.TrimSpace(name) == "" {
			return errors.New("module name is empty")
		}
		svc, err := newService(v)
		if err != nil {
			return err
		}
		return l.preloadModuleLocked(name, svc)
	}))
}

// DoString 执行 Lua 代码字符串
func (l *Luax) DoString(code string) error {
	return l.exec("DoString", func() error { return l.runScript(l.L.DoString, code) })
}

// DoFile 执行 Lua 文件
func (l *Luax) DoFile(filename string) error {
	return l.exec("DoFile", func() error { return l.runScript(l.L.DoFile, filename) })
}

// runScript 执行 DoString/DoFile，并保证执行后 Lua 栈回到执行前的高度。
// gopher-lua 的 DoString/DoFile 内部使用 PCall(0, MultRet)，脚本的返回值会残留在栈上，
// 反复调用会不断抬高栈顶，最终触发 registry overflow。
func (l *Luax) runScript(do func(string) error, src string) error {
	top := l.L.GetTop()
	err := do(src)
	if cur := l.L.GetTop(); cur > top {
		l.L.Pop(cur - top)
	}
	return err
}

// LoadFile 加载 Lua 模块文件（文件需要 return 一个模块表），
// 以文件名（去掉 .lua 后缀）作为模块名注册，供 Call/Call2/CallN 使用。
func (l *Luax) LoadFile(filename string) (fn *lua.LFunction, err error) {
	err = l.exec("LoadFile", func() error {
		var e error
		fn, e = l.loadFileLocked(filename)
		return e
	})
	return fn, err
}

// LoadDir 加载目录下的所有 .lua 模块文件（不递归子目录）
func (l *Luax) LoadDir(path string) error {
	return l.exec("LoadDir", func() error { return l.loadDirLocked(path) })
}

// LoadString 以模块名 m 注册一段 Lua 代码（代码需要 return 一个模块表）
func (l *Luax) LoadString(m string, code string) (fn *lua.LFunction, err error) {
	err = l.exec("LoadString", func() error {
		var e error
		fn, e = l.loadStringLocked(m, code)
		return e
	})
	return fn, err
}

// LoadAndWatchFile 加载 Lua 文件并监听其变化，变化后自动重载
func (l *Luax) LoadAndWatchFile(filename string) error {
	if _, err := l.LoadFile(filename); err != nil {
		return err
	}
	return l.WatchFile(filename)
}

// LoadAndWatchDir 加载目录下的 Lua 文件并监听目录变化，变化后自动重载
func (l *Luax) LoadAndWatchDir(dir string) error {
	if err := l.LoadDir(dir); err != nil {
		return err
	}
	return l.WatchDir(dir)
}

// WatchFile 监听单个 .lua 文件，文件变化后自动重载
func (l *Luax) WatchFile(filename string) error {
	absFile, err := filepath.Abs(filename)
	if err != nil {
		return fmt.Errorf("gua: WatchFile: %w", err)
	}
	if strings.ToLower(filepath.Ext(absFile)) != ".lua" {
		return fmt.Errorf("gua: WatchFile: only .lua files are supported: %s", filename)
	}
	return l.exec("WatchFile", func() error { return l.watchFileLocked(absFile) })
}

// WatchDir 监听目录下的 .lua 文件，文件变化后自动重载
func (l *Luax) WatchDir(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("gua: WatchDir: %w", err)
	}
	return l.exec("WatchDir", func() error { return l.watchDirLocked(absDir) })
}

// Call 调用 Lua 模块函数并返回 1 个字符串结果
func (l *Luax) Call(mn string, args ...string) (string, error) {
	res, err := l.CallN(mn, 1, args...)
	if err != nil {
		return "", err
	}
	if len(res) < 1 {
		return "", fmt.Errorf("gua: call %s returned %d values, want at least 1", mn, len(res))
	}
	return res[0], nil
}

// Call2 调用 Lua 模块函数并返回 2 个字符串结果
func (l *Luax) Call2(mn string, args ...string) (string, string, error) {
	res, err := l.CallN(mn, 2, args...)
	if err != nil {
		return "", "", err
	}
	if len(res) < 2 {
		return "", "", fmt.Errorf("gua: call %s returned %d values, want at least 2", mn, len(res))
	}
	return res[0], res[1], nil
}

// CallN 调用 Lua 模块函数 "模块名.函数名" 并返回 nret 个字符串结果。
// 模块表每次调用都会重新执行，因此文件监听重载后能立即生效。
func (l *Luax) CallN(mn string, nret int, args ...string) (ret []string, err error) {
	if nret < 0 {
		return nil, fmt.Errorf("gua: CallN: negative nret %d", nret)
	}
	err = l.exec("CallN", func() error {
		var e error
		ret, e = l.callLocked(mn, nret, args)
		return e
	})
	return ret, err
}

/* ------------------------------ 内部实现 ------------------------------ */

// exec 是所有对外操作的统一入口：加锁 + 关闭检查 + panic 恢复
func (l *Luax) exec(name string, fn func() error) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.L == nil || l.L.IsClosed() {
		return fmt.Errorf("%w (%s)", ErrClosed, name)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gua: %s: recovered panic: %v", name, r)
		}
	}()
	if err := fn(); err != nil {
		return fmt.Errorf("gua: %w", err)
	}
	return nil
}

// report 记录无返回值的注册方法的错误
func (l *Luax) report(err error) {
	if err != nil {
		l.warnf("%v", err)
	}
}

func (l *Luax) warnf(format string, args ...any) {
	log.Printf(format, args...)
}

func (l *Luax) debugf(format string, args ...any) {
	if LogMode(l.logMode.Load()) == LogModeDebug {
		log.Printf("gua: "+format, args...)
	}
}

func (l *Luax) setGlobalLocked(vs []any) error {
	var errs []error
	for _, v := range vs {
		svc, err := newService(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SetGlobal: %w", err))
			continue
		}
		for _, m := range svc.M {
			fn, err := bindMethod(svc.V, m)
			if err != nil {
				errs = append(errs, fmt.Errorf("SetGlobal: %w", err))
				continue
			}
			l.L.SetGlobal(m.Name, l.L.NewFunction(fn))
			l.debugf("set global function: %s", m.Name)
		}
	}
	return errors.Join(errs...)
}

func (l *Luax) setFunctionLocked(vs []any) error {
	var errs []error
	for _, v := range vs {
		rv := reflect.ValueOf(v)
		name, err := funcName(rv)
		if err != nil {
			errs = append(errs, fmt.Errorf("SetFunction: %w", err))
			continue
		}
		fn, err := bindFunc(name, rv)
		if err != nil {
			errs = append(errs, fmt.Errorf("SetFunction: %w", err))
			continue
		}
		l.L.SetGlobal(name, l.L.NewFunction(fn))
		l.debugf("set global function: %s", name)
	}
	return errors.Join(errs...)
}

func (l *Luax) modulesLocked(vs []any) error {
	var errs []error
	for _, v := range vs {
		rv := reflect.ValueOf(v)
		if rv.IsValid() && rv.Kind() == reflect.String {
			continue // 兼容旧版代码：字符串参数忽略
		}
		svc, err := newService(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("Modules: %w", err))
			continue
		}
		if err := l.preloadModuleLocked(moduleNameFromType(svc.N), svc); err != nil {
			errs = append(errs, fmt.Errorf("Modules: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (l *Luax) preloadModuleLocked(name string, svc *ServiceFuncs) error {
	funcs := make(map[string]lua.LGFunction, len(svc.M))
	var errs []error
	for _, m := range svc.M {
		fn, err := bindMethod(svc.V, m)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		funcs[m.Name] = fn
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("module %s: %w", name, err)
	}
	mod := l.L.SetFuncs(l.L.NewTable(), funcs)
	l.L.PreloadModule(name, moduleLoader(mod))
	l.debugf("preload module: [%s]", name)
	return nil
}

func (l *Luax) loadFileLocked(filename string) (*lua.LFunction, error) {
	fn, err := l.L.LoadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", filename, err)
	}
	modName := moduleNameFromFilename(filename)
	l.Fn[modName] = fn
	l.debugf("loaded lua module: %s (%s)", modName, filename)
	return fn, nil
}

func (l *Luax) loadDirLocked(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var errs []error
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".lua" {
			continue
		}
		if _, err := l.loadFileLocked(filepath.Join(dir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (l *Luax) loadStringLocked(m string, code string) (*lua.LFunction, error) {
	if strings.TrimSpace(m) == "" {
		return nil, errors.New("module name is empty")
	}
	fn, err := l.L.LoadString(code)
	if err != nil {
		return nil, fmt.Errorf("load module %s: %w", m, err)
	}
	l.Fn[m] = fn
	return fn, nil
}

func (l *Luax) callLocked(mn string, nret int, args []string) ([]string, error) {
	ns := strings.Split(mn, ".")
	if len(ns) != 2 || ns[0] == "" || ns[1] == "" {
		return nil, fmt.Errorf("invalid lua function name %q, want module.function", mn)
	}
	modname, method := ns[0], ns[1]

	fn, ok := l.Fn[modname]
	if !ok {
		return nil, fmt.Errorf("module not found: %s", modname)
	}

	// 执行模块入口，取到模块表（保护调用，Lua 报错不会导致进程崩溃）
	if err := l.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}); err != nil {
		return nil, fmt.Errorf("load module %s: %w", modname, err)
	}
	mod := l.L.Get(-1)
	l.L.Pop(1)
	modTable, ok := mod.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("%s did not return a module table", modname)
	}

	target := l.L.GetField(modTable, method)
	if target.Type() != lua.LTFunction {
		return nil, fmt.Errorf("%s.%s is not a function", modname, method)
	}

	params := make([]lua.LValue, 0, len(args))
	for _, arg := range args {
		params = append(params, lua.LString(arg))
	}
	if err := l.L.CallByParam(lua.P{Fn: target, NRet: nret, Protect: true}, params...); err != nil {
		return nil, fmt.Errorf("call %s.%s: %w", modname, method, err)
	}

	rst := make([]string, 0, nret)
	for i := 1; i <= nret; i++ {
		rst = append(rst, l.L.Get(-nret+i-1).String())
	}
	l.L.Pop(nret)
	return rst, nil
}

/* ------------------------------ 文件监听 ------------------------------ */

func (l *Luax) watchFileLocked(absFile string) error {
	if err := l.ensureWatcherLocked(); err != nil {
		return err
	}
	if _, err := l.loadFileLocked(absFile); err != nil {
		return err
	}
	l.watchedFiles[absFile] = moduleNameFromFilename(absFile)

	dir := filepath.Dir(absFile)
	if _, ok := l.watchedDirs[dir]; ok {
		return nil
	}
	if err := l.watcher.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}
	l.watchedDirs[dir] = struct{}{}
	return nil
}

func (l *Luax) watchDirLocked(absDir string) error {
	if err := l.ensureWatcherLocked(); err != nil {
		return err
	}
	if err := l.loadDirLocked(absDir); err != nil {
		return err
	}

	files, err := os.ReadDir(absDir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", absDir, err)
	}
	for _, file := range files {
		if file.IsDir() || strings.ToLower(filepath.Ext(file.Name())) != ".lua" {
			continue
		}
		fullPath := filepath.Join(absDir, file.Name())
		l.watchedFiles[fullPath] = moduleNameFromFilename(fullPath)
	}

	if _, ok := l.watchedDirs[absDir]; ok {
		return nil
	}
	if err := l.watcher.Add(absDir); err != nil {
		return fmt.Errorf("watch %s: %w", absDir, err)
	}
	l.watchedDirs[absDir] = struct{}{}
	return nil
}

// ensureWatcherLocked 创建监听实例，创建失败不会留下半初始化状态
func (l *Luax) ensureWatcherLocked() error {
	if l.watcher != nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	l.watcher = w
	go l.watchLoop(w)
	return nil
}

func (l *Luax) watchLoop(w *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			l.handleWatchEvent(event)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			l.warnf("gua: watcher error: %v", err)
		case <-l.stopWatch:
			return
		}
	}
}

func (l *Luax) handleWatchEvent(event fsnotify.Event) {
	if strings.ToLower(filepath.Ext(event.Name)) != ".lua" {
		return
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}

	fullPath, err := filepath.Abs(event.Name)
	if err != nil {
		l.warnf("gua: watcher path error: %v", err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

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
		l.warnf("gua: reload %s error: %v", fullPath, err)
		return
	}
	l.lastReload[fullPath] = now
	l.debugf("reloaded lua file: %s", fullPath)
}

// stopWatcherLocked 停止监听循环并释放资源
func (l *Luax) stopWatcherLocked() {
	select {
	case <-l.stopWatch:
	default:
		close(l.stopWatch)
	}
	if l.watcher != nil {
		_ = l.watcher.Close()
		l.watcher = nil
	}
	l.watchedFiles = make(map[string]string)
	l.watchedDirs = make(map[string]struct{})
	l.lastReload = make(map[string]time.Time)
}

// moduleNameFromFilename 由文件名推导模块名（去掉目录与 .lua 后缀）
func moduleNameFromFilename(filename string) string {
	return filepath.Base(strings.TrimSuffix(filename, ".lua"))
}
