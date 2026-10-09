package types

import "time"

// KafkaInstance Kafka 消息队列实例
type KafkaInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`   // 实例ID
	InstanceName string `json:"instance_name"` // 实例名称
	Status       string `json:"status"`        // 状态: running/creating/stopped/error
	Region       string `json:"region"`        // 地域
	Zone         string `json:"zone"`          // 可用区
	Description  string `json:"description"`   // 描述

	// 版本信息
	Version     string `json:"version"`      // Kafka版本: 2.2.0, 2.6.0, 3.x等
	SpecType    string `json:"spec_type"`    // 规格类型: standard/professional
	MessageType string `json:"message_type"` // 消息类型: normal/order/transaction

	// 配置信息
	TopicCount       int    `json:"topic_count"`       // Topic数量
	TopicQuota       int    `json:"topic_quota"`       // Topic配额
	PartitionCount   int    `json:"partition_count"`   // 分区数量
	PartitionQuota   int    `json:"partition_quota"`   // 分区配额
	ConsumerGroups   int    `json:"consumer_groups"`   // 消费组数量
	MaxMessageSize   int    `json:"max_message_size"`  // 最大消息大小(KB)
	MessageRetention int    `json:"message_retention"` // 消息保留时间(小时)
	DiskSize         int64  `json:"disk_size"`         // 磁盘大小(GB)
	DiskUsed         int64  `json:"disk_used"`         // 已用磁盘(GB)
	DiskType         string `json:"disk_type"`         // 磁盘类型: cloud_ssd/cloud_efficiency

	// 性能配置
	Bandwidth    int `json:"bandwidth"`     // 带宽(MB/s)
	TPS          int `json:"tps"`           // TPS上限
	IOMax        int `json:"io_max"`        // 最大IO
	BrokerCount  int `json:"broker_count"`  // Broker节点数
	ZookeeperNum int `json:"zookeeper_num"` // Zookeeper节点数

	// 网络信息
	VPCID            string   `json:"vpc_id"`            // VPC ID
	VSwitchID        string   `json:"vswitch_id"`        // 交换机ID
	SecurityGroupID  string   `json:"security_group_id"` // 安全组ID
	EndpointType     string   `json:"endpoint_type"`     // 接入点类型: vpc/public
	BootstrapServers string   `json:"bootstrap_servers"` // 接入点地址
	SSLEndpoint      string   `json:"ssl_endpoint"`      // SSL接入点
	SASLEndpoint     string   `json:"sasl_endpoint"`     // SASL接入点
	ZoneIDs          []string `json:"zone_ids"`          // 多可用区部署

	// 安全配置
	SSLEnabled  bool   `json:"ssl_enabled"`  // 是否启用SSL
	SASLEnabled bool   `json:"sasl_enabled"` // 是否启用SASL
	ACLEnabled  bool   `json:"acl_enabled"`  // 是否启用ACL
	EncryptType int    `json:"encrypt_type"` // 加密类型
	KMSKeyID    string `json:"kms_key_id"`   // KMS密钥ID

	// 计费信息
	ChargeType   string    `json:"charge_type"`   // 付费类型: PrePaid/PostPaid
	CreationTime time.Time `json:"creation_time"` // 创建时间
	ExpiredTime  time.Time `json:"expired_time"`  // 过期时间

	// 项目/资源组信息
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ResourceGroupID string `json:"resource_group_id"`

	// 标签
	Tags     map[string]string `json:"tags"`
	Provider string            `json:"provider"` // 云厂商标识
}

// KafkaInstanceFilter Kafka 过滤条件
type KafkaInstanceFilter struct {
	InstanceIDs  []string          `json:"instance_ids,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	Status       []string          `json:"status,omitempty"`
	VPCID        string            `json:"vpc_id,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	PageNumber   int               `json:"page_number,omitempty"`
	PageSize     int               `json:"page_size,omitempty"`
}

// KafkaStatus Kafka 状态标准化
func KafkaStatus(provider, status string) string {
	statusMap := map[string]map[string]string{
		"aliyun": {
			"0":  "creating",
			"1":  "running",
			"2":  "stopped",
			"3":  "starting",
			"4":  "stopping",
			"5":  "running", // 服务中
			"6":  "upgrading",
			"7":  "deleting",
			"15": "expired",
		},
		"huawei": {
			"CREATING":   "creating",
			"RUNNING":    "running",
			"FAULTY":     "error",
			"RESTARTING": "restarting",
			"RESIZING":   "resizing",
			"FROZEN":     "frozen",
		},
		"tencent": {
			"0": "creating",
			"1": "running",
			"2": "deleting",
			"5": "isolated", // 隔离中
		},
		"aws": {
			"CREATING":  "creating",
			"ACTIVE":    "running",
			"REBOOTING": "restarting",
			"UPDATING":  "updating",
			"DELETING":  "deleting",
			"FAILED":    "error",
		},
		"volcano": {
			"Running":  "running",
			"Creating": "creating",
			"Deleting": "deleting",
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
