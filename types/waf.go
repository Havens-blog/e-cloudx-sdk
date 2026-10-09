package types

// WAFInstance WAF防火墙实例（通用格式）
type WAFInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`   // 实例ID
	InstanceName string `json:"instance_name"` // 实例名称
	Status       string `json:"status"`        // 状态: active/inactive/creating/expired
	Region       string `json:"region"`        // 地域
	Edition      string `json:"edition"`       // 版本: basic/pro/business/enterprise

	// 防护域名
	DomainCount    int      `json:"domain_count"`    // 已接入域名数
	DomainLimit    int      `json:"domain_limit"`    // 域名配额
	ProtectedHosts []string `json:"protected_hosts"` // 防护域名列表

	// 源站信息
	SourceIPs []string `json:"source_ips"` // 源站IP列表（回源地址）
	Cname     string   `json:"cname"`      // WAF分配的CNAME地址

	// 规则信息
	RuleCount      int `json:"rule_count"`       // 自定义规则数
	ACLRuleCount   int `json:"acl_rule_count"`   // ACL规则数
	CCRuleCount    int `json:"cc_rule_count"`    // CC防护规则数
	RateLimitCount int `json:"rate_limit_count"` // 限速规则数

	// 防护模式: block=拦截 / observe=观察 / off=关闭(空表示厂商不支持该语义)
	ProtectionMode string `json:"protection_mode"`

	// 防护规则列表(精简摘要;厂商规则模型差异大,只取可通用归一的字段)
	Rules []WAFRule `json:"rules,omitempty"`

	// 防护能力
	WAFEnabled     bool `json:"waf_enabled"`      // Web防护开关
	CCEnabled      bool `json:"cc_enabled"`       // CC防护开关
	AntiBotEnabled bool `json:"anti_bot_enabled"` // Bot防护开关

	// 规格信息
	QPS         int    `json:"qps"`          // QPS配额
	Bandwidth   int    `json:"bandwidth"`    // 带宽(Mbps)
	ExclusiveIP bool   `json:"exclusive_ip"` // 是否独享IP
	PayType     string `json:"pay_type"`     // 付费类型: subscription/payasyougo

	// 时间信息
	CreationTime string `json:"creation_time"` // 创建时间
	ExpiredTime  string `json:"expired_time"`  // 过期时间

	// 项目/资源组信息
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

// WAFRule 防护规则通用摘要(各厂商规则模型差异大,统一归一到这几个字段)。
type WAFRule struct {
	Name   string `json:"name"`        // 规则描述/名称
	Type   string `json:"rule_type"`   // 规则类型(如门神/id 等,厂商原值)
	Level  string `json:"level"`       // 威胁等级(高/中/低/观察等,厂商原值)
	Status string `json:"status"`      // enabled/disabled(规则开关)
}

// WAFInstanceFilter WAF实例过滤条件
type WAFInstanceFilter struct {
	InstanceName string `json:"instance_name,omitempty"` // 实例名称（模糊匹配）
	Status       string `json:"status,omitempty"`        // 状态
	Edition      string `json:"edition,omitempty"`       // 版本
	PageNumber   int    `json:"page_number,omitempty"`
	PageSize     int    `json:"page_size,omitempty"`
}
