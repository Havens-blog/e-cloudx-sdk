package types

// VSwitchInstance 交换机/子网实例（通用格式）
type VSwitchInstance struct {
	// 基本信息
	VSwitchID   string `json:"vswitch_id"`   // 交换机ID
	VSwitchName string `json:"vswitch_name"` // 交换机名称
	Status      string `json:"status"`       // 状态: Available/Pending/Deleting
	Region      string `json:"region"`       // 地域
	Zone        string `json:"zone"`         // 可用区
	Description string `json:"description"`  // 描述

	// 网络配置
	CidrBlock     string `json:"cidr_block"`      // CIDR块
	IPv6CidrBlock string `json:"ipv6_cidr_block"` // IPv6 CIDR块
	EnableIPv6    bool   `json:"enable_ipv6"`     // 是否启用IPv6
	IsDefault     bool   `json:"is_default"`      // 是否为默认交换机
	GatewayIP     string `json:"gateway_ip"`      // 网关IP

	// 所属VPC
	VPCID   string `json:"vpc_id"`   // VPC ID
	VPCName string `json:"vpc_name"` // VPC名称

	// 资源统计
	AvailableIPCount int64 `json:"available_ip_count"` // 可用IP数量
	TotalIPCount     int64 `json:"total_ip_count"`     // 总IP数量

	// 路由表
	RouteTableID string `json:"route_table_id"` // 关联路由表ID

	// 时间信息
	CreationTime string `json:"creation_time"` // 创建时间

	// 项目/资源组信息
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ResourceGroupID string `json:"resource_group_id"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags     map[string]string `json:"tags"`
	Provider string            `json:"provider"` // 云厂商标识
}

// VSwitchInstanceFilter 交换机过滤条件
type VSwitchInstanceFilter struct {
	VSwitchIDs  []string          `json:"vswitch_ids,omitempty"`
	VSwitchName string            `json:"vswitch_name,omitempty"`
	VPCID       string            `json:"vpc_id,omitempty"`
	Zone        string            `json:"zone,omitempty"`
	Status      []string          `json:"status,omitempty"`
	IsDefault   *bool             `json:"is_default,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	PageNumber  int               `json:"page_number,omitempty"`
	PageSize    int               `json:"page_size,omitempty"`
}
