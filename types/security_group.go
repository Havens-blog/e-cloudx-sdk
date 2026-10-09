package types

// SecurityGroupInstance 安全组详情
type SecurityGroupInstance struct {
	// 基本信息
	SecurityGroupID   string `json:"security_group_id"`
	SecurityGroupName string `json:"security_group_name"`
	Description       string `json:"description"`
	SecurityGroupType string `json:"security_group_type"` // normal, enterprise

	// 网络信息
	VPCID   string `json:"vpc_id"`
	VPCName string `json:"vpc_name"`

	// 规则统计
	IngressRuleCount int `json:"ingress_rule_count"` // 入方向规则数
	EgressRuleCount  int `json:"egress_rule_count"`  // 出方向规则数

	// 关联实例
	InstanceCount int      `json:"instance_count"` // 关联的ECS实例数
	InstanceIDs   []string `json:"instance_ids"`   // 关联的ECS实例ID列表

	// 规则详情
	IngressRules []SecurityGroupRule `json:"ingress_rules,omitempty"`
	EgressRules  []SecurityGroupRule `json:"egress_rules,omitempty"`

	// 区域信息
	Region string `json:"region"`

	// 资源组/项目
	ResourceGroupID string `json:"resource_group_id"`

	// 时间信息
	CreationTime string `json:"creation_time"`

	// 标签
	Tags map[string]string `json:"tags"`

	// 云厂商
	Provider string `json:"provider"`
}

// SecurityGroupRule 安全组规则
type SecurityGroupRule struct {
	RuleID        string `json:"rule_id"`
	Direction     string `json:"direction"`       // ingress, egress
	Protocol      string `json:"protocol"`        // tcp, udp, icmp, gre, all
	PortRange     string `json:"port_range"`      // 1/65535, 22/22, -1/-1
	SourceCIDR    string `json:"source_cidr"`     // 入方向: 源地址
	DestCIDR      string `json:"dest_cidr"`       // 出方向: 目标地址
	SourceGroupID string `json:"source_group_id"` // 源安全组ID
	DestGroupID   string `json:"dest_group_id"`   // 目标安全组ID
	Priority      int    `json:"priority"`        // 优先级 1-100
	Policy        string `json:"policy"`          // accept, drop
	Description   string `json:"description"`
	CreationTime  string `json:"creation_time"`
}

// SecurityGroupFilter 安全组过滤条件
type SecurityGroupFilter struct {
	SecurityGroupIDs  []string          `json:"security_group_ids,omitempty"`
	SecurityGroupName string            `json:"security_group_name,omitempty"`
	VPCID             string            `json:"vpc_id,omitempty"`
	SecurityGroupType string            `json:"security_group_type,omitempty"`
	ResourceGroupID   string            `json:"resource_group_id,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	PageNumber        int               `json:"page_number,omitempty"`
	PageSize          int               `json:"page_size,omitempty"`
}
