package aliyun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryAccessKeyClientForcesHTTPS 是这轮修复的棘轮守卫：
// 任何用 NewClientWithAccessKey 构造阿里云客户端的文件，都必须调用
// aliyunhttps.Force 修正默认协议，否则该客户端的请求会落到明文 HTTP
// （SDK 的 config.Scheme 默认是 "HTTP"）。
//
// 新增适配器时如果忘了 Force，这条用例会点名文件失败。
func TestEveryAccessKeyClientForcesHTTPS(t *testing.T) {
	root := ".."
	dirs := []string{
		filepath.Join(root, "aliyun"),
		filepath.Join(root, "common", "aliyun"),
		filepath.Join(root, "billing", "aliyun"),
	}

	var offenders []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("读取目录 %s 失败: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			content, err := os.ReadFile(path) //nolint:gosec // 只读本仓库源码
			if err != nil {
				t.Fatalf("读取 %s 失败: %v", path, err)
			}
			ctors, forces := countSchemeCalls(string(content))
			if ctors > forces {
				offenders = append(offenders, path+": NewClientWithAccessKey("+
					itoa(ctors)+") 但只有 aliyunhttps.Force("+itoa(forces)+")")
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("下列文件构造了阿里云客户端但没有强制 HTTPS：\n%s", strings.Join(offenders, "\n"))
	}
}

// countSchemeCalls 统计构造器与 Force 调用次数，忽略整行注释。
func countSchemeCalls(content string) (ctors int, forces int) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		ctors += strings.Count(trimmed, "NewClientWithAccessKey(")
		forces += strings.Count(trimmed, "aliyunhttps.Force(")
	}
	return ctors, forces
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
