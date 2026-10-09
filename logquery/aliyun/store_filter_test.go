package aliyun

import (
	"reflect"
	"testing"
)

// filterCatalogStores 的 kind 感知过滤:ALB 混装项目里的 app 业务日志
// (-business/-tomcat-access)与 K8s 控制面流(apiserver/ccm/controlplane/
// kcm/scheduler)既无 alb_layer7_access_log 主题又逐条全扫描拖慢联邦查询,
// 需剔除;非 ALB 类型(CDN 转存等)不受影响;内部流(-metrics/internal-*)
// 无条件过滤。
func TestFilterCatalogStores_ALBSkipsNonLB(t *testing.T) {
	in := []string{
		"alb-mvezktcft1d81uojx9",                 // ALB 实例流:保留
		"prod-3d_jlcpcb_com_https_access",       // ALB ingress 访问流:保留
		"prod-ai-app-core-service-business",     // 应用业务日志:剔除
		"prod-ai-app-core-service-tomcat-access", // 应用访问日志:剔除
		"apiserver-c15269bdca5024133b87c8ed4206ace55", // K8s 控制面:剔除
		"kcm-c15269bdca5024133b87c8ed4206ace55",  // K8s 控制面:剔除
		"jlc-lb-log",                            // 项目同名聚合:保留
		"internal-ml-log",                       // 内部流:剔除
		"prod-overseas-web-app-log",             // 杂项:保留(不误伤)
	}
	want := []string{
		"alb-mvezktcft1d81uojx9",
		"prod-3d_jlcpcb_com_https_access",
		"jlc-lb-log",
		"prod-overseas-web-app-log",
	}
	if got := filterCatalogStores(kindALB, in); !reflect.DeepEqual(got, want) {
		t.Fatalf("ALB filter:\n want=%v\n  got=%v", want, got)
	}
}

func TestFilterCatalogStores_NonALBOnlyInternal(t *testing.T) {
	in := []string{
		"jlcfa-his",            // CDN 转存:保留
		"api_forface3d_com",    // CDN 转存:保留
		"prod-x-business",      // 名字带 -business 但非 ALB 类型:保留
		"app-metrics",          // 内部流:剔除
		"foo-metrics-result",   // 内部流:剔除
	}
	want := []string{"jlcfa-his", "api_forface3d_com", "prod-x-business"}
	if got := filterCatalogStores(kindCDNOffline, in); !reflect.DeepEqual(got, want) {
		t.Fatalf("CDN-offline filter:\n want=%v\n  got=%v", want, got)
	}
}

func TestNonALBStore(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"prod-overseas-order-service-business", true},
		{"prod-overseas-order-service-tomcat-access", true},
		{"apiserver-c15269bdca5024133b87c8ed4206ace55", true},
		{"controlplane-events-c15269bdca5024133b87c8ed4206ace55", true},
		{"scheduler-c15269bdca5024133b87c8ed4206ace55", true},
		{"ccm-x", true},
		{"kcm-x", true},
		{"alb-mvezktcft1d81uojx9", false},
		{"prod-3d_jlcpcb_com_https_access", false},
		{"prod-3d_jlcpcb_com_https_error", false},
		{"nacos-jlcops-com", false},
		{"jlc-lb-log", false},
	}
	for _, c := range cases {
		if got := nonALBStore(c.name); got != c.want {
			t.Errorf("nonALBStore(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}