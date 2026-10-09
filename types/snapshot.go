package types

// SnapshotInstance 磁盘快照详情
type SnapshotInstance struct {
	// 基本信息
	SnapshotID   string `json:"snapshot_id"`
	SnapshotName string `json:"snapshot_name"`
	Description  string `json:"description"`

	// 快照类型
	SnapshotType  string `json:"snapshot_type"`  // auto, user, all
	Category      string `json:"category"`       // standard, flash
	InstantAccess bool   `json:"instant_access"` // 是否开启快照极速可用

	// 状态信息
	Status   string `json:"status"`   // progressing, accomplished, failed
	Progress string `json:"progress"` // 创建进度 100%

	// 容量信息
	SourceDiskSize int `json:"source_disk_size"` // 源磁盘大小 GB
	SnapshotSize   int `json:"snapshot_size"`    // 快照大小 GB (增量)

	// 来源信息
	SourceDiskID       string `json:"source_disk_id"`       // 源磁盘ID
	SourceDiskType     string `json:"source_disk_type"`     // system, data
	SourceDiskCategory string `json:"source_disk_category"` // cloud_ssd, cloud_essd 等

	// 关联实例
	SourceInstanceID   string `json:"source_instance_id"` // 源实例ID (如果是系统盘快照)
	SourceInstanceName string `json:"source_instance_name"`

	// 加密信息
	Encrypted bool   `json:"encrypted"`
	KMSKeyID  string `json:"kms_key_id"`

	// 使用信息
	Usage          string `json:"usage"`            // image, disk, none
	UsedImageCount int    `json:"used_image_count"` // 基于此快照创建的镜像数
	UsedDiskCount  int    `json:"used_disk_count"`  // 基于此快照创建的磁盘数

	// 保留信息
	RetentionDays int `json:"retention_days"` // 保留天数 (自动快照)

	// 区域信息
	Region string `json:"region"`

	// 资源组/项目
	ResourceGroupID string `json:"resource_group_id"`

	// 时间信息
	CreationTime     string `json:"creation_time"`
	LastModifiedTime string `json:"last_modified_time"`

	// 标签
	Tags map[string]string `json:"tags"`

	// 云厂商
	Provider string `json:"provider"`
}

// SnapshotFilter 快照过滤条件
type SnapshotFilter struct {
	SnapshotIDs     []string          `json:"snapshot_ids,omitempty"`
	SnapshotName    string            `json:"snapshot_name,omitempty"`
	SnapshotType    string            `json:"snapshot_type,omitempty"` // auto, user, all
	Status          string            `json:"status,omitempty"`
	SourceDiskID    string            `json:"source_disk_id,omitempty"`
	SourceDiskType  string            `json:"source_disk_type,omitempty"` // system, data
	InstanceID      string            `json:"instance_id,omitempty"`      // 源实例ID
	Encrypted       *bool             `json:"encrypted,omitempty"`
	ResourceGroupID string            `json:"resource_group_id,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	PageNumber      int               `json:"page_number,omitempty"`
	PageSize        int               `json:"page_size,omitempty"`
}
