package types

// ImageInstance 镜像详情
type ImageInstance struct {
	// 基本信息
	ImageID      string `json:"image_id"`
	ImageName    string `json:"image_name"`
	Description  string `json:"description"`
	ImageVersion string `json:"image_version"`
	ImageFamily  string `json:"image_family"`

	// 镜像类型
	ImageOwnerAlias string `json:"image_owner_alias"` // system, self, others, marketplace
	IsSelfShared    bool   `json:"is_self_shared"`    // 是否共享镜像
	IsPublic        bool   `json:"is_public"`         // 是否公共镜像
	IsCopied        bool   `json:"is_copied"`         // 是否复制的镜像

	// 操作系统信息
	OSType       string `json:"os_type"` // linux, windows
	OSName       string `json:"os_name"` // CentOS 7.9 64位
	OSNameEn     string `json:"os_name_en"`
	Platform     string `json:"platform"`     // CentOS, Ubuntu, Windows Server
	Architecture string `json:"architecture"` // x86_64, arm64

	// 镜像状态
	Status   string `json:"status"`   // Available, Creating, Waiting, UnAvailable
	Progress string `json:"progress"` // 创建进度

	// 磁盘信息
	Size               int                `json:"size"` // 镜像大小 GB
	DiskDeviceMappings []ImageDiskMapping `json:"disk_device_mappings"`

	// 来源信息
	SourceInstanceID string `json:"source_instance_id"` // 来源实例ID (自定义镜像)
	SourceSnapshotID string `json:"source_snapshot_id"` // 来源快照ID
	SourceRegion     string `json:"source_region"`      // 来源地域 (复制镜像)

	// 使用统计
	Usage         string `json:"usage"`          // instance, none
	InstanceCount int    `json:"instance_count"` // 使用此镜像的实例数

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

	// 是否支持云盘扩容
	IsSupportCloudinit   bool `json:"is_support_cloudinit"`
	IsSupportIoOptimized bool `json:"is_support_io_optimized"`

	// 启动模式
	BootMode string `json:"boot_mode"` // BIOS, UEFI
}

// ImageDiskMapping 镜像磁盘映射
type ImageDiskMapping struct {
	Device     string `json:"device"` // 设备名
	Size       int    `json:"size"`   // GB
	SnapshotID string `json:"snapshot_id"`
	Type       string `json:"type"`   // system, data
	Format     string `json:"format"` // RAW, VHD
}

// ImageFilter 镜像过滤条件
type ImageFilter struct {
	ImageIDs        []string          `json:"image_ids,omitempty"`
	ImageName       string            `json:"image_name,omitempty"`
	ImageOwnerAlias string            `json:"image_owner_alias,omitempty"` // system, self, others, marketplace
	OSType          string            `json:"os_type,omitempty"`           // linux, windows
	Platform        string            `json:"platform,omitempty"`
	Architecture    string            `json:"architecture,omitempty"`
	Status          string            `json:"status,omitempty"`
	ResourceGroupID string            `json:"resource_group_id,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	PageNumber      int               `json:"page_number,omitempty"`
	PageSize        int               `json:"page_size,omitempty"`
}
