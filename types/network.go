package types

// VPCInstance VPC实例（通用格式）
type VPCInstance struct {
	// 基本信息
	VPCID       string `json:"vpc_id"`
	VPCName     string `json:"vpc_name"`
	Status      string `json:"status"`
	Region      string `json:"region"`
	Description string `json:"description"`

	// 网络配置
	CidrBlock        string   `json:"cidr_block"`         // 主CIDR块
	SecondaryCidrs   []string `json:"secondary_cidrs"`    // 附加CIDR块
	IPv6CidrBlock    string   `json:"ipv6_cidr_block"`    // IPv6 CIDR块
	EnableIPv6       bool     `json:"enable_ipv6"`        // 是否启用IPv6
	IsDefault        bool     `json:"is_default"`         // 是否为默认VPC
	DhcpOptionsID    string   `json:"dhcp_options_id"`    // DHCP选项集ID
	EnableDnsSupport bool     `json:"enable_dns_support"` // 是否启用DNS支持

	// 关联资源统计
	VSwitchCount       int `json:"vswitch_count"`        // 交换机数量
	RouteTableCount    int `json:"route_table_count"`    // 路由表数量
	NatGatewayCount    int `json:"nat_gateway_count"`    // NAT网关数量
	SecurityGroupCount int `json:"security_group_count"` // 安全组数量

	// 计费信息
	CreationTime string `json:"creation_time"`

	// 项目/资源组信息
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags     map[string]string `json:"tags"`
	Provider string            `json:"provider"` // 云厂商标识
}

// VPCInstanceFilter VPC过滤条件
type VPCInstanceFilter struct {
	// VPC ID列表
	VPCIDs []string `json:"vpc_ids,omitempty"`

	// VPC名称（支持模糊匹配）
	VPCName string `json:"vpc_name,omitempty"`

	// 状态过滤
	Status []string `json:"status,omitempty"`

	// CIDR块
	CidrBlock string `json:"cidr_block,omitempty"`

	// 是否默认VPC
	IsDefault *bool `json:"is_default,omitempty"`

	// 标签过滤
	Tags map[string]string `json:"tags,omitempty"`

	// 分页
	PageNumber int `json:"page_number,omitempty"`
	PageSize   int `json:"page_size,omitempty"`
}
