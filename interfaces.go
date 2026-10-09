package cloudx

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// CloudAdapter 统一云适配器接口
// 每个云厂商实现此接口，提供资产和IAM管理能力
type CloudAdapter interface {
	// GetProvider 获取云厂商类型
	GetProvider() domain.CloudProvider

	// Asset 获取资产适配器 (通用资产管理，已废弃，建议使用 ECS)
	// Deprecated: 请使用 ECS() 获取云虚拟机适配器
	Asset() AssetAdapter

	// ========== 计算资源 ==========

	// ECS 获取ECS适配器 (云虚拟机专用)
	ECS() ECSAdapter

	// SecurityGroup 获取安全组适配器
	SecurityGroup() SecurityGroupAdapter

	// Image 获取镜像适配器
	Image() ImageAdapter

	// Disk 获取云盘适配器
	Disk() DiskAdapter

	// Snapshot 获取快照适配器
	Snapshot() SnapshotAdapter

	// ========== 数据库资源 ==========

	// RDS 获取RDS适配器 (云数据库MySQL/PostgreSQL等)
	RDS() RDSAdapter

	// Redis 获取Redis适配器 (云Redis)
	Redis() RedisAdapter

	// MongoDB 获取MongoDB适配器 (云MongoDB)
	MongoDB() MongoDBAdapter

	// ========== 网络资源 ==========

	// VPC 获取VPC适配器 (虚拟私有云)
	VPC() VPCAdapter

	// EIP 获取EIP适配器 (弹性公网IP)
	EIP() EIPAdapter

	// VSwitch 获取交换机/子网适配器
	VSwitch() VSwitchAdapter

	// LB 获取负载均衡适配器 (SLB/ALB/NLB)
	LB() LBAdapter

	// CDN 获取CDN适配器 (内容分发网络)
	CDN() CDNAdapter

	// WAF 获取WAF适配器 (Web应用防火墙)
	WAF() WAFAdapter

	// DNS 获取DNS适配器 (域名解析管理)
	DNS() DNSAdapter

	// ENI 获取弹性网卡适配器 (Elastic Network Interface)
	ENI() ENIAdapter

	// ========== 存储资源 ==========

	// NAS 获取NAS适配器 (文件存储)
	NAS() NASAdapter

	// OSS 获取OSS适配器 (对象存储)
	OSS() OSSAdapter

	// ========== 中间件资源 ==========

	// Kafka 获取Kafka适配器 (消息队列)
	Kafka() KafkaAdapter

	// Elasticsearch 获取Elasticsearch适配器 (搜索服务)
	Elasticsearch() ElasticsearchAdapter

	// ========== IAM ==========

	// IAM 获取IAM适配器
	IAM() IAMAdapter

	// ========== 标签管理 ==========

	// Tag 获取标签适配器
	Tag() TagAdapter

	// ========== 资源创建与查询 ==========

	// ECSCreate 获取 ECS 创建适配器（可选，不支持创建的厂商返回 nil）
	ECSCreate() ECSCreateAdapter

	// ResourceQuery 获取资源查询适配器（用于模板创建和直接创建时的联动下拉数据）
	ResourceQuery() ResourceQueryAdapter

	// ValidateCredentials 验证凭证
	ValidateCredentials(ctx context.Context) error
}

// ============================================================================
// ECSAdapter - 云虚拟机适配器接口 (推荐使用)
// ============================================================================

// ECSAdapter ECS云主机适配器接口
// 专门用于云虚拟机的同步和管理
type ECSAdapter interface {
	// GetRegions 获取支持的地域列表
	GetRegions(ctx context.Context) ([]types.Region, error)

	// ListInstances 获取云主机实例列表
	ListInstances(ctx context.Context, region string) ([]types.ECSInstance, error)

	// GetInstance 获取单个云主机实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.ECSInstance, error)

	// ListInstancesByIDs 批量获取云主机实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ECSInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *ECSInstanceFilter) ([]types.ECSInstance, error)
}

// ECSInstanceFilter ECS实例过滤条件
type ECSInstanceFilter struct {
	// 实例ID列表
	InstanceIDs []string `json:"instance_ids,omitempty"`

	// 实例名称（支持模糊匹配）
	InstanceName string `json:"instance_name,omitempty"`

	// 状态过滤
	Status []string `json:"status,omitempty"`

	// VPC ID
	VPCID string `json:"vpc_id,omitempty"`

	// 可用区
	Zone string `json:"zone,omitempty"`

	// 标签过滤
	Tags map[string]string `json:"tags,omitempty"`

	// 分页
	PageNumber int `json:"page_number,omitempty"`
	PageSize   int `json:"page_size,omitempty"`
}

// ============================================================================
// AssetAdapter - 资产适配器接口 (已废弃)
// ============================================================================

// AssetAdapter 资产适配器接口
// Deprecated: 此接口将逐步废弃，请使用 ECSAdapter 等专用适配器
type AssetAdapter interface {
	// GetRegions 获取支持的地域列表
	GetRegions(ctx context.Context) ([]types.Region, error)

	// GetECSInstances 获取云主机实例列表
	// Deprecated: 请使用 ECSAdapter.ListInstances
	GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error)
}

// ============================================================================
// RDSAdapter - 云数据库适配器接口
// ============================================================================

// RDSAdapter RDS云数据库适配器接口
// 用于MySQL、PostgreSQL、MariaDB、SQL Server等关系型数据库
type RDSAdapter interface {
	// ListInstances 获取RDS实例列表
	ListInstances(ctx context.Context, region string) ([]types.RDSInstance, error)

	// GetInstance 获取单个RDS实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.RDSInstance, error)

	// ListInstancesByIDs 批量获取RDS实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RDSInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.RDSInstanceFilter) ([]types.RDSInstance, error)
}

// RDSMetricQuerier 可选能力:按 RDS 实例查询单日 CPU/内存/磁盘使用率 + 连接数指标。
// 指标不随同步落库,由定时任务 rds:collect_metrics 按日采集;RDSAdapter 实现方可
// 按需实现本接口(探测不到可用监控 API 的厂商不实现,采集跳过——T1 探测定案:
// 必达 aliyun/huawei/aws,volcengine 订阅未开通二期补)。
// 与 DiskMetricQuerier/NASMetricQuerier 同型:RDS 是地域性资源(实例绑定 region),
// 签名带 region——对多 region 账号按「实例 → region」逐实例查询,不做全局 region
// 推断(aws 账号 regions 配置与实盘漂移时尤须按实例真实 region,probe-report §3);
// 与 CDNMetricQuerier/OSSMetricQuerier(全局服务,签名无 region)不同。
// 签名额外带 engine(T1 探测定案,与 NAS/Disk 的差异点):aliyun 指标名按引擎前缀
// 分派(SQLServer_*)、huawei CES 维度键按引擎分键(rds_cluster_id /
// postgresql_cluster_id),engine 是适配器查询分派的必经依据,非纯展示元数据
// (probe-report §2「多引擎口径确认」)。
type RDSMetricQuerier interface {
	// GetRDSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该 RDS 实例
	// 的逐日 CPU/内存/磁盘使用率 + 连接数指标(单位归一、内存字节换算与 qc_status
	// 语义见 types.RDSMetric)。rdsID 为实例 ID(各厂商检索键);instanceName 为
	// 实例名称(展示/部分厂商检索用);region 为实例所在地域,由调用方从实例元数据
	// 取,适配器必须按该 region 调用对应监控 API;engine 为数据库引擎(适配器按其
	// 分派指标名/维度键)。
	GetRDSMetrics(ctx context.Context, rdsID, instanceName, region, engine, startDate, endDate string) ([]types.RDSMetric, error)
}

// ============================================================================
// RedisAdapter - 云Redis适配器接口
// ============================================================================

// RedisAdapter Redis云缓存适配器接口
// 用于Redis缓存服务
type RedisAdapter interface {
	// ListInstances 获取Redis实例列表
	ListInstances(ctx context.Context, region string) ([]types.RedisInstance, error)

	// GetInstance 获取单个Redis实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.RedisInstance, error)

	// ListInstancesByIDs 批量获取Redis实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.RedisInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.RedisInstanceFilter) ([]types.RedisInstance, error)
}

// ============================================================================
// MongoDBAdapter - 云MongoDB适配器接口
// ============================================================================

// MongoDBAdapter MongoDB云数据库适配器接口
// 用于MongoDB文档数据库
type MongoDBAdapter interface {
	// ListInstances 获取MongoDB实例列表
	ListInstances(ctx context.Context, region string) ([]types.MongoDBInstance, error)

	// GetInstance 获取单个MongoDB实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.MongoDBInstance, error)

	// ListInstancesByIDs 批量获取MongoDB实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.MongoDBInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.MongoDBInstanceFilter) ([]types.MongoDBInstance, error)
}

// ============================================================================
// IAMAdapter - IAM适配器接口
// ============================================================================

// IAMAdapter IAM适配器接口
type IAMAdapter interface {
	// ========== 用户管理 ==========

	// ListUsers 获取用户列表
	ListUsers(ctx context.Context) ([]*domain.CloudUser, error)

	// GetUser 获取用户详情
	GetUser(ctx context.Context, userID string) (*domain.CloudUser, error)

	// GetUserPolicies 获取用户的个人权限策略
	GetUserPolicies(ctx context.Context, userID string) ([]domain.PermissionPolicy, error)

	// CreateUser 创建用户
	CreateUser(ctx context.Context, req *types.CreateUserRequest) (*domain.CloudUser, error)

	// UpdateUserPermissions 更新用户权限
	UpdateUserPermissions(ctx context.Context, userID string, policies []domain.PermissionPolicy) error

	// DeleteUser 删除用户
	DeleteUser(ctx context.Context, userID string) error

	// ========== 用户组管理 ==========

	// ListGroups 获取用户组列表
	ListGroups(ctx context.Context) ([]*domain.UserGroup, error)

	// GetGroup 获取用户组详情
	GetGroup(ctx context.Context, groupID string) (*domain.UserGroup, error)

	// CreateGroup 创建用户组
	CreateGroup(ctx context.Context, req *types.CreateGroupRequest) (*domain.UserGroup, error)

	// UpdateGroupPolicies 更新用户组权限策略
	UpdateGroupPolicies(ctx context.Context, groupID string, policies []domain.PermissionPolicy) error

	// DeleteGroup 删除用户组
	DeleteGroup(ctx context.Context, groupID string) error

	// ListGroupUsers 获取用户组成员列表
	ListGroupUsers(ctx context.Context, groupID string) ([]*domain.CloudUser, error)

	// AddUserToGroup 将用户添加到用户组
	AddUserToGroup(ctx context.Context, groupID string, userID string) error

	// RemoveUserFromGroup 将用户从用户组移除
	RemoveUserFromGroup(ctx context.Context, groupID string, userID string) error

	// ========== 策略管理 ==========

	// ListPolicies 获取权限策略列表
	ListPolicies(ctx context.Context) ([]domain.PermissionPolicy, error)

	// GetPolicy 获取策略详情
	GetPolicy(ctx context.Context, policyID string) (*domain.PermissionPolicy, error)
}

// AdapterCreator 适配器创建函数类型
type AdapterCreator func(account *domain.CloudAccount) (CloudAdapter, error)

// ============================================================================
// VPCAdapter - VPC适配器接口
// ============================================================================

// VPCAdapter VPC虚拟私有云适配器接口
type VPCAdapter interface {
	// ListInstances 获取VPC列表
	ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error)

	// GetInstance 获取单个VPC详情
	GetInstance(ctx context.Context, region, vpcID string) (*types.VPCInstance, error)

	// ListInstancesByIDs 批量获取VPC
	ListInstancesByIDs(ctx context.Context, region string, vpcIDs []string) ([]types.VPCInstance, error)

	// GetInstanceStatus 获取VPC状态
	GetInstanceStatus(ctx context.Context, region, vpcID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取VPC列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.VPCInstanceFilter) ([]types.VPCInstance, error)
}

// ============================================================================
// EIPAdapter - 弹性公网IP适配器接口
// ============================================================================

// EIPAdapter 弹性公网IP适配器接口
type EIPAdapter interface {
	// ListInstances 获取EIP列表
	ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error)

	// GetInstance 获取单个EIP详情
	GetInstance(ctx context.Context, region, allocationID string) (*types.EIPInstance, error)

	// ListInstancesByIDs 批量获取EIP
	ListInstancesByIDs(ctx context.Context, region string, allocationIDs []string) ([]types.EIPInstance, error)

	// GetInstanceStatus 获取EIP状态
	GetInstanceStatus(ctx context.Context, region, allocationID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取EIP列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.EIPInstanceFilter) ([]types.EIPInstance, error)
}

// ============================================================================
// VSwitchAdapter - 交换机/子网适配器接口
// ============================================================================

// VSwitchAdapter 交换机/子网适配器接口
// 用于阿里云VSwitch、华为云Subnet、腾讯云Subnet、AWS Subnet、火山引擎Subnet
type VSwitchAdapter interface {
	// ListInstances 获取交换机列表
	ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error)

	// GetInstance 获取单个交换机详情
	GetInstance(ctx context.Context, region, vswitchID string) (*types.VSwitchInstance, error)

	// ListInstancesByIDs 批量获取交换机
	ListInstancesByIDs(ctx context.Context, region string, vswitchIDs []string) ([]types.VSwitchInstance, error)

	// GetInstanceStatus 获取交换机状态
	GetInstanceStatus(ctx context.Context, region, vswitchID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取交换机列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.VSwitchInstanceFilter) ([]types.VSwitchInstance, error)
}

// ============================================================================
// LBAdapter - 负载均衡适配器接口
// ============================================================================

// LBAdapter 负载均衡适配器接口
// 用于阿里云SLB/ALB/NLB、华为云ELB、腾讯云CLB、AWS ELB等
type LBAdapter interface {
	// ListInstances 获取负载均衡实例列表
	ListInstances(ctx context.Context, region string) ([]types.LBInstance, error)

	// GetInstance 获取单个负载均衡实例详情
	GetInstance(ctx context.Context, region, lbID string) (*types.LBInstance, error)

	// ListInstancesByIDs 批量获取负载均衡实例
	ListInstancesByIDs(ctx context.Context, region string, lbIDs []string) ([]types.LBInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, lbID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.LBInstanceFilter) ([]types.LBInstance, error)
}

// ============================================================================
// NASAdapter - NAS文件存储适配器接口
// ============================================================================

// NASAdapter NAS文件存储适配器接口
// 用于阿里云NAS、华为云SFS、腾讯云CFS、AWS EFS等
type NASAdapter interface {
	// ListInstances 获取NAS文件系统列表
	ListInstances(ctx context.Context, region string) ([]types.NASInstance, error)

	// GetInstance 获取单个NAS文件系统详情
	GetInstance(ctx context.Context, region, fileSystemID string) (*types.NASInstance, error)

	// ListInstancesByIDs 批量获取NAS文件系统
	ListInstancesByIDs(ctx context.Context, region string, fileSystemIDs []string) ([]types.NASInstance, error)

	// GetInstanceStatus 获取文件系统状态
	GetInstanceStatus(ctx context.Context, region, fileSystemID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取文件系统列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.NASInstanceFilter) ([]types.NASInstance, error)
}

// NASMetricQuerier 可选能力:按文件系统查询 NAS 单日容量/用量指标。
// 指标不随同步落库,由定时任务 nas:collect_metrics 按日采集;NASAdapter
// 实现方可按需实现本接口(探测不到可用监控 API 的厂商不实现,采集跳过)。
// 与 CDNMetricQuerier 不同:签名带 region——CDN 是全局服务故签名无 region,
// NAS 是地域性资源,必须按实例所在 region 调用对应厂商监控 API(华为 CES 用
// 实例真实 region,其余厂商同);对多 region 账号按「实例 → region」逐实例
// 查询,不做全局 region 推断,避免非默认 region 的实例查错地域。
type NASMetricQuerier interface {
	// GetNASMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该文件系统
	// 的逐日容量/用量指标(单位归一化与 qc_status 语义见 types.NASMetric)。
	// fsID 为文件系统 ID(各厂商检索键);fsName 为文件系统名称(展示/部分
	// 厂商检索用);region 为实例所在地域,由调用方从实例元数据取,适配器
	// 必须按该 region 调用对应监控 API。
	GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error)
}

// ============================================================================
// OSSAdapter - OSS对象存储适配器接口
// ============================================================================

// OSSAdapter OSS对象存储适配器接口
// 用于阿里云OSS、华为云OBS、腾讯云COS、AWS S3、火山引擎TOS等
type OSSAdapter interface {
	// ListBuckets 获取存储桶列表 (OSS是全局服务，region参数可选)
	ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error)

	// GetBucket 获取单个存储桶详情
	GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error)

	// GetBucketStats 获取存储桶统计信息
	GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error)

	// ListBucketsWithFilter 带过滤条件获取存储桶列表
	ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error)
}

// OSSMetricQuerier 可选能力:按存储桶查询 OSS 单日容量/对象数指标。
// 指标不随同步落库,由定时任务 oss:collect_metrics 按日采集;OSSAdapter
// 实现方可按需实现本接口(探测不到可用监控 API 的厂商不实现,采集跳过)。
// 与 NASMetricQuerier 不同:签名不带 region——OSS 是全局服务(ListBuckets 的
// region 参数可选,各厂商实现均有 defaultRegion 回退),与 CDNMetricQuerier 同型:
// bucket_name 全局唯一即检索键,唯一键用 bucket_name 类比 CDN 的 domain。
type OSSMetricQuerier interface {
	// GetOSSMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该存储桶
	// 的逐日容量/对象数指标(单位归一化与 qc_status 语义见 types.OSSMetric)。
	// bucketName 为存储桶名称(各厂商检索键,全局唯一)。
	GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error)
}

// ============================================================================
// KafkaAdapter - Kafka消息队列适配器接口
// ============================================================================

// KafkaAdapter Kafka消息队列适配器接口
// 用于阿里云Kafka、华为云DMS Kafka、腾讯云CKafka、AWS MSK等
type KafkaAdapter interface {
	// ListInstances 获取Kafka实例列表
	ListInstances(ctx context.Context, region string) ([]types.KafkaInstance, error)

	// GetInstance 获取单个Kafka实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.KafkaInstance, error)

	// ListInstancesByIDs 批量获取Kafka实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.KafkaInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.KafkaInstanceFilter) ([]types.KafkaInstance, error)
}

// ============================================================================
// ElasticsearchAdapter - Elasticsearch搜索服务适配器接口
// ============================================================================

// ElasticsearchAdapter Elasticsearch搜索服务适配器接口
// 用于阿里云ES、华为云CSS、腾讯云ES、AWS OpenSearch等
type ElasticsearchAdapter interface {
	// ListInstances 获取Elasticsearch实例列表
	ListInstances(ctx context.Context, region string) ([]types.ElasticsearchInstance, error)

	// GetInstance 获取单个Elasticsearch实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.ElasticsearchInstance, error)

	// ListInstancesByIDs 批量获取Elasticsearch实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.ElasticsearchInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.ElasticsearchInstanceFilter) ([]types.ElasticsearchInstance, error)
}

// ============================================================================
// SecurityGroupAdapter - 安全组适配器接口
// ============================================================================

// SecurityGroupAdapter 安全组适配器接口
type SecurityGroupAdapter interface {
	// ListInstances 获取安全组列表
	ListInstances(ctx context.Context, region string) ([]types.SecurityGroupInstance, error)

	// GetInstance 获取单个安全组详情 (包含规则)
	GetInstance(ctx context.Context, region, securityGroupID string) (*types.SecurityGroupInstance, error)

	// ListInstancesByIDs 批量获取安全组
	ListInstancesByIDs(ctx context.Context, region string, securityGroupIDs []string) ([]types.SecurityGroupInstance, error)

	// ListInstancesWithFilter 带过滤条件获取安全组列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.SecurityGroupFilter) ([]types.SecurityGroupInstance, error)

	// GetSecurityGroupRules 获取安全组规则
	GetSecurityGroupRules(ctx context.Context, region, securityGroupID string) ([]types.SecurityGroupRule, error)

	// ListByInstanceID 获取实例关联的安全组
	ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SecurityGroupInstance, error)
}

// ============================================================================
// ImageAdapter - 镜像适配器接口
// ============================================================================

// ImageAdapter 镜像适配器接口
type ImageAdapter interface {
	// ListInstances 获取镜像列表
	ListInstances(ctx context.Context, region string) ([]types.ImageInstance, error)

	// GetInstance 获取单个镜像详情
	GetInstance(ctx context.Context, region, imageID string) (*types.ImageInstance, error)

	// ListInstancesByIDs 批量获取镜像
	ListInstancesByIDs(ctx context.Context, region string, imageIDs []string) ([]types.ImageInstance, error)

	// ListInstancesWithFilter 带过滤条件获取镜像列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.ImageFilter) ([]types.ImageInstance, error)
}

// ============================================================================
// DiskAdapter - 云盘适配器接口
// ============================================================================

// DiskAdapter 云盘适配器接口
type DiskAdapter interface {
	// ListInstances 获取云盘列表
	ListInstances(ctx context.Context, region string) ([]types.DiskInstance, error)

	// GetInstance 获取单个云盘详情
	GetInstance(ctx context.Context, region, diskID string) (*types.DiskInstance, error)

	// ListInstancesByIDs 批量获取云盘
	ListInstancesByIDs(ctx context.Context, region string, diskIDs []string) ([]types.DiskInstance, error)

	// GetInstanceStatus 获取云盘状态
	GetInstanceStatus(ctx context.Context, region, diskID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取云盘列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.DiskFilter) ([]types.DiskInstance, error)

	// ListByInstanceID 获取实例挂载的云盘
	ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.DiskInstance, error)
}

// DiskMetricQuerier 可选能力:按云盘查询 Disk 单日使用率/IOPS/吞吐指标。
// 指标不随同步落库,由定时任务 disk:collect_metrics 按日采集;DiskAdapter
// 实现方可按需实现本接口(探测不到可用监控 API 的厂商不实现,采集跳过)。
// 与 NASMetricQuerier 同型:云硬盘是地域性资源(disk_id 绑定 region),签名
// 带 region——对多 region 账号按「实例 → region」逐盘查询,不做全局 region
// 推断(aws 账号 regions 配置与实盘不一致时尤须按实例真实 region,见
// disk-ops-insight probe-report §5);与 CDNMetricQuerier/OSSMetricQuerier
// (全局服务,签名无 region)不同。
type DiskMetricQuerier interface {
	// GetDiskMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内该云盘
	// 的逐日使用率/IOPS/吞吐指标(单位归一、usage_scope 口径标注与 qc_status
	// 语义见 types.DiskMetric)。diskID 为云盘 ID(各厂商检索键);diskName 为
	// 云盘名称(展示/部分厂商检索用);region 为实例所在地域,由调用方从实例
	// 元数据取,适配器必须按该 region 调用对应监控 API。
	GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error)
}

// ============================================================================
// SnapshotAdapter - 磁盘快照适配器接口
// ============================================================================

// SnapshotAdapter 磁盘快照适配器接口
type SnapshotAdapter interface {
	// ListInstances 获取快照列表
	ListInstances(ctx context.Context, region string) ([]types.SnapshotInstance, error)

	// GetInstance 获取单个快照详情
	GetInstance(ctx context.Context, region, snapshotID string) (*types.SnapshotInstance, error)

	// ListInstancesByIDs 批量获取快照
	ListInstancesByIDs(ctx context.Context, region string, snapshotIDs []string) ([]types.SnapshotInstance, error)

	// GetInstanceStatus 获取快照状态
	GetInstanceStatus(ctx context.Context, region, snapshotID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取快照列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.SnapshotFilter) ([]types.SnapshotInstance, error)

	// ListByDiskID 获取磁盘的快照列表
	ListByDiskID(ctx context.Context, region, diskID string) ([]types.SnapshotInstance, error)

	// ListByInstanceID 获取实例的快照列表 (系统盘+数据盘)
	ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.SnapshotInstance, error)
}

// ============================================================================
// CDNAdapter - CDN内容分发网络适配器接口
// ============================================================================

// CDNAdapter CDN内容分发网络适配器接口
// 用于阿里云CDN、华为云CDN、腾讯云CDN、AWS CloudFront、火山引擎CDN等
type CDNAdapter interface {
	// ListInstances 获取CDN加速域名列表
	ListInstances(ctx context.Context, region string) ([]types.CDNInstance, error)

	// GetInstance 获取单个CDN加速域名详情
	GetInstance(ctx context.Context, region, domainName string) (*types.CDNInstance, error)

	// ListInstancesByIDs 批量获取CDN加速域名
	ListInstancesByIDs(ctx context.Context, region string, domainNames []string) ([]types.CDNInstance, error)

	// GetInstanceStatus 获取域名状态
	GetInstanceStatus(ctx context.Context, region, domainName string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取域名列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.CDNInstanceFilter) ([]types.CDNInstance, error)
}

// CDNCacheQuerier 可选能力:按域名实时查询 CDN 缓存配置。
// 缓存规则不随同步落库,详情页打开时按需调用(见 web.GetCDNCacheConfig);
// CDNAdapter 实现方可按需实现本接口,未实现时前端展示"暂无"。
type CDNCacheQuerier interface {
	// GetCacheConfig 查询缓存规则
	// domainName 为加速域名(多数厂商的检索键);domainID 为域名/分发 ID
	// (AWS CloudFront 仅认 ID)。两者至少传一。
	GetCacheConfig(ctx context.Context, domainName, domainID string) ([]types.CDNCacheRule, error)
}

// CDNSettingsQuerier 可选能力:按域名实时查询功能配置全景(性能优化/
// 访问控制/流量限制/HTTPS/重定向/回源等),详情页按需调用。
type CDNSettingsQuerier interface {
	GetDomainSettings(ctx context.Context, domainName, domainID string) (*types.CDNDomainSettings, error)
}

// CDNMetricQuerier 可选能力:按域名查询 CDN 单日带宽/流量/命中率指标。
// 指标不随同步落库,由定时任务 cdn:collect_metrics 按日采集;CDNAdapter
// 实现方可按需实现本接口(探测不到可用监控 API 的厂商不实现,采集跳过)。
type CDNMetricQuerier interface {
	// GetDomainMetrics 查询 [startDate, endDate](含两端,YYYY-MM-DD)内
	// 该域名的逐日指标;命中率为当日均值/末端值,厂商未提供时为 -1。
	// domainName 为加速域名(多数厂商的检索键);domainID 为域名/分发 ID
	// (AWS CloudFront 仅认 ID)。两者至少传一。
	GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error)
}

// ============================================================================
// WAFAdapter - WAF Web应用防火墙适配器接口
// ============================================================================

// WAFAdapter WAF Web应用防火墙适配器接口
// 用于阿里云WAF、华为云WAF、腾讯云WAF、AWS WAF、火山引擎WAF等
type WAFAdapter interface {
	// ListInstances 获取WAF实例列表
	ListInstances(ctx context.Context, region string) ([]types.WAFInstance, error)

	// GetInstance 获取单个WAF实例详情
	GetInstance(ctx context.Context, region, instanceID string) (*types.WAFInstance, error)

	// ListInstancesByIDs 批量获取WAF实例
	ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.WAFInstance, error)

	// GetInstanceStatus 获取实例状态
	GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取实例列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error)
}

// ============================================================================
// DNSAdapter - DNS 域名解析管理适配器接口
// ============================================================================

// DNSAdapter DNS 解析管理适配器接口
// 用于阿里云 Alidns、AWS Route53、华为云 DNS、腾讯云 DNSPod、火山引擎 TrafficRoute 等
type DNSAdapter interface {
	// ListDomains 查询托管域名列表
	ListDomains(ctx context.Context) ([]types.DNSDomain, error)

	// ListRecords 查询域名下解析记录列表。
	// domain 同时携带域名名(DomainName)与厂商 zone 标识(DomainID):
	// 腾讯 DNSPod / 阿里 AliDNS 按 DomainName 查询;AWS Route53 / 华为云按 DomainID 查询。
	ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error)

	// GetRecord 查询单条解析记录详情
	GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error)

	// CreateRecord 创建解析记录
	CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error)

	// UpdateRecord 修改解析记录
	UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error)

	// DeleteRecord 删除解析记录
	DeleteRecord(ctx context.Context, domain, recordID string) error
}

// ============================================================================
// ECSCreateAdapter - ECS 实例创建适配器接口
// ============================================================================

// ECSCreateAdapter ECS 实例创建适配器接口
// 每个云厂商实现此接口，提供 ECS 实例创建能力
type ECSCreateAdapter interface {
	// CreateInstances 创建 ECS 实例，返回创建的实例 ID 列表
	CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error)
}

// ============================================================================
// ResourceQueryAdapter - 云资源查询适配器接口
// ============================================================================

// ResourceQueryAdapter 云资源查询适配器接口
// 用于模板创建和直接创建时查询云厂商可用资源（联动下拉数据）
type ResourceQueryAdapter interface {
	// ListAvailableInstanceTypes 查询可用实例规格
	ListAvailableInstanceTypes(ctx context.Context, region string) ([]types.InstanceTypeInfo, error)

	// ListAvailableImages 查询可用镜像
	ListAvailableImages(ctx context.Context, region string) ([]types.ImageInfo, error)

	// ListVPCs 查询 VPC 列表
	ListVPCs(ctx context.Context, region string) ([]types.VPCInfo, error)

	// ListSubnets 查询子网列表
	ListSubnets(ctx context.Context, region, vpcID string) ([]types.SubnetInfo, error)

	// ListSecurityGroups 查询安全组列表
	ListSecurityGroups(ctx context.Context, region, vpcID string) ([]types.SecurityGroupInfo, error)
}

// ============================================================================
// TagAdapter - 标签适配器接口
// ============================================================================

// ============================================================================
// ENIAdapter - 弹性网卡适配器接口
// ============================================================================

// ENIAdapter 弹性网卡适配器接口
// 用于阿里云ENI、AWS ENI、华为云Port、腾讯云ENI、火山引擎ENI等
type ENIAdapter interface {
	// ListInstances 获取弹性网卡列表
	ListInstances(ctx context.Context, region string) ([]types.ENIInstance, error)

	// GetInstance 获取单个弹性网卡详情
	GetInstance(ctx context.Context, region, eniID string) (*types.ENIInstance, error)

	// ListInstancesByIDs 批量获取弹性网卡
	ListInstancesByIDs(ctx context.Context, region string, eniIDs []string) ([]types.ENIInstance, error)

	// GetInstanceStatus 获取弹性网卡状态
	GetInstanceStatus(ctx context.Context, region, eniID string) (string, error)

	// ListInstancesWithFilter 带过滤条件获取弹性网卡列表
	ListInstancesWithFilter(ctx context.Context, region string, filter *types.ENIInstanceFilter) ([]types.ENIInstance, error)

	// ListByInstanceID 获取实例绑定的弹性网卡
	ListByInstanceID(ctx context.Context, region, instanceID string) ([]types.ENIInstance, error)
}

// TagAdapter 标签适配器接口
// 用于阿里云 Tag API、AWS Resource Groups Tagging API、华为云 TMS、腾讯云标签 API、火山引擎标签 API
type TagAdapter interface {
	// ListTagKeys 查询标签键列表
	ListTagKeys(ctx context.Context, region string) ([]string, error)

	// ListTagValues 查询指定标签键的值列表
	ListTagValues(ctx context.Context, region, key string) ([]string, error)

	// GetResourceTags 查询资源绑定的标签
	GetResourceTags(ctx context.Context, region, resourceType, resourceID string) (map[string]string, error)

	// TagResource 为资源绑定标签
	TagResource(ctx context.Context, region, resourceType, resourceID string, tags map[string]string) error

	// UntagResource 解绑资源标签
	UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error

	// ListResourcesByTag 按标签查询资源列表
	ListResourcesByTag(ctx context.Context, region, key, value string) ([]TaggedResource, error)
}

// TaggedResource 标签关联的资源
type TaggedResource struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Region       string `json:"region"`
}
