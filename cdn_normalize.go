package cloudx

import "github.com/Havens-blog/e-cloudx-sdk/types"

// CDN 属性归一化:各厂商原始枚举(如 volcengine 的 page/api、aliyun 的 dcdn、
// huawei 的 mainland_china、AWS 的 Deployed)映射为统一枚举,前端/过滤不再感知厂商差异。
//
// 归一化在同步写库前生效(两条同步路径都调用 NormalizeCDNInstance),原始值保留在
// attributes 的 *_raw 字段便于排查;ListCDN 过滤用 XxxVariants 做 $in 兼容——
// 未重新同步的历史数据仍是原始值,过滤照常命中。

// cdnBusinessTypeValues 统一业务类型 → 各厂商历史原始值
// web=网页/静态加速 download=下载加速 media=流媒体/点播 whole_site=全站加速 dynamic=动态加速
var cdnBusinessTypeValues = map[string][]string{
	"web":        {"web", "page"},
	"download":   {"download", "file"},
	"media":      {"media", "video", "vod", "vodDomainName"},
	"whole_site": {"wholeSite", "dcdn"},
	"dynamic":    {"api", "dynamic"},
	"other":      {"other"},
}

// cdnServiceAreaValues 统一服务区域 → 各厂商历史原始值
var cdnServiceAreaValues = map[string][]string{
	"domestic": {"domestic", "mainland", "mainland_china"},
	"overseas": {"overseas"},
	"global":   {"global"},
}

// cdnStatusValues 统一状态 → 各厂商历史原始值
var cdnStatusValues = map[string][]string{
	"online":       {"online", "Online", "Deployed", "deployed", "active", "Active", "Started", "started"},
	"offline":      {"offline", "Offline", "stopped", "Stopped", "disabled", "closed", "Closed"},
	"configuring":  {"configuring", "Configuring", "InProgress", "inprogress", "deploying", "Deploying", "creating"},
	"checking":     {"checking", "Checking", "pending"},
	"check_failed": {"check_failed", "CheckFailed"},
	"error":        {"error", "failed", "Failed"},
}

// normalizeByTable 按映射表归一化,未收录的值原样返回
func normalizeByTable(raw string, table map[string][]string) string {
	if raw == "" {
		return ""
	}
	for unified, variants := range table {
		for _, v := range variants {
			if raw == v {
				return unified
			}
		}
	}
	return raw
}

// NormalizeCDNBusinessType 厂商原始业务类型 → 统一枚举(web/download/media/whole_site/dynamic/other)
func NormalizeCDNBusinessType(raw string) string {
	return normalizeByTable(raw, cdnBusinessTypeValues)
}

// NormalizeCDNServiceArea 厂商原始服务区域 → 统一枚举(domestic/overseas/global)
func NormalizeCDNServiceArea(raw string) string {
	return normalizeByTable(raw, cdnServiceAreaValues)
}

// NormalizeCDNStatus 厂商原始状态 → 统一枚举(online/offline/configuring/checking/check_failed/error)
func NormalizeCDNStatus(raw string) string {
	return normalizeByTable(raw, cdnStatusValues)
}

// BusinessTypeVariants 统一业务类型对应的所有历史原始值(含自身),
// 供过滤层 $in 兼容未重同步的历史数据;未知值原样返回单元素
func BusinessTypeVariants(unified string) []string {
	return variantsOf(unified, cdnBusinessTypeValues)
}

// ServiceAreaVariants 统一服务区域对应的所有历史原始值(含自身)
func ServiceAreaVariants(unified string) []string {
	return variantsOf(unified, cdnServiceAreaValues)
}

// StatusVariants 统一状态对应的所有历史原始值(含自身)
func StatusVariants(unified string) []string {
	return variantsOf(unified, cdnStatusValues)
}

func variantsOf(unified string, table map[string][]string) []string {
	if variants, ok := table[unified]; ok {
		return append([]string{unified}, variants...)
	}
	return []string{unified}
}

// NormalizeCDNInstance 就地把实例的三类枚举归一化为统一值,
// 原始值保留到 *_raw 字段(由同步层一并写入 attributes)。
func NormalizeCDNInstance(inst *types.CDNInstance) {
	inst.BusinessTypeRaw = inst.BusinessType
	inst.BusinessType = NormalizeCDNBusinessType(inst.BusinessType)
	inst.ServiceAreaRaw = inst.ServiceArea
	inst.ServiceArea = NormalizeCDNServiceArea(inst.ServiceArea)
	inst.StatusRaw = inst.Status
	inst.Status = NormalizeCDNStatus(inst.Status)
}
