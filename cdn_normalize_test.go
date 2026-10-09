package cloudx

import "testing"

// 表驱动:各厂商原始值 → 统一枚举,未知值原样返回
func TestNormalizeCDNAttributes(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{"业务类型-阿里云web", NormalizeCDNBusinessType, "web", "web"},
		{"业务类型-阿里云DCDN", NormalizeCDNBusinessType, "dcdn", "whole_site"},
		{"业务类型-华为全站", NormalizeCDNBusinessType, "wholeSite", "whole_site"},
		{"业务类型-火山page", NormalizeCDNBusinessType, "page", "web"},
		{"业务类型-火山api", NormalizeCDNBusinessType, "api", "dynamic"},
		{"业务类型-华为点播", NormalizeCDNBusinessType, "vod", "media"},
		{"业务类型-AWS空值", NormalizeCDNBusinessType, "", ""},
		{"业务类型-未知原样", NormalizeCDNBusinessType, "something", "something"},
		{"服务区域-华为mainland_china", NormalizeCDNServiceArea, "mainland_china", "domestic"},
		{"服务区域-腾讯mainland", NormalizeCDNServiceArea, "mainland", "domestic"},
		{"服务区域-全球", NormalizeCDNServiceArea, "global", "global"},
		{"服务区域-海外", NormalizeCDNServiceArea, "overseas", "overseas"},
		{"状态-AWS Deployed", NormalizeCDNStatus, "Deployed", "online"},
		{"状态-火山 Started", NormalizeCDNStatus, "Started", "online"},
		{"状态-部署中", NormalizeCDNStatus, "InProgress", "configuring"},
		{"状态-停用", NormalizeCDNStatus, "Stopped", "offline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.fn(c.in); got != c.want {
				t.Fatalf("in=%q got=%q want=%q", c.in, got, c.want)
			}
		})
	}
}

// 过滤兼容:统一枚举展开为含自身与全部历史原始值的集合;
// 任意历史原始值经 Variants 展开也必须命中归一化后的目标域
func TestVariantsCoverLegacyRawValues(t *testing.T) {
	tables := []struct {
		name      string
		normalize func(string) string
		variants  func(string) []string
		table     map[string][]string
	}{
		{"业务类型", NormalizeCDNBusinessType, BusinessTypeVariants, cdnBusinessTypeValues},
		{"服务区域", NormalizeCDNServiceArea, ServiceAreaVariants, cdnServiceAreaValues},
		{"状态", NormalizeCDNStatus, StatusVariants, cdnStatusValues},
	}
	for _, tb := range tables {
		t.Run(tb.name, func(t *testing.T) {
			for unified, raws := range tb.table {
				set := map[string]bool{}
				for _, v := range tb.variants(unified) {
					set[v] = true
				}
				// 自身必在集合内
				if !set[unified] {
					t.Errorf("%s: variants(%q) 未包含自身", tb.name, unified)
				}
				// 每个历史原始值归一化后必须回到该统一枚举
				for _, raw := range raws {
					if got := tb.normalize(raw); got != unified {
						t.Errorf("%s: 原始值 %q 归一化为 %q,期望 %q", tb.name, raw, got, unified)
					}
					if !set[raw] {
						t.Errorf("%s: variants(%q) 未覆盖历史原始值 %q", tb.name, unified, raw)
					}
				}
			}
		})
	}
}
