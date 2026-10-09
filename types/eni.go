package types

// ENIInstance 弹性网卡实例（通用格式）
type ENIInstance struct {
	// 基本信息
	ENIID       string `json:"eni_id"`      // 弹性网卡ID
	ENIName     string `json:"eni_name"`    // 弹性网卡名称
	Description string `json:"description"` // 描述
	Status      string `json:"status"`      // 状态
	Type        string `json:"type"`        // 网卡类型: Primary(主网卡), Secondary(辅助网卡)
	Region      string `json:"region"`      // 地域
	Zone        string `json:"zone"`        // 可用区

	// 网络信息
	VPCID              string   `json:"vpc_id"`               // VPC ID
	SubnetID           string   `json:"subnet_id"`            // 子网/交换机ID
	PrimaryPrivateIP   string   `json:"primary_private_ip"`   // 主私网IP
	PrivateIPAddresses []string `json:"private_ip_addresses"` // 所有私网IP列表
	MacAddress         string   `json:"mac_address"`          // MAC地址
	IPv6Addresses      []string `json:"ipv6_addresses"`       // IPv6地址列表

	// 绑定信息
	InstanceID   string `json:"instance_id"`   // 绑定的ECS实例ID
	InstanceName string `json:"instance_name"` // 绑定的ECS实例名称
	DeviceIndex  int    `json:"device_index"`  // 设备索引

	// 安全组
	SecurityGroupIDs []string `json:"security_group_ids"` // 关联的安全组ID列表

	// 公网信息
	PublicIP     string   `json:"public_ip"`     // 关联的公网IP
	EIPAddresses []string `json:"eip_addresses"` // 关联的EIP地址列表

	// 资源信息
	ResourceGroupID string `json:"resource_group_id"` // 资源组ID
	ProjectID       string `json:"project_id"`        // 项目ID

	// 计费信息
	CreationTime string `json:"creation_time"` // 创建时间

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags     map[string]string `json:"tags"`
	Provider string            `json:"provider"` // 云厂商标识
}

// ENIInstanceFilter ENI实例过滤条件
type ENIInstanceFilter struct {
	ENIIDs           []string          `json:"eni_ids,omitempty"`
	ENIName          string            `json:"eni_name,omitempty"`
	Status           []string          `json:"status,omitempty"`
	Type             string            `json:"type,omitempty"` // Primary / Secondary
	VPCID            string            `json:"vpc_id,omitempty"`
	SubnetID         string            `json:"subnet_id,omitempty"`
	InstanceID       string            `json:"instance_id,omitempty"` // 绑定的ECS实例ID
	PrimaryPrivateIP string            `json:"primary_private_ip,omitempty"`
	SecurityGroupID  string            `json:"security_group_id,omitempty"` // 关联的安全组ID
	Tags             map[string]string `json:"tags,omitempty"`
	PageNumber       int               `json:"page_number,omitempty"`
	PageSize         int               `json:"page_size,omitempty"`
}

// ENI 网卡类型常量
const (
	ENITypePrimary   = "Primary"   // 主网卡
	ENITypeSecondary = "Secondary" // 辅助网卡
)

// ENI 标准化状态
const (
	ENIStatusAvailable = "available" // 可用 (未绑定)
	ENIStatusInUse     = "in_use"    // 使用中 (已绑定)
	ENIStatusAttaching = "attaching" // 绑定中
	ENIStatusDetaching = "detaching" // 解绑中
	ENIStatusCreating  = "creating"  // 创建中
	ENIStatusDeleting  = "deleting"  // 删除中
	ENIStatusError     = "error"     // 异常
	ENIStatusUnknown   = "unknown"   // 未知
)

// eniAliyunENIStatusMap 阿里云原始网卡状态 → 标准化状态（大小写敏感）
var eniAliyunENIStatusMap = map[string]string{
	"Available": ENIStatusAvailable,
	"InUse":     ENIStatusInUse,
	"Attaching": ENIStatusAttaching,
	"Detaching": ENIStatusDetaching,
	"Creating":  ENIStatusCreating,
	"Deleting":  ENIStatusDeleting,
}

// eniAWSENIStatusMap AWS 原始网卡状态 → 标准化状态（大小写敏感）
var eniAWSENIStatusMap = map[string]string{
	"available":  ENIStatusAvailable,
	"in-use":     ENIStatusInUse,
	"attaching":  ENIStatusAttaching,
	"detaching":  ENIStatusDetaching,
	"associated": ENIStatusInUse,
}

// eniHuaweiENIStatusMap 华为云原始网卡状态 → 标准化状态（大小写敏感）
var eniHuaweiENIStatusMap = map[string]string{
	"ACTIVE": ENIStatusInUse,
	"BUILD":  ENIStatusCreating,
	"DOWN":   ENIStatusAvailable,
	"ERROR":  ENIStatusError,
}

// eniTencentENIStatusMap 腾讯云原始网卡状态 → 标准化状态（大小写敏感）
// 注意："BINDbindingd " 为带尾空格的历史怪例，必须原样保留
var eniTencentENIStatusMap = map[string]string{
	"AVAILABLE":     ENIStatusAvailable,
	"BINDbindingd":  ENIStatusAttaching,
	"BINDbindingd ": ENIStatusAttaching,
	"BINDUNBINDING": ENIStatusDetaching,
	"BINDBOUND":     ENIStatusInUse,
	"BINDUNBOUND":   ENIStatusAvailable,
	"BINDDELETING":  ENIStatusDeleting,
	// 腾讯云 Pending 状态
	"PENDING":  ENIStatusCreating,
	"DELETING": ENIStatusDeleting,
}

// eniVolcanoENIStatusMap 火山引擎原始网卡状态 → 标准化状态（大小写敏感）
var eniVolcanoENIStatusMap = map[string]string{
	"Available": ENIStatusAvailable,
	"InUse":     ENIStatusInUse,
	"Attaching": ENIStatusAttaching,
	"Detaching": ENIStatusDetaching,
	"Creating":  ENIStatusCreating,
	"Deleting":  ENIStatusDeleting,
}

// eniStatusMaps provider → 原始状态串 → 标准化状态
// volcano 与 volcengine 两个 key 路由同一映射
var eniStatusMaps = map[string]map[string]string{
	"aliyun":     eniAliyunENIStatusMap,
	"aws":        eniAWSENIStatusMap,
	"huawei":     eniHuaweiENIStatusMap,
	"tencent":    eniTencentENIStatusMap,
	"volcano":    eniVolcanoENIStatusMap,
	"volcengine": eniVolcanoENIStatusMap,
}

// NormalizeENIStatus 标准化弹性网卡状态
// 大小写敏感（匹配厂商原始状态串），未命中或未知 provider 返回原值
func NormalizeENIStatus(provider, status string) string {
	if status == "" {
		return ENIStatusUnknown
	}

	if m, ok := eniStatusMaps[provider]; ok {
		if normalized, ok := m[status]; ok {
			return normalized
		}
	}
	return status
}
