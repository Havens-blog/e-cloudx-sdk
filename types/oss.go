package types

import "time"

// OSSBucket OSS 对象存储桶
type OSSBucket struct {
	// 基本信息
	BucketName   string    `json:"bucket_name"`   // 存储桶名称
	Region       string    `json:"region"`        // 地域
	Location     string    `json:"location"`      // 位置 (如 oss-cn-hangzhou)
	CreationTime time.Time `json:"creation_time"` // 创建时间

	// 存储类型
	StorageClass string `json:"storage_class"` // 存储类型: Standard/IA/Archive/ColdArchive/DeepColdArchive

	// 访问控制
	ACL                    string `json:"acl"`                      // 访问权限: private/public-read/public-read-write
	Versioning             string `json:"versioning"`               // 版本控制: Enabled/Suspended
	CrossRegionReplication bool   `json:"cross_region_replication"` // 跨区域复制

	// 容量统计
	ObjectCount     int64 `json:"object_count"`      // 对象数量
	StorageSize     int64 `json:"storage_size"`      // 存储大小 (Bytes)
	StandardSize    int64 `json:"standard_size"`     // 标准存储大小
	IASize          int64 `json:"ia_size"`           // 低频存储大小
	ArchiveSize     int64 `json:"archive_size"`      // 归档存储大小
	ColdArchiveSize int64 `json:"cold_archive_size"` // 冷归档存储大小

	// 网络配置
	ExtranetEndpoint     string `json:"extranet_endpoint"`     // 外网访问域名
	IntranetEndpoint     string `json:"intranet_endpoint"`     // 内网访问域名
	TransferAcceleration bool   `json:"transfer_acceleration"` // 传输加速

	// 安全配置
	ServerSideEncryption string `json:"server_side_encryption"` // 服务端加密: None/AES256/KMS
	KMSKeyID             string `json:"kms_key_id"`             // KMS密钥ID
	BlockPublicAccess    bool   `json:"block_public_access"`    // 阻止公共访问

	// 生命周期
	LifecycleRuleCount int `json:"lifecycle_rule_count"` // 生命周期规则数量

	// 静态网站
	WebsiteEnabled bool   `json:"website_enabled"` // 是否启用静态网站
	IndexDocument  string `json:"index_document"`  // 默认首页
	ErrorDocument  string `json:"error_document"`  // 错误页面

	// 日志
	LoggingEnabled bool   `json:"logging_enabled"` // 是否启用日志
	LoggingBucket  string `json:"logging_bucket"`  // 日志存储桶
	LoggingPrefix  string `json:"logging_prefix"`  // 日志前缀

	// CORS
	CORSRuleCount int `json:"cors_rule_count"` // CORS规则数量

	// 标签
	Tags     map[string]string `json:"tags"`     // 标签
	Provider string            `json:"provider"` // 云厂商
}

// OSSBucketFilter OSS 过滤条件
type OSSBucketFilter struct {
	BucketNames  []string          `json:"bucket_names,omitempty"`
	Prefix       string            `json:"prefix,omitempty"`        // 名称前缀
	StorageClass string            `json:"storage_class,omitempty"` // 存储类型
	Tags         map[string]string `json:"tags,omitempty"`
	PageNumber   int               `json:"page_number,omitempty"`
	PageSize     int               `json:"page_size,omitempty"`
}

// OSSBucketStats 存储桶统计信息
type OSSBucketStats struct {
	BucketName       string `json:"bucket_name"`
	ObjectCount      int64  `json:"object_count"`       // 对象数量
	StorageSize      int64  `json:"storage_size"`       // 总存储大小 (Bytes)
	MultipartCount   int64  `json:"multipart_count"`    // 分片上传数量
	LiveChannelCount int64  `json:"live_channel_count"` // 直播频道数量
	LastModifiedTime int64  `json:"last_modified_time"` // 最后修改时间
}
