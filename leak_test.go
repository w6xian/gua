package gua

import "testing"

// 本文件同样只使用新旧两版都存在的 API，用于对比回归：
// gopher-lua 的 DoString/DoFile 会把脚本返回值残留在 Lua 栈上，
// 反复调用会一直抬高栈顶，直到报 registry overflow。
// 重构后 runScript 会在执行后恢复栈高度，本测试应当通过。
func TestDoStringStackNotLeaking(t *testing.T) {
	l := NewState(CallStackSize(256), RegistrySize(1024))
	const n = 2000
	for i := 0; i < n; i++ {
		if err := l.DoString("return 42"); err != nil {
			t.Fatalf("iteration %d/%d: %v", i, n, err)
		}
	}
	if top := l.L.GetTop(); top != 0 {
		t.Fatalf("lua stack top = %d after %d calls, want 0 (stack leaked)", top, n)
	}
}
