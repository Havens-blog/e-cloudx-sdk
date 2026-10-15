package types

// DDOSInstance DDoS防护实例通用格式。
// 字段以各云厂商 SDK 结构实证归一,不硬造无证据字段:
//   - 腾讯云: antiddos v20200309 BGPInstance/BGPIPInstance(SDK 注释自带状态枚举与带宽单位)
//   - 阿里云: ddosbgp DescribeInstanceList / ddoscoo DescribeInstances
//   - 华为云: aad/v1 ListInstance(带宽单位未在 SDK 中标注,留空不猜)
//   - AWS:    shield ListProtections/DescribeSubscription
type DDOSInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`   // 实例ID
	InstanceName string `json:"instance_name"` // 实例名称
	Status       string `json:"status"`        // 状态(厂商原生值;腾讯: idle/attacking/blocking/creating/deblocking/isolate)
	Region       string `json:"region"`        // 地域
	Edition      string `json:"edition"`       // 产品形态(如 高防IP/高防包/DDoS原生防护/DDoS高防/Shield)

	// 防护带宽(原始数值 + 单位;单位以 SDK 注释实证为准,未知时为 "" 不猜)
	BasicBandwidth   int64  `json:"basic_bandwidth"`    // 保底防护带宽
	ElasticBandwidth int64  `json:"elastic_bandwidth"`  // 弹性防护带宽
	ServiceBandwidth int64  `json:"service_bandwidth"`  // 业务带宽
	BandwidthUnit    string `json:"bandwidth_unit"`     // "Gbps" / "Mbps" / ""(厂商未标注单位)
	CCQPS            int64  `json:"cc_qps"`             // CC防护峰值 QPS

	// 防护对象
	ProtectedIPs     []string `json:"protected_ips"`      // 防护IP/关联IP列表
	ProtectedIPCount int      `json:"protected_ip_count"` // 已防护IP数(厂商返回时)

	// 付费/周期
	ChargeType   string `json:"charge_type"` // 计费方式(厂商原值)
	AutoRenew    bool   `json:"auto_renew"`  // 是否自动续费
	CreationTime string `json:"creation_time"`
	ExpiredTime  string `json:"expired_time"`

	// 项目/资源组
	ProjectID       string `json:"project_id"`
	ResourceGroupID string `json:"resource_group_id"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags        map[string]string `json:"tags"`
	Description string            `json:"description"`
	Provider    string            `json:"provider"` // 云厂商标识
}

// DDOSInstanceFilter DDoS实例过滤条件
type DDOSInstanceFilter struct {
	InstanceName string `json:"instance_name,omitempty"` // 实例名称(模糊匹配)
	Status       string `json:"status,omitempty"`        // 状态
	Region       string `json:"region,omitempty"`        // 地域
	PageNumber   int    `json:"page_number,omitempty"`
	PageSize     int    `json:"page_size,omitempty"`
}