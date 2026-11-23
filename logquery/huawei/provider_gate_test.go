package huawei

import (
	"os"
	"strings"
	"testing"
)

// TestWafElbListStreamsGoThroughGate 源码级守卫:WAF/ELB 路径枚举流清单必须走
// listGroupStreams(= withLTSRetry 闸门 + 限流退避),不得直接裸调
// p.lts().ListLogStreams。组数由账号实际命名动态发现(2026-11-19 实测 3 个 ELB
// 组,此前固定 2 个),裸调在并发枚举时撞 LTS ~10 QPS 限流,被限流只 Warn 跳过
// 整组,组内流会静默从源清单/查询/聚合三处消失(OpenCodeReview PR#20 复审问题 #3)。
func TestWafElbListStreamsGoThroughGate(t *testing.T) {
	raw, err := os.ReadFile("provider.go")
	if err != nil {
		t.Fatalf("read provider.go: %v", err)
	}
	src := string(raw)

	if strings.Contains(src, "LogGroupName: &src.group") {
		t.Error("provider.go 仍存在裸调 p.lts().ListLogStreams{LogGroupName: &src.group};" +
			" WAF/ELB 路径必须经 listGroupStreams 走 LTS 限流闸门")
	}
	// 三个枚举点都必须经闸门：listSourcesUncached / Search 走 listGroupStreams，
	// Aggregate 走同闸门的 listGroupStreamsWith（要注入调用方的 api，见其注释）。
	gated := strings.Count(src, "p.listGroupStreams(ctx, src.group)") +
		strings.Count(src, "p.listGroupStreamsWith(ctx, api, src.group)")
	if gated != 3 {
		t.Errorf("经闸门的组流枚举调用点 = %d, want 3(listSourcesUncached/Search/Aggregate)", gated)
	}
	idx := strings.Index(src, "func (p *provider) listGroupStreamsWith(")
	if idx < 0 {
		t.Fatal("provider.go 缺少 listGroupStreamsWith")
	}
	body := src[idx:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "p.withLTSRetry(ctx,") {
		t.Error("listGroupStreamsWith 未走 withLTSRetry,限流重试失效")
	}
	// 外层函数必须委托给注入版，否则两条路径的闸门语义会分叉。
	if !strings.Contains(src, "return p.listGroupStreamsWith(ctx, p.lts(), group)") {
		t.Error("listGroupStreams 未委托给 listGroupStreamsWith")
	}
}
