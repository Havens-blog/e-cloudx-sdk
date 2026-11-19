package huawei

import (
	"reflect"
	"testing"
)

// TestWafELBGroups 组发现:从账号全部组名里挑出 WAF/ELB 形态的组,
// 已知组沿用 catalog 的展示名与备注,未知组用组名本身,结果按组名排序。
func TestWafELBGroups(t *testing.T) {
	ids := map[string]string{
		"hwyun-waf-logs":   "g1",
		"eda-prod-elb":     "g2",
		"dev-elb":          "g3",
		"eda-uat-elb":      "g4",
		"app-order":        "g5",
		"k8s-log-08810f4a": "g6",
		"lts-system":       "g7",
		"waf-service-logs": "g8",
	}
	got := wafELBGroups(ids)
	want := []hGroup{
		{group: "dev-elb", name: "dev-elb"},
		{group: "eda-prod-elb", name: "ELB", note: "ELB 访问日志(实时)"},
		{group: "eda-uat-elb", name: "eda-uat-elb"},
		{group: "hwyun-waf-logs", name: "WAF", note: "WAF 攻击+访问日志(实时)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wafELBGroups() = %#v, want %#v", got, want)
	}
}

// TestWafELBGroupsEmpty 无 WAF/ELB 组时返回空切片(不 panic,长度 0)。
func TestWafELBGroupsEmpty(t *testing.T) {
	if got := wafELBGroups(map[string]string{"app-order": "g1"}); len(got) != 0 {
		t.Fatalf("wafELBGroups() = %#v, want empty", got)
	}
	if got := wafELBGroups(nil); len(got) != 0 {
		t.Fatalf("wafELBGroups(nil) = %#v, want empty", got)
	}
}
