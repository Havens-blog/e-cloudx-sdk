package types

// ==================== 网络域产品状态归一（eip / lb / vpc / vswitch） ====================
//
// 背景（P0-2）：EIP/LB/VPC/VSwitch 四类产品此前无统一状态表，五厂商输出互不兼容：
// - EIP：aliyun 原样 Status、tencent 原样 BIND/UNBIND、volcano 原样、aws/huawei 各自本地固定词；
// - LB：aliyun 原样 LoadBalancerStatus、aws state.Code、huawei ProvisioningStatus、volcano 原样、
//   tencent 数字码 0/1 转词（2 等其余码裸显数字）；
// - VPC/VSwitch：tencent 恒 "Available"（状态造假），其余厂商 Available/Pending/OK/ACTIVE 混用。
// 本文件提供与 ENI（types/eni.go NormalizeENIStatus）同构的表驱动归一：
// 大小写敏感匹配厂商原始值，未命中原样透传，空值归一为 "unknown"。
// 前端 e-cam-web fieldLabels 词表以本组 canonical 小写值为键（与 ENI 一致）。

// EIP 状态 canonical（小写蛇形，前端 EIP_STATUS_LABELS 收键）
const (
	EIPStatusInUse         = "in_use"
	EIPStatusAvailable     = "available"
	EIPStatusAssociating   = "associating"
	EIPStatusUnassociating = "unassociating"
	EIPStatusReleasing     = "releasing"
	EIPStatusPending       = "pending"
	EIPStatusError         = "error"
	EIPStatusFrozen        = "frozen"
	EIPStatusUnknown       = "unknown"
)

// LB 状态 canonical
const (
	LBStatusActive   = "active"
	LBStatusInactive = "inactive"
	LBStatusCreating = "creating"
	LBStatusUpdating = "updating"
	LBStatusDeleting = "deleting"
	LBStatusError    = "error"
	LBStatusFrozen   = "frozen"
	LBStatusUnknown  = "unknown"
)

// VPC 状态 canonical
const (
	VPCStatusAvailable = "available"
	VPCStatusPending   = "pending"
	VPCStatusDeleting  = "deleting"
	VPCStatusError     = "error"
	VPCStatusUnknown   = "unknown"
)

// VSwitch 状态 canonical
const (
	VSwitchStatusAvailable = "available"
	VSwitchStatusPending   = "pending"
	VSwitchStatusDeleting  = "deleting"
	VSwitchStatusError     = "error"
	VSwitchStatusUnknown   = "unknown"
)

// eipStatusMaps provider → 原始状态串 → 标准化状态（大小写敏感，未命中透传）
var eipStatusMaps = map[string]map[string]string{
	// 阿里云 EIP Status 官方值域：Associating/Unassociating/InUse/Available/Releasing
	"aliyun": {
		"Associating":   EIPStatusAssociating,
		"Unassociating": EIPStatusUnassociating,
		"InUse":         EIPStatusInUse,
		"Available":     EIPStatusAvailable,
		"Releasing":     EIPStatusReleasing,
	},
	// AWS 由转换函数已换算为固定词（绑定→InUse，未绑定→Available）
	"aws": {
		"InUse":     EIPStatusInUse,
		"Available": EIPStatusAvailable,
	},
	// 华为由转换函数换算（含 ACTIVE/ELB/VPN→InUse、DOWN→Available 的防御键）
	"huawei": {
		"InUse":      EIPStatusInUse,
		"Available":  EIPStatusAvailable,
		"Error":      EIPStatusError,
		"Frozen":     EIPStatusFrozen,
		"ACTIVE":     EIPStatusInUse,
		"DOWN":       EIPStatusAvailable,
		"BIND_ERROR": EIPStatusError,
		"FREEZED":    EIPStatusFrozen,
	},
	// 腾讯云 AddressStatus 官方值域：BIND/UNBIND/BIND_ENI（CREATE_FAILED 等防御键）
	"tencent": {
		"BIND":          EIPStatusInUse,
		"UNBIND":        EIPStatusAvailable,
		"BIND_ENI":      EIPStatusInUse,
		"CREATE_FAILED": EIPStatusError,
		"BINDING":       EIPStatusAssociating,
		"UNBINDING":     EIPStatusUnassociating,
	},
	// 火山引擎 EIP Status（与阿里同源值域，含 Attaching/Detaching 防御变体）
	"volcano": {
		"InUse":         EIPStatusInUse,
		"Available":     EIPStatusAvailable,
		"Associating":   EIPStatusAssociating,
		"Unassociating": EIPStatusUnassociating,
		"Releasing":     EIPStatusReleasing,
		"Attaching":     EIPStatusAssociating,
		"Detaching":     EIPStatusUnassociating,
	},
}

// lbStatusMaps provider → 原始状态串 → 标准化状态
var lbStatusMaps = map[string]map[string]string{
	// 阿里云 SLB LoadBalancerStatus：Active/Inactive/Provisioning/Configuring/Deleting
	"aliyun": {
		"Active":       LBStatusActive,
		"Inactive":     LBStatusInactive,
		"Provisioning": LBStatusCreating,
		"Configuring":  LBStatusCreating,
		"Deleting":     LBStatusDeleting,
	},
	// AWS ELBv2 state.Code：active/provisioning/active_impaired/failed
	"aws": {
		"active":          LBStatusActive,
		"provisioning":    LBStatusCreating,
		"active_impaired": LBStatusError,
		"failed":          LBStatusError,
	},
	// 华为 ELB ProvisioningStatus：ACTIVE/PENDING_CREATE/PENDING_UPDATE/PENDING_DELETE/ERROR/NOT_ACTIVE/FROZEN
	"huawei": {
		"ACTIVE":         LBStatusActive,
		"PENDING_CREATE": LBStatusCreating,
		"PENDING_UPDATE": LBStatusUpdating,
		"PENDING_DELETE": LBStatusDeleting,
		"ERROR":          LBStatusError,
		"NOT_ACTIVE":     LBStatusInactive,
		"FROZEN":         LBStatusFrozen,
	},
	// 腾讯 CLB Status 数字码已由转换函数词化为 Creating/Active，其余码原样数字兜底
	"tencent": {
		"Creating": LBStatusCreating,
		"Active":   LBStatusActive,
	},
	// 火山引擎 CLB Status（小写值域为主，PascalCase 防御变体同收）
	"volcano": {
		"active":   LBStatusActive,
		"inactive": LBStatusInactive,
		"creating": LBStatusCreating,
		"updating": LBStatusUpdating,
		"deleting": LBStatusDeleting,
		"error":    LBStatusError,
		"Active":   LBStatusActive,
		"Inactive": LBStatusInactive,
		"Creating": LBStatusCreating,
		"Deleting": LBStatusDeleting,
		"Error":    LBStatusError,
	},
}

// vpcStatusMaps provider → 原始状态串 → 标准化状态
var vpcStatusMaps = map[string]map[string]string{
	// 阿里云 VPC Status：Available/Pending
	"aliyun": {
		"Available": VPCStatusAvailable,
		"Pending":   VPCStatusPending,
	},
	// AWS VpcState：available/pending
	"aws": {
		"available": VPCStatusAvailable,
		"pending":   VPCStatusPending,
	},
	// 华为 VPC status：OK/ERROR/PENDING（转换层默认词 Available 亦收）
	"huawei": {
		"OK":        VPCStatusAvailable,
		"ERROR":     VPCStatusError,
		"PENDING":   VPCStatusPending,
		"CREATING":  VPCStatusPending,
		"Available": VPCStatusAvailable,
	},
	// 腾讯云 VPC 无状态字段，历史恒 "Available"（保持为 available，不再造假为父级词）
	"tencent": {
		"Available": VPCStatusAvailable,
	},
	// 火山引擎 VPC Status
	"volcano": {
		"Available": VPCStatusAvailable,
		"Creating":  VPCStatusPending,
		"Pending":   VPCStatusPending,
		"Deleting":  VPCStatusDeleting,
		"Error":     VPCStatusError,
	},
}

// vswitchStatusMaps provider → 原始状态串 → 标准化状态
var vswitchStatusMaps = map[string]map[string]string{
	// 阿里云 VSwitch Status：Available/Pending
	"aliyun": {
		"Available": VSwitchStatusAvailable,
		"Pending":   VSwitchStatusPending,
	},
	// AWS SubnetState：available/pending
	"aws": {
		"available": VSwitchStatusAvailable,
		"pending":   VSwitchStatusPending,
	},
	// 华为 subnet status：ACTIVE/DOWN（neutron 值域，转换层默认词 Available 亦收）
	"huawei": {
		"ACTIVE":    VSwitchStatusAvailable,
		"DOWN":      VSwitchStatusError,
		"Available": VSwitchStatusAvailable,
	},
	// 腾讯云子网无状态字段，历史恒 "Available"
	"tencent": {
		"Available": VSwitchStatusAvailable,
	},
	// 火山引擎 Subnet Status
	"volcano": {
		"Available": VSwitchStatusAvailable,
		"ACTIVE":    VSwitchStatusAvailable,
		"DOWN":      VSwitchStatusError,
		"Creating":  VSwitchStatusPending,
		"Pending":   VSwitchStatusPending,
		"Deleting":  VSwitchStatusDeleting,
		"Error":     VSwitchStatusError,
	},
}

// NormalizeEIPStatus 标准化 EIP 状态。未命中/未知 provider 返回原值；空值原样透传（字段缺失语义）。
func NormalizeEIPStatus(provider, status string) string {
	return lookupNetworkStatus(eipStatusMaps, provider, status)
}

// NormalizeLBStatus 标准化 LB 状态。未命中/未知 provider 返回原值；空值原样透传（字段缺失语义）。
func NormalizeLBStatus(provider, status string) string {
	return lookupNetworkStatus(lbStatusMaps, provider, status)
}

// NormalizeVPCStatus 标准化 VPC 状态。未命中/未知 provider 返回原值；空值原样透传（字段缺失语义）。
func NormalizeVPCStatus(provider, status string) string {
	return lookupNetworkStatus(vpcStatusMaps, provider, status)
}

// NormalizeVSwitchStatus 标准化 VSwitch 状态。未命中/未知 provider 返回原值；空值原样透传（字段缺失语义）。
func NormalizeVSwitchStatus(provider, status string) string {
	return lookupNetworkStatus(vswitchStatusMaps, provider, status)
}

// lookupNetworkStatus 表驱动归一：volcano/volcengine 双别名路由同一映射（与 ENI 一致）；
// 空值直接透传（网络域产品中空状态 = 字段未返回，与"未知"语义不同，前端渲染为 -）。
func lookupNetworkStatus(maps map[string]map[string]string, provider, status string) string {
	if status == "" {
		return ""
	}
	p := provider
	if p == "volcengine" {
		p = "volcano"
	}
	if m, ok := maps[p]; ok {
		if normalized, ok := m[status]; ok {
			return normalized
		}
	}
	return status
}
