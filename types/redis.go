package types

// RedisInstance 云Redis实例（通用格式）
type RedisInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	Status       string `json:"status"`
	Region       string `json:"region"`
	Zone         string `json:"zone"`

	// Redis信息
	EngineVersion string `json:"engine_version"` // 5.0, 6.0, 7.0 等
	InstanceClass string `json:"instance_class"` // 实例规格
	Architecture  string `json:"architecture"`   // standard, cluster, rwsplit

	// 配置信息
	Capacity    int `json:"capacity"`    // 容量 MB
	Bandwidth   int `json:"bandwidth"`   // 带宽 Mbps
	Connections int `json:"connections"` // 最大连接数
	QPS         int `json:"qps"`         // 每秒查询数
	ShardCount  int `json:"shard_count"` // 分片数 (集群版)

	// 网络信息
	ConnectionDomain string `json:"connection_domain"` // 连接地址
	Port             int    `json:"port"`              // 端口
	VPCID            string `json:"vpc_id"`
	VSwitchID        string `json:"vswitch_id"`
	PrivateIP        string `json:"private_ip"`

	// 高可用信息
	NodeType      string `json:"node_type"`      // single, double, readone, readthree
	ReplicaCount  int    `json:"replica_count"`  // 副本数
	SecondaryZone string `json:"secondary_zone"` // 备可用区

	// 计费信息
	ChargeType   string `json:"charge_type"`
	CreationTime string `json:"creation_time"`
	ExpiredTime  string `json:"expired_time"`

	// 安全信息
	SecurityIPList []string `json:"security_ip_list"`
	SSLEnabled     bool     `json:"ssl_enabled"`
	Password       bool     `json:"password"` // 是否设置密码

	// 项目/资源组信息
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags        map[string]string `json:"tags"`
	Description string            `json:"description"`
	Provider    string            `json:"provider"`
}

// RedisInstanceFilter Redis实例过滤条件
type RedisInstanceFilter struct {
	InstanceIDs  []string          `json:"instance_ids,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	Status       []string          `json:"status,omitempty"`
	Architecture string            `json:"architecture,omitempty"`
	VPCID        string            `json:"vpc_id,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	PageNumber   int               `json:"page_number,omitempty"`
	PageSize     int               `json:"page_size,omitempty"`
}
