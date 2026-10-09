package domain

import (
	"reflect"
	"testing"
)

// allShorts 注册表应覆盖的全部资源类型短名。
var allShorts = []string{
	"ecs", "disk", "snapshot", "security_group", "rds", "redis", "mongodb",
	"vpc", "eip", "eni", "vswitch", "lb", "cdn", "waf", "image",
	"nas", "oss", "kafka", "elasticsearch",
}

func TestModelUIDPatternFor_AllRegistered(t *testing.T) {
	for _, short := range allShorts {
		if _, ok := ModelUIDPatternFor(short); !ok {
			t.Errorf("ModelUIDPatternFor(%q) 未注册", short)
		}
	}
}

func TestModelUIDPatternFor_Aliases(t *testing.T) {
	cases := []struct {
		key             string
		generics, suffix []string
	}{
		{"ecs", []string{"cloud_vm"}, []string{"ecs"}},
		{"cloud_vm", []string{"cloud_vm"}, []string{"ecs"}},
		{"rds", []string{"cloud_rds"}, []string{"rds"}},
		{"cloud_rds", []string{"cloud_rds"}, []string{"rds"}},
		// lb 家族：短名 / 裸别名 / 通用名 → 同一模式
		{"lb", []string{"cloud_lb", "cloud_slb", "cloud_alb", "cloud_nlb"}, []string{"lb", "slb", "alb", "nlb", "elb", "clb"}},
		{"slb", []string{"cloud_lb", "cloud_slb", "cloud_alb", "cloud_nlb"}, []string{"lb", "slb", "alb", "nlb", "elb", "clb"}},
		{"cloud_lb", []string{"cloud_lb", "cloud_slb", "cloud_alb", "cloud_nlb"}, []string{"lb", "slb", "alb", "nlb", "elb", "clb"}},
		// vswitch 家族
		{"vswitch", []string{"cloud_vswitch", "cloud_subnet"}, []string{"vswitch", "subnet"}},
		{"subnet", []string{"cloud_vswitch", "cloud_subnet"}, []string{"vswitch", "subnet"}},
		{"cloud_subnet", []string{"cloud_vswitch", "cloud_subnet"}, []string{"vswitch", "subnet"}},
		// security_group 连字符别名
		{"security-group", []string{"cloud_security_group"}, []string{"security_group"}},
		{"security_group", []string{"cloud_security_group"}, []string{"security_group"}},
		// 漂移修复目标类型
		{"cdn", []string{"cloud_cdn"}, []string{"cdn"}},
		{"cloud_cdn", []string{"cloud_cdn"}, []string{"cdn"}},
		{"waf", []string{"cloud_waf"}, []string{"waf"}},
		{"eni", []string{"cloud_eni"}, []string{"eni"}},
		{"image", []string{"cloud_image"}, []string{"image"}},
	}
	for _, tt := range cases {
		pat, ok := ModelUIDPatternFor(tt.key)
		if !ok {
			t.Errorf("ModelUIDPatternFor(%q) 未命中", tt.key)
			continue
		}
		if !reflect.DeepEqual(pat.Generics, tt.generics) {
			t.Errorf("ModelUIDPatternFor(%q).Generics = %v, want %v", tt.key, pat.Generics, tt.generics)
		}
		if !reflect.DeepEqual(pat.Suffixes, tt.suffix) {
			t.Errorf("ModelUIDPatternFor(%q).Suffixes = %v, want %v", tt.key, pat.Suffixes, tt.suffix)
		}
	}
}

func TestModelUIDPatternFor_Unknown(t *testing.T) {
	if _, ok := ModelUIDPatternFor("not_a_type"); ok {
		t.Error("未知类型不应命中")
	}
}

// TestExtractAssetType 反向提取：语义与原 web 版 asset_vo.go extractAssetType 一致
// （通用名精确匹配 → 注册表顺序后缀扫描 → 原样返回）。
func TestExtractAssetType(t *testing.T) {
	cases := []struct {
		modelUID, want string
	}{
		// 通用模型
		{"cloud_vm", "ecs"},
		{"cloud_rds", "rds"},
		{"cloud_slb", "lb"}, // 与 web 版旧语义一致（lb 家族归并）
		{"cloud_subnet", "vswitch"},
		{"cloud_vswitch", "vswitch"},
		// 厂商前缀：多词类型不得切错
		{"aliyun_ecs", "ecs"},
		{"volcano_ecs", "ecs"},
		{"aliyun_security_group", "security_group"},
		{"volcengine_elasticsearch", "elasticsearch"},
		{"huawei_mongodb", "mongodb"},
		{"aws_rds", "rds"},
		// lb 家族返回字面后缀（与旧 web 版一致：aliyun_slb→"slb"，仅通用名归并 lb）
		{"aliyun_slb", "slb"},
		{"aliyun_alb", "alb"},
		{"aliyun_nlb", "nlb"},
		{"aliyun_lb", "lb"},
		// vswitch 家族返回字面后缀
		{"aliyun_subnet", "subnet"},
		{"aliyun_vswitch", "vswitch"},
		// 注册表新增 elb/clb（旧版返回原值，现归入 lb 家族）
		{"aws_elb", "elb"},
		{"tencent_clb", "clb"},
		// 无前缀原样返回
		{"ecs", "ecs"},
		{"subnet", "subnet"},
		{"", ""},
		{"nonsense", "nonsense"},
	}
	for _, tt := range cases {
		if got := ExtractAssetType(tt.modelUID); got != tt.want {
			t.Errorf("ExtractAssetType(%q) = %q, want %q", tt.modelUID, got, tt.want)
		}
	}
}
