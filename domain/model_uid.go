// 文件：model_uid.go
//
// 作用：资产类型 ↔ model_uid 映射的单一事实源（清单收敛：消除 DAO 查询构建器
// buildQuery/buildSearchQuery 两份 19/16-case switch 的复制漂移，并为 web 反向
// 提取 extractAssetType 提供同步的逆向索引）。
//
// 约定：数据库 model_uid = "<provider>_<suffix>"（如 aliyun_ecs），或通用
// "<generic>"（如 cloud_vm）。此注册表收敛自：
//   - internal/cam/repository/dao/instance.go buildQuery / buildSearchQuery
//   - internal/cam/web/asset_vo.go extractAssetType
//
// 各条目的 Suffixes 顺序即反向提取的后缀优先级（"lb" 必须先于 "slb"/"alb"/"nlb"，
// 否则 tencent_slb 会被误判为 slb）。
//
// 未纳入的同类映射（语义不同，非复制对，保持各自实现）：
//   - cam/web/asset_helpers.go matchAssetType（lb 按厂商枚举后缀，与注册表全量不一致）
//   - cam/servicetree/service/node_asset.go extractAssetType（首下划线截断，cloud_slb→"slb"，
//     与 web 版 "lb" 语义本就不同，测试锁定）
//   - cam/tag/tag_compliance.go resourceTypeToModelUID（未锚定 + 大小写不敏感 regex）
//   - topology/service/live_builder.go modelUIDToType（子串匹配 → 拓扑节点类型，独立词汇表）
package domain

import "strings"

// ModelUIDPattern 资源类型 → model_uid 匹配模式。
type ModelUIDPattern struct {
	// Generics 通用 model_uid 精确值（如 cloud_vm、cloud_subnet）。
	Generics []string
	// Suffixes 厂商 model_uid 后缀（如 ecs），用于构造 "_ecs" 匹配；
	// 顺序即反向提取优先级（"lb" 必须先于 "slb"/"alb"/"nlb"）。
	Suffixes []string
	// BareNames 裸类型名（lb 家族含 slb/alb/nlb；vswitch 含 subnet），查询 key 别名。
	BareNames []string
}

// modelUIDEntry 有序注册表条目（顺序 = 反向提取的后缀扫描优先级，
// 对齐原 extractAssetType 的扁平后缀表：_ecs,_disk,..._eni,_vswitch,_subnet,_lb,...）。
type modelUIDEntry struct {
	short string
	pat   ModelUIDPattern
}

// modelUIDEntries 注册表（19 类资源）。
var modelUIDEntries = []modelUIDEntry{
	{"ecs", ModelUIDPattern{Generics: []string{"cloud_vm"}, Suffixes: []string{"ecs"}, BareNames: []string{"ecs"}}},
	{"disk", ModelUIDPattern{Generics: []string{"cloud_disk"}, Suffixes: []string{"disk"}, BareNames: []string{"disk"}}},
	{"snapshot", ModelUIDPattern{Generics: []string{"cloud_snapshot"}, Suffixes: []string{"snapshot"}, BareNames: []string{"snapshot"}}},
	{"security_group", ModelUIDPattern{Generics: []string{"cloud_security_group"}, Suffixes: []string{"security_group"}, BareNames: []string{"security_group", "security-group"}}},
	{"rds", ModelUIDPattern{Generics: []string{"cloud_rds"}, Suffixes: []string{"rds"}, BareNames: []string{"rds"}}},
	{"redis", ModelUIDPattern{Generics: []string{"cloud_redis"}, Suffixes: []string{"redis"}, BareNames: []string{"redis"}}},
	{"mongodb", ModelUIDPattern{Generics: []string{"cloud_mongodb"}, Suffixes: []string{"mongodb"}, BareNames: []string{"mongodb"}}},
	{"vpc", ModelUIDPattern{Generics: []string{"cloud_vpc"}, Suffixes: []string{"vpc"}, BareNames: []string{"vpc"}}},
	{"eip", ModelUIDPattern{Generics: []string{"cloud_eip"}, Suffixes: []string{"eip"}, BareNames: []string{"eip"}}},
	{"eni", ModelUIDPattern{Generics: []string{"cloud_eni"}, Suffixes: []string{"eni"}, BareNames: []string{"eni"}}},
	{"vswitch", ModelUIDPattern{Generics: []string{"cloud_vswitch", "cloud_subnet"}, Suffixes: []string{"vswitch", "subnet"}, BareNames: []string{"vswitch", "subnet"}}},
	{"lb", ModelUIDPattern{Generics: []string{"cloud_lb", "cloud_slb", "cloud_alb", "cloud_nlb"}, Suffixes: []string{"lb", "slb", "alb", "nlb", "elb", "clb"}, BareNames: []string{"lb", "slb", "alb", "nlb"}}},
	{"cdn", ModelUIDPattern{Generics: []string{"cloud_cdn"}, Suffixes: []string{"cdn"}, BareNames: []string{"cdn"}}},
	{"waf", ModelUIDPattern{Generics: []string{"cloud_waf"}, Suffixes: []string{"waf"}, BareNames: []string{"waf"}}},
	{"image", ModelUIDPattern{Generics: []string{"cloud_image"}, Suffixes: []string{"image"}, BareNames: []string{"image"}}},
	{"nas", ModelUIDPattern{Generics: []string{"cloud_nas"}, Suffixes: []string{"nas"}, BareNames: []string{"nas"}}},
	{"oss", ModelUIDPattern{Generics: []string{"cloud_oss"}, Suffixes: []string{"oss"}, BareNames: []string{"oss"}}},
	{"kafka", ModelUIDPattern{Generics: []string{"cloud_kafka"}, Suffixes: []string{"kafka"}, BareNames: []string{"kafka"}}},
	{"elasticsearch", ModelUIDPattern{Generics: []string{"cloud_elasticsearch"}, Suffixes: []string{"elasticsearch"}, BareNames: []string{"elasticsearch"}}},
}

// modelUIDLookup 任意 key（短名/通用名/裸别名）→ 模式。
var modelUIDLookup = func() map[string]ModelUIDPattern {
	m := make(map[string]ModelUIDPattern, len(modelUIDEntries)*3)
	for _, e := range modelUIDEntries {
		m[e.short] = e.pat
		for _, g := range e.pat.Generics {
			m[g] = e.pat
		}
		for _, b := range e.pat.BareNames {
			m[b] = e.pat
		}
	}
	return m
}()

// modelUIDGenericLookup 通用 model_uid → 短名（extractAssetType 精确匹配分支专用；
// 不含裸名，避免 "subnet" 被解析成 "vswitch"）。
var modelUIDGenericLookup = func() map[string]string {
	m := make(map[string]string, len(modelUIDEntries))
	for _, e := range modelUIDEntries {
		for _, g := range e.pat.Generics {
			m[g] = e.short
		}
	}
	return m
}()

// ModelUIDPatternFor 解析任意 key（短名/通用名/裸别名）为匹配模式。
func ModelUIDPatternFor(key string) (ModelUIDPattern, bool) {
	p, ok := modelUIDLookup[key]
	return p, ok
}

// ExtractAssetType 从 model_uid 提取资产类型（反向映射，语义与原 web 版一致）：
// 先精确匹配通用 model_uid（cloud_vm → ecs），再按注册表顺序扫描厂商后缀
// （aliyun_subnet → subnet，aliyun_slb → lb）。未命中返回原值。
func ExtractAssetType(modelUID string) string {
	if short, ok := modelUIDGenericLookup[modelUID]; ok {
		return short
	}
	for _, e := range modelUIDEntries {
		for _, s := range e.pat.Suffixes {
			suffix := "_" + s
			if len(modelUID) > len(suffix) && strings.HasSuffix(modelUID, suffix) {
				return s
			}
		}
	}
	return modelUID
}
