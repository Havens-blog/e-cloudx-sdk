package aliyun

import (
	"os"
	"strings"
	"testing"
)

// TestK8sEventTargetsUsesPaginatedProjectList 源码级守卫：k8sEventTargets
// 不得再出现裸 ListProject()（不带 offset/size，服务端只回首页，静默丢
// 排在后面的 k8s-log-* project）。裸调用发生在传给 logstoreEnumCache.get
// 的闭包里，无法用行为测试直接覆盖，用源码扫描钉住接线。
func TestK8sEventTargetsUsesPaginatedProjectList(t *testing.T) {
	raw, err := os.ReadFile("provider.go") //nolint:gosec // G304: 测试内读取本包源码做接线守卫
	if err != nil {
		t.Fatalf("read provider.go: %v", err)
	}
	src := string(raw)
	if strings.Contains(src, ".ListProject()") {
		t.Fatal("provider.go 仍存在裸 ListProject() 调用：SLS 只回首页，k8s-log-* project 会被静默漏掉，应改用 listAllProjectNames（project_page.go）")
	}
	if !strings.Contains(src, "listAllProjectNames(p.clientFor(region))") {
		t.Fatal("k8sEventTargets 未接入 listAllProjectNames 分页枚举")
	}
}
