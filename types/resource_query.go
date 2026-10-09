package types

// ============================================================================
// 云资源查询相关类型（用于模板创建和直接创建时的联动下拉数据）
// ============================================================================

// InstanceTypeInfo 实例规格信息
type InstanceTypeInfo struct {
	InstanceType string  `json:"instance_type"` // 规格名称
	CPU          int     `json:"cpu"`           // CPU 核数
	MemoryGB     float64 `json:"memory_gb"`     // 内存大小 (GB)
	Architecture string  `json:"architecture"`  // 架构 (x86_64/arm64)
}

// ImageInfo 镜像信息
type ImageInfo struct {
	ImageID      string `json:"image_id"`     // 镜像 ID
	Name         string `json:"name"`         // 镜像名称
	OSType       string `json:"os_type"`      // 操作系统类型 (linux/windows)
	Platform     string `json:"platform"`     // 平台 (CentOS/Ubuntu/Windows Server 等)
	Architecture string `json:"architecture"` // 架构 (x86_64/arm64/i386)
}

// VPCInfo VPC 信息（轻量版，用于下拉选择）
type VPCInfo struct {
	VPCID     string `json:"vpc_id"`     // VPC ID
	VPCName   string `json:"vpc_name"`   // VPC 名称
	CidrBlock string `json:"cidr_block"` // CIDR 块
	Status    string `json:"status"`     // 状态
}

// SubnetInfo 子网/交换机信息（轻量版，用于下拉选择）
type SubnetInfo struct {
	SubnetID  string `json:"subnet_id"`  // 子网 ID
	Name      string `json:"name"`       // 子网名称
	CidrBlock string `json:"cidr_block"` // CIDR 块
	Zone      string `json:"zone"`       // 可用区
	VPCID     string `json:"vpc_id"`     // 所属 VPC ID
}

// SecurityGroupInfo 安全组信息（轻量版，用于下拉选择）
type SecurityGroupInfo struct {
	SecurityGroupID string `json:"security_group_id"` // 安全组 ID
	Name            string `json:"name"`              // 安全组名称
	Description     string `json:"description"`       // 描述
	VPCID           string `json:"vpc_id"`            // 所属 VPC ID
}

// ValidationError 参数校验错误项
type ValidationError struct {
	Field  string `json:"field"`  // 字段名称
	Reason string `json:"reason"` // 失败原因
}
