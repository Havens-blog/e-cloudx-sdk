package types

// DiskInstance 云盘详情
type DiskInstance struct {
	// 基本信息
	DiskID      string `json:"disk_id"`
	DiskName    string `json:"disk_name"`
	Description string `json:"description"`

	// 磁盘类型
	DiskType         string `json:"disk_type"`         // system, data
	Category         string `json:"category"`          // cloud, cloud_efficiency, cloud_ssd, cloud_essd, cloud_auto
	PerformanceLevel string `json:"performance_level"` // PL0, PL1, PL2, PL3 (ESSD)

	// 容量信息
	Size       int `json:"size"`       // GB
	IOPS       int `json:"iops"`       // 每秒读写次数
	Throughput int `json:"throughput"` // 吞吐量 MB/s

	// 状态信息
	Status             string `json:"status"`               // In_use, Available, Attaching, Detaching, Creating, ReIniting
	Portable           bool   `json:"portable"`             // 是否可卸载
	DeleteAutoSnapshot bool   `json:"delete_auto_snapshot"` // 删除磁盘时是否删除自动快照
	DeleteWithInstance bool   `json:"delete_with_instance"` // 是否随实例释放
	EnableAutoSnapshot bool   `json:"enable_auto_snapshot"` // 是否启用自动快照

	// 挂载信息
	InstanceID   string `json:"instance_id"`   // 挂载的实例ID
	InstanceName string `json:"instance_name"` // 挂载的实例名称
	Device       string `json:"device"`        // 设备名 /dev/xvda
	AttachedTime string `json:"attached_time"` // 挂载时间

	// 加密信息
	Encrypted bool   `json:"encrypted"`
	KMSKeyID  string `json:"kms_key_id"`

	// 快照信息
	SourceSnapshotID     string `json:"source_snapshot_id"`      // 创建磁盘的快照ID
	AutoSnapshotPolicyID string `json:"auto_snapshot_policy_id"` // 自动快照策略ID
	SnapshotCount        int    `json:"snapshot_count"`          // 快照数量

	// 网络信息
	Zone   string `json:"zone"`
	Region string `json:"region"`

	// 镜像信息 (系统盘)
	ImageID string `json:"image_id"`

	// 计费信息
	ChargeType  string `json:"charge_type"` // PrePaid, PostPaid
	ExpiredTime string `json:"expired_time"`

	// 资源组/项目
	ResourceGroupID string `json:"resource_group_id"`

	// 时间信息
	CreationTime string `json:"creation_time"`

	// 标签
	Tags map[string]string `json:"tags"`

	// 云厂商
	Provider string `json:"provider"`

	// 多重挂载 (共享盘)
	MultiAttach bool             `json:"multi_attach"`
	Attachments []DiskAttachment `json:"attachments,omitempty"`
}

// DiskAttachment 磁盘挂载信息
type DiskAttachment struct {
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	Device       string `json:"device"`
	AttachedTime string `json:"attached_time"`
}

// DiskFilter 云盘过滤条件
type DiskFilter struct {
	DiskIDs         []string          `json:"disk_ids,omitempty"`
	DiskName        string            `json:"disk_name,omitempty"`
	DiskType        string            `json:"disk_type,omitempty"` // system, data
	Category        string            `json:"category,omitempty"`
	Status          string            `json:"status,omitempty"`
	InstanceID      string            `json:"instance_id,omitempty"` // 挂载的实例ID
	Portable        *bool             `json:"portable,omitempty"`
	Encrypted       *bool             `json:"encrypted,omitempty"`
	ResourceGroupID string            `json:"resource_group_id,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	PageNumber      int               `json:"page_number,omitempty"`
	PageSize        int               `json:"page_size,omitempty"`
}
