package gua

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// 本文件针对文件监听做单元测试。
// 除标记了“真实监听”的用例外，其余用例直接把文件登记到监听表里再手动投递事件，
// 这样 handleWatchEvent 的各条分支可以被确定性地触发，不受操作系统事件时序影响。

// writeLuaModule 写入一个返回模块表的 lua 文件，模块里 V() 返回 value
func writeLuaModule(t *testing.T, file, value string) {
	t.Helper()
	code := fmt.Sprintf("local m = {}\nfunction m.V() return %q end\nreturn m\n", value)
	if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

// registerWatched 手动把文件登记到监听表（不启动真实监听器）
func registerWatched(t *testing.T, l *Luax, file string, watchDir bool) string {
	t.Helper()
	abs, err := filepath.Abs(file)
	if err != nil {
		t.Fatalf("abs(%s): %v", file, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if watchDir {
		l.watchedDirs[filepath.Dir(abs)] = struct{}{}
	}
	l.watchedFiles[abs] = moduleNameFromFilename(abs)
	return abs
}

func TestHandleWatchEventReloadsWatchedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hw.lua")
	writeLuaModule(t, file, "v1")

	l := newLua(t)
	abs := registerWatched(t, l, file, true)
	if _, err := l.LoadFile(file); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	for _, op := range []fsnotify.Op{fsnotify.Write, fsnotify.Create, fsnotify.Rename} {
		value := "v-" + op.String()
		writeLuaModule(t, file, value)
		l.handleWatchEvent(fsnotify.Event{Name: abs, Op: op})
		got, err := l.Call("hw.V")
		if err != nil {
			t.Fatalf("Call after %s: %v", op, err)
		}
		if got != value {
			t.Fatalf("hw.V after %s = %q, want %q", op, got, value)
		}
		// 跳过防抖窗口
		time.Sleep(reloadDebounce + 100*time.Millisecond)
	}
}

func TestHandleWatchEventDebounce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "db.lua")
	writeLuaModule(t, file, "v1")

	l := newLua(t)
	abs := registerWatched(t, l, file, true)

	writeLuaModule(t, file, "v2")
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	// 防抖窗口内的第二次事件不应生效
	writeLuaModule(t, file, "v3")
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})
	if got, err := l.Call("db.V"); err != nil || got != "v2" {
		t.Fatalf("db.V = %q, err = %v, want v2 (debounced)", got, err)
	}

	// 超过防抖窗口后生效
	time.Sleep(reloadDebounce + 100*time.Millisecond)
	writeLuaModule(t, file, "v4")
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})
	if got, err := l.Call("db.V"); err != nil || got != "v4" {
		t.Fatalf("db.V = %q, err = %v, want v4", got, err)
	}
}

func TestHandleWatchEventIgnoresIrrelevantEvents(t *testing.T) {
	l := newLua(t)

	// 非 .lua 文件
	l.handleWatchEvent(fsnotify.Event{Name: "notes.txt", Op: fsnotify.Write})
	// 未登记的文件
	l.handleWatchEvent(fsnotify.Event{Name: "unwatched.lua", Op: fsnotify.Write})
	// 非 Write/Create/Rename 事件
	l.handleWatchEvent(fsnotify.Event{Name: "removed.lua", Op: fsnotify.Remove})

	if len(l.Fn) != 0 {
		t.Fatalf("Fn = %v, want empty", l.Fn)
	}
}

func TestHandleWatchEventIgnoresRemoveOpForWatchedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rm.lua")
	writeLuaModule(t, file, "v1")

	l := newLua(t)
	abs := registerWatched(t, l, file, true)
	if _, err := l.LoadFile(file); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	writeLuaModule(t, file, "v2")
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Remove})

	if got, err := l.Call("rm.V"); err != nil || got != "v1" {
		t.Fatalf("rm.V = %q, err = %v, want v1", got, err)
	}
}

func TestHandleWatchEventRegistersNewFileInWatchedDir(t *testing.T) {
	dir := t.TempDir()
	l := newLua(t)

	// 只登记目录，文件是后来新建的
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	l.mu.Lock()
	l.watchedDirs[absDir] = struct{}{}
	l.mu.Unlock()

	file := filepath.Join(dir, "new.lua")
	writeLuaModule(t, file, "n1")
	abs, err := filepath.Abs(file)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Create})

	if got, err := l.Call("new.V"); err != nil || got != "n1" {
		t.Fatalf("new.V = %q, err = %v, want n1", got, err)
	}
}

func TestHandleWatchEventReloadErrorKeepsOldModule(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bk.lua")
	writeLuaModule(t, file, "v1")

	l := newLua(t)
	abs := registerWatched(t, l, file, true)
	if _, err := l.LoadFile(file); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if err := os.WriteFile(file, []byte("return ("), 0o644); err != nil {
		t.Fatalf("write broken lua: %v", err)
	}
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	if got, err := l.Call("bk.V"); err != nil || got != "v1" {
		t.Fatalf("bk.V = %q, err = %v, want v1", got, err)
	}
}

func TestHandleWatchEventAfterClose(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ac.lua")
	writeLuaModule(t, file, "v1")

	l := New()
	abs := registerWatched(t, l, file, true)
	l.Close()

	// 关闭后的事件应当被直接丢弃，不 panic
	l.handleWatchEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})
	if len(l.Fn) != 0 {
		t.Fatalf("Fn = %v, want empty", l.Fn)
	}
}

/* ------------------------------ 真实监听 ------------------------------ */

func TestWatchFileMissingFile(t *testing.T) {
	l := newLua(t)
	if err := l.WatchFile(filepath.Join(t.TempDir(), "missing.lua")); err == nil {
		t.Fatal("watching a missing lua file should fail")
	}
}

func TestWatchDirIgnoresNonLuaAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeLuaModule(t, filepath.Join(dir, "a.lua"), "a")
	writeLuaModule(t, filepath.Join(dir, "b.lua"), "b")
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLuaModule(t, filepath.Join(sub, "d.lua"), "d")

	l := newLua(t)
	if err := l.WatchDir(dir); err != nil {
		t.Fatalf("WatchDir: %v", err)
	}

	for mod, want := range map[string]string{"a": "a", "b": "b"} {
		got, err := l.Call(mod + ".V")
		if err != nil {
			t.Fatalf("Call %s.V: %v", mod, err)
		}
		if got != want {
			t.Fatalf("%s.V = %q, want %q", mod, got, want)
		}
	}
	if _, err := l.Call("d.V"); err == nil {
		t.Fatal("sub directories should not be watched")
	}
	if _, ok := l.Fn["c"]; ok {
		t.Fatal("non .lua files should not be loaded")
	}
}

func TestWatchDirEmptyDir(t *testing.T) {
	l := newLua(t)
	if err := l.WatchDir(t.TempDir()); err != nil {
		t.Fatalf("WatchDir on an empty dir: %v", err)
	}
}

func TestLoadAndWatchFileReloads(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lw.lua")
	writeLuaModule(t, file, "v1")

	l := newLua(t)
	if err := l.LoadAndWatchFile(file); err != nil {
		t.Fatalf("LoadAndWatchFile: %v", err)
	}
	if got, err := l.Call("lw.V"); err != nil || got != "v1" {
		t.Fatalf("lw.V = %q, err = %v, want v1", got, err)
	}

	writeLuaModule(t, file, "v2")
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := l.Call("lw.V")
		if err != nil {
			t.Fatalf("Call after reload: %v", err)
		}
		if got == "v2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lua file was not reloaded, still %q", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatchFileThenWatchDirReusesWatcher(t *testing.T) {
	dir := t.TempDir()
	writeLuaModule(t, filepath.Join(dir, "x.lua"), "x")
	writeLuaModule(t, filepath.Join(dir, "y.lua"), "y")

	l := newLua(t)
	if err := l.WatchFile(filepath.Join(dir, "x.lua")); err != nil {
		t.Fatalf("WatchFile: %v", err)
	}
	if err := l.WatchDir(dir); err != nil {
		t.Fatalf("WatchDir: %v", err)
	}
	if err := l.WatchFile(filepath.Join(dir, "y.lua")); err != nil {
		t.Fatalf("WatchFile: %v", err)
	}

	// 监听器只创建一次
	l.mu.Lock()
	w := l.watcher
	l.mu.Unlock()
	if w == nil {
		t.Fatal("watcher should exist")
	}
	if err := l.WatchDir(dir); err != nil {
		t.Fatalf("WatchDir twice: %v", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watcher != w {
		t.Fatal("watcher should be reused")
	}
}
