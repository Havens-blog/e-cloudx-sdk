package types

// ============================================================================
// ECS 实例创建相关类型
// ============================================================================

// CreateInstanceParams 创建实例参数（统一格式，模板创建和直接创建共享）
type CreateInstanceParams struct {
	Region           string            `json:"region"`             // 地域
	Zone             string            `json:"zone"`               // 可用区
	InstanceType     string            `json:"instance_type"`      // 实例规格
	ImageID          string            `json:"image_id"`           // 镜像 ID
	VPCID            string            `json:"vpc_id"`             // VPC ID
	SubnetID         string            `json:"subnet_id"`          // 子网/交换机 ID
	SecurityGroupIDs []string          `json:"security_group_ids"` // 安全组 ID 列表
	InstanceName     string            `json:"instance_name"`      // 实例名称
	HostName         string            `json:"host_name"`          // 主机名
	SystemDiskType   string            `json:"system_disk_type"`   // 系统盘类型
	SystemDiskSize   int               `json:"system_disk_size"`   // 系统盘大小 (GB)
	DataDisks        []DataDiskParam   `json:"data_disks"`         // 数据盘列表
	BandwidthOut     int               `json:"bandwidth_out"`      // 公网出带宽 (Mbps)
	ChargeType       string            `json:"charge_type"`        // 计费方式: PostPaid / PrePaid
	KeyPairName      string            `json:"key_pair_name"`      // 密钥对名称
	Tags             map[string]string `json:"tags"`               // 标签
	Count            int               `json:"count"`              // 创建数量 (1-20)
}

// DataDiskParam 数据盘参数
type DataDiskParam struct {
	Category string `json:"category"` // 磁盘类型
	Size     int    `json:"size"`     // 大小 (GB)
}

// CreateInstanceResult 创建实例结果
type CreateInstanceResult struct {
	InstanceIDs []string `json:"instance_ids"` // 成功创建的实例 ID 列表
}
