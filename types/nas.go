package types

import "time"

// NASInstance NAS 文件存储实例
type NASInstance struct {
	// 基本信息
	FileSystemID   string `json:"file_system_id"`   // 文件系统ID
	FileSystemName string `json:"file_system_name"` // 文件系统名称
	Description    string `json:"description"`      // 描述
	Status         string `json:"status"`           // 状态: Creating/Running/Stopping/Stopped
	Region         string `json:"region"`           // 地域
	Zone           string `json:"zone"`             // 可用区

	// 类型和协议
	FileSystemType string `json:"file_system_type"` // 类型: standard/extreme/cpfs
	ProtocolType   string `json:"protocol_type"`    // 协议: NFS/SMB/CPFS
	StorageType    string `json:"storage_type"`     // 存储类型: Capacity/Performance

	// 容量信息
	Capacity     int64 `json:"capacity"`      // 总容量 (GB)
	UsedCapacity int64 `json:"used_capacity"` // 已用容量 (GB)
	MeteredSize  int64 `json:"metered_size"`  // 计量大小 (Bytes)

	// 网络信息
	VPCID        string        `json:"vpc_id"`        // VPC ID
	VSwitchID    string        `json:"vswitch_id"`    // 交换机ID
	MountTargets []MountTarget `json:"mount_targets"` // 挂载点列表

	// 计费信息
	ChargeType   string    `json:"charge_type"`   // 付费类型: PayAsYouGo/Subscription
	CreationTime time.Time `json:"creation_time"` // 创建时间
	ExpiredTime  time.Time `json:"expired_time"`  // 过期时间

	// 加密
	EncryptType int    `json:"encrypt_type"` // 加密类型: 0-不加密, 1-NAS托管密钥, 2-用户管理密钥
	KMSKeyID    string `json:"kms_key_id"`   // KMS密钥ID

	// 其他
	Tags     map[string]string `json:"tags"`     // 标签
	Provider string            `json:"provider"` // 云厂商
}

// MountTarget 挂载点
type MountTarget struct {
	MountTargetID     string `json:"mount_target_id"`     // 挂载点ID
	MountTargetDomain string `json:"mount_target_domain"` // 挂载点域名
	NetworkType       string `json:"network_type"`        // 网络类型: VPC/Classic
	VPCID             string `json:"vpc_id"`              // VPC ID
	VSwitchID         string `json:"vswitch_id"`          // 交换机ID
	AccessGroupName   string `json:"access_group_name"`   // 权限组名称
	Status            string `json:"status"`              // 状态
}

// NASInstanceFilter NAS 过滤条件
type NASInstanceFilter struct {
	FileSystemIDs  []string          `json:"file_system_ids,omitempty"`
	FileSystemType string            `json:"file_system_type,omitempty"` // standard/extreme/cpfs
	ProtocolType   string            `json:"protocol_type,omitempty"`    // NFS/SMB
	Status         []string          `json:"status,omitempty"`
	VPCID          string            `json:"vpc_id,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	PageNumber     int               `json:"page_number,omitempty"`
	PageSize       int               `json:"page_size,omitempty"`
}

// NASStatus NAS 状态标准化
// 各厂商分表为大小写敏感映射(匹配厂商原始状态串), 调用方不得提前 ToLower/ToUpper
func NASStatus(provider, status string) string {
	statusMap := map[string]map[string]string{
		"aliyun": {
			"Running":  "running",
			"Creating": "creating",
			"Stopping": "stopping",
			"Stopped":  "stopped",
			"Deleting": "deleting",
		},
		// AWS EFS LifeCycleState 原始枚举值(大小写敏感, 全部小写)
		"aws": {
			"available": "running",
			"creating":  "creating",
			"deleting":  "deleting",
			"deleted":   "deleted",
			"updating":  "updating",
			"error":     "error",
		},
		"huawei": {
			// 数字状态码为 SFS Turbo 接口返回形态, 与英文状态并存;
			// 来源于 internal/shared/cloudx/huawei/sfs.go 迁移前的本地映射表
			"100":       "creating",
			"200":       "running",
			"300":       "deleting",
			"303":       "error",
			"400":       "deleted",
			"available": "running",
			"creating":  "creating",
			"deleting":  "deleting",
			"error":     "error",
		},
		"tencent": {
			// deleting/delete_failed 来源于 internal/shared/cloudx/tencent/cfs.go 迁移前的本地映射表
			"available":     "running",
			"creating":      "creating",
			"create_failed": "error",
			"deleting":      "deleting",
			"delete_failed": "error",
		},
		"volcano": {
			"Running":     "running",
			"Creating":    "creating",
			"Expanding":   "expanding",
			"Deleting":    "deleting",
			"Error":       "error",
			"DeleteError": "error",
			"Deleted":     "deleted",
			"Stopped":     "stopped",
			"Unknown":     "unknown",
		},
	}

	if m, ok := statusMap[provider]; ok {
		if s, ok := m[status]; ok {
			return s
		}
	}
	return status
}
