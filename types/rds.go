package types

// RDSInstance 云数据库实例（通用格式）
type RDSInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	Status       string `json:"status"`
	Region       string `json:"region"`
	Zone         string `json:"zone"`

	// 数据库信息
	Engine          string `json:"engine"`            // mysql, postgresql, mariadb, sqlserver
	EngineVersion   string `json:"engine_version"`    // 5.7, 8.0 等
	DBInstanceClass string `json:"db_instance_class"` // 实例规格

	// 配置信息
	CPU         int    `json:"cpu"`          // vCPU 数量
	Memory      int    `json:"memory"`       // 内存 MB
	Storage     int    `json:"storage"`      // 存储 GB
	StorageType string `json:"storage_type"` // 存储类型: cloud_ssd, cloud_essd 等
	MaxIOPS     int    `json:"max_iops"`     // 最大 IOPS

	// 网络信息
	ConnectionString string `json:"connection_string"` // 连接地址
	Port             int    `json:"port"`              // 端口
	VPCID            string `json:"vpc_id"`
	VSwitchID        string `json:"vswitch_id"`
	PrivateIP        string `json:"private_ip"`
	PublicIP         string `json:"public_ip"`

	// 高可用信息
	Category         string `json:"category"`           // Basic, HighAvailability, Finance
	ReplicationMode  string `json:"replication_mode"`   // Async, Sync, SemiSync
	SecondaryZone    string `json:"secondary_zone"`     // 备可用区
	ReadReplicaCount int    `json:"read_replica_count"` // 只读副本数

	// 计费信息
	ChargeType   string `json:"charge_type"` // PrePaid, PostPaid
	CreationTime string `json:"creation_time"`
	ExpiredTime  string `json:"expired_time"`

	// 安全信息
	SecurityIPList []string `json:"security_ip_list"` // 白名单
	SSLEnabled     bool     `json:"ssl_enabled"`

	// 备份信息
	BackupRetentionPeriod int    `json:"backup_retention_period"` // 备份保留天数
	PreferredBackupTime   string `json:"preferred_backup_time"`   // 备份时间窗口

	// 项目/资源组信息
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags        map[string]string `json:"tags"`
	Description string            `json:"description"`
	Provider    string            `json:"provider"` // 云厂商标识
}

// RDSInstanceFilter RDS实例过滤条件
type RDSInstanceFilter struct {
	InstanceIDs  []string          `json:"instance_ids,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	Status       []string          `json:"status,omitempty"`
	Engine       string            `json:"engine,omitempty"`
	VPCID        string            `json:"vpc_id,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	PageNumber   int               `json:"page_number,omitempty"`
	PageSize     int               `json:"page_size,omitempty"`
}
