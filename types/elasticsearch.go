package types

import "time"

// ElasticsearchInstance Elasticsearch 搜索服务实例
type ElasticsearchInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`   // 实例ID
	InstanceName string `json:"instance_name"` // 实例名称
	Status       string `json:"status"`        // 状态: running/creating/stopped/error
	Region       string `json:"region"`        // 地域
	Zone         string `json:"zone"`          // 可用区
	Description  string `json:"description"`   // 描述

	// 版本信息
	Version     string `json:"version"`      // ES版本: 7.10, 8.x等
	EngineType  string `json:"engine_type"`  // 引擎类型: elasticsearch/opensearch
	LicenseType string `json:"license_type"` // 许可类型: oss/basic/platinum

	// 节点配置
	NodeCount    int    `json:"node_count"`     // 数据节点数量
	NodeSpec     string `json:"node_spec"`      // 数据节点规格
	NodeCPU      int    `json:"node_cpu"`       // 数据节点CPU
	NodeMemory   int    `json:"node_memory"`    // 数据节点内存(GB)
	NodeDiskSize int    `json:"node_disk_size"` // 数据节点磁盘(GB)
	NodeDiskType string `json:"node_disk_type"` // 数据节点磁盘类型
	MasterCount  int    `json:"master_count"`   // 专用主节点数量
	MasterSpec   string `json:"master_spec"`    // 专用主节点规格
	ClientCount  int    `json:"client_count"`   // 协调节点数量
	ClientSpec   string `json:"client_spec"`    // 协调节点规格
	WarmCount    int    `json:"warm_count"`     // 冷数据节点数量
	WarmSpec     string `json:"warm_spec"`      // 冷数据节点规格
	WarmDiskSize int    `json:"warm_disk_size"` // 冷数据节点磁盘(GB)
	KibanaCount  int    `json:"kibana_count"`   // Kibana节点数量
	KibanaSpec   string `json:"kibana_spec"`    // Kibana节点规格

	// 存储信息
	TotalDiskSize int64 `json:"total_disk_size"` // 总磁盘大小(GB)
	UsedDiskSize  int64 `json:"used_disk_size"`  // 已用磁盘(GB)
	IndexCount    int   `json:"index_count"`     // 索引数量
	DocCount      int64 `json:"doc_count"`       // 文档数量
	ShardCount    int   `json:"shard_count"`     // 分片数量

	// 网络信息
	VPCID              string `json:"vpc_id"`               // VPC ID
	VSwitchID          string `json:"vswitch_id"`           // 交换机ID
	SecurityGroupID    string `json:"security_group_id"`    // 安全组ID
	PrivateEndpoint    string `json:"private_endpoint"`     // 内网访问地址
	PublicEndpoint     string `json:"public_endpoint"`      // 公网访问地址
	KibanaEndpoint     string `json:"kibana_endpoint"`      // Kibana访问地址
	KibanaPrivateURL   string `json:"kibana_private_url"`   // Kibana内网地址
	KibanaPublicURL    string `json:"kibana_public_url"`    // Kibana公网地址
	Port               int    `json:"port"`                 // 访问端口
	EnablePublicAccess bool   `json:"enable_public_access"` // 是否开启公网访问

	// 安全配置
	SSLEnabled       bool     `json:"ssl_enabled"`       // 是否启用HTTPS
	AuthEnabled      bool     `json:"auth_enabled"`      // 是否启用认证
	EncryptType      int      `json:"encrypt_type"`      // 加密类型
	KMSKeyID         string   `json:"kms_key_id"`        // KMS密钥ID
	WhitelistEnabled bool     `json:"whitelist_enabled"` // 是否启用白名单
	WhitelistIPs     []string `json:"whitelist_ips"`     // 白名单IP列表

	// 高可用配置
	ZoneCount       int      `json:"zone_count"`        // 可用区数量
	ZoneIDs         []string `json:"zone_ids"`          // 可用区列表
	EnableHA        bool     `json:"enable_ha"`         // 是否启用高可用
	EnableAutoScale bool     `json:"enable_auto_scale"` // 是否启用自动扩缩容

	// 计费信息
	ChargeType   string    `json:"charge_type"`   // 付费类型: PrePaid/PostPaid
	CreationTime time.Time `json:"creation_time"` // 创建时间
	ExpiredTime  time.Time `json:"expired_time"`  // 过期时间
	UpdateTime   time.Time `json:"update_time"`   // 更新时间

	// 项目/资源组信息
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ResourceGroupID string `json:"resource_group_id"`

	// 标签
	Tags     map[string]string `json:"tags"`
	Provider string            `json:"provider"` // 云厂商标识
}

// ElasticsearchInstanceFilter Elasticsearch 过滤条件
type ElasticsearchInstanceFilter struct {
	InstanceIDs  []string          `json:"instance_ids,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	Status       []string          `json:"status,omitempty"`
	Version      string            `json:"version,omitempty"`
	VPCID        string            `json:"vpc_id,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	PageNumber   int               `json:"page_number,omitempty"`
	PageSize     int               `json:"page_size,omitempty"`
}

// ElasticsearchStatus Elasticsearch 状态标准化
func ElasticsearchStatus(provider, status string) string {
	statusMap := map[string]map[string]string{
		"aliyun": {
			"active":     "running",
			"activating": "creating",
			"inactive":   "stopped",
			"invalid":    "error",
		},
		"huawei": {
			"100": "creating",
			"200": "running",
			"303": "error",
			"400": "deleted",
		},
		"tencent": {
			"0":  "creating",
			"1":  "running",
			"2":  "stopped",
			"-1": "error",
			"-2": "deleting",
		},
		"aws": {
			"CREATING":                    "creating",
			"ACTIVE":                      "running",
			"MODIFYING":                   "updating",
			"UPGRADING":                   "upgrading",
			"DELETING":                    "deleting",
			"DELETED":                     "deleted",
			"VPC_ENDPOINT_LIMIT_EXCEEDED": "error",
		},
		"volcano": {
			"Running":  "running",
			"Creating": "creating",
			"Deleting": "deleting",
			"Updating": "updating",
			"Error":    "error",
		},
	}

	if m, ok := statusMap[provider]; ok {
		if s, ok := m[status]; ok {
			return s
		}
	}
	return status
}
