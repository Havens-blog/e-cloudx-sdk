package domain

import (
	"fmt"
	"time"
)

// CloudAccountStatus 云账号状态枚举
type CloudAccountStatus string

const (
	CloudAccountStatusActive   CloudAccountStatus = "active"   // 活跃状态
	CloudAccountStatusDisabled CloudAccountStatus = "disabled" // 已禁用
	CloudAccountStatusError    CloudAccountStatus = "error"    // 错误状态
	CloudAccountStatusTesting  CloudAccountStatus = "testing"  // 测试中
)

// CloudProvider 云厂商枚举
type CloudProvider string

const (
	CloudProviderAliyun     CloudProvider = "aliyun"     // 阿里云
	CloudProviderAWS        CloudProvider = "aws"        // Amazon Web Services
	CloudProviderAzure      CloudProvider = "azure"      // Microsoft Azure
	CloudProviderTencent    CloudProvider = "tencent"    // 腾讯云
	CloudProviderHuawei     CloudProvider = "huawei"     // 华为云
	CloudProviderVolcano    CloudProvider = "volcano"    // 火山云 (别名)
	CloudProviderVolcengine CloudProvider = "volcengine" // 火山引擎
)

// PrimaryAssetProviders 资产能力的主云厂商规范清单（展示层单源）。
//
// 用于 mcp enum、topology ValidProviders 等展示/校验清单，避免各层各自硬编码
// 导致"azure 有无"类漂移。注意：本清单是"资产能力"子集，不含 azure——azure
// 当前仅证书发现（无 CloudAdapter）。若 azure 接入 CloudAdapter，必须先在本清单
// 补 azure（cloudx registry_test 有断言兜底），再收尾展示层。
// volcengine 是 volcano 的别名键，展示层只保留 volcano，故不在此列。
var PrimaryAssetProviders = []CloudProvider{
	CloudProviderAliyun,
	CloudProviderAWS,
	CloudProviderTencent,
	CloudProviderHuawei,
	CloudProviderVolcano,
}

// Environment 环境枚举
type Environment string

const (
	EnvironmentProduction  Environment = "production"  // 生产环境
	EnvironmentStaging     Environment = "staging"     // 预发环境
	EnvironmentDevelopment Environment = "development" // 开发环境
)

// CloudAccountConfig 云账号配置信息
type CloudAccountConfig struct {
	EnableAutoSync       bool     `json:"enable_auto_sync" bson:"enable_auto_sync"`             // 是否启用自动同步
	SyncInterval         int64    `json:"sync_interval" bson:"sync_interval"`                   // 同步间隔(分钟)
	ReadOnly             bool     `json:"read_only" bson:"read_only"`                           // 只读权限
	ShowSubAccounts      bool     `json:"show_sub_accounts" bson:"show_sub_accounts"`           // 显示子账号
	EnableCostMonitoring bool     `json:"enable_cost_monitoring" bson:"enable_cost_monitoring"` // 启用成本监控
	SupportedRegions     []string `json:"supported_regions" bson:"supported_regions"`           // 支持的地域列表
	SupportedAssetTypes  []string `json:"supported_asset_types" bson:"supported_asset_types"`   // 支持的资产类型
}

// CloudAccount 云账号领域模型
type CloudAccount struct {
	ID              int64              `json:"id" bson:"id"`                               // 账号ID
	Name            string             `json:"name" bson:"name"`                           // 账号名称
	Provider        CloudProvider      `json:"provider" bson:"provider"`                   // 云厂商
	Environment     Environment        `json:"environment" bson:"environment"`             // 环境
	AccessKeyID     string             `json:"access_key_id" bson:"access_key_id"`         // 访问密钥ID
	AccessKeySecret string             `json:"access_key_secret" bson:"access_key_secret"` // 访问密钥Secret (加密存储)
	Regions         []string           `json:"regions" bson:"regions"`                     // 支持的地域列表
	Description     string             `json:"description" bson:"description"`             // 描述信息
	Status          CloudAccountStatus `json:"status" bson:"status"`                       // 账号状态
	Config          CloudAccountConfig `json:"config" bson:"config"`                       // 配置信息
	TenantID        int64              `json:"tenant_id" bson:"tenant_id"`                 // 租户ID
	LastSyncTime    *time.Time         `json:"last_sync_time" bson:"last_sync_time"`       // 最后同步时间
	LastTestTime    *time.Time         `json:"last_test_time" bson:"last_test_time"`       // 最后测试时间
	AssetCount      int64              `json:"asset_count" bson:"asset_count"`             // 资产数量
	ErrorMessage    string             `json:"error_message" bson:"error_message"`         // 错误信息
	CreateTime      time.Time          `json:"create_time" bson:"create_time"`             // 创建时间
	UpdateTime      time.Time          `json:"update_time" bson:"update_time"`             // 更新时间
	CTime           int64              `json:"ctime" bson:"ctime"`                         // 创建时间戳
	UTime           int64              `json:"utime" bson:"utime"`                         // 更新时间戳
}

// CloudAccountFilter 云账号查询过滤器
type CloudAccountFilter struct {
	Provider    CloudProvider      `json:"provider"`
	Environment Environment        `json:"environment"`
	Status      CloudAccountStatus `json:"status"`
	TenantID    int64              `json:"tenant_id"`
	Offset      int64              `json:"offset"`
	Limit       int64              `json:"limit"`
}

// CreateCloudAccountRequest 创建云账号请求
type CreateCloudAccountRequest struct {
	Name            string             `json:"name" binding:"required,min=1,max=100"`
	Provider        CloudProvider      `json:"provider" binding:"required"`
	Environment     Environment        `json:"environment" binding:"required"`
	AccessKeyID     string             `json:"access_key_id" binding:"required,min=16,max=128"`
	AccessKeySecret string             `json:"access_key_secret" binding:"required,min=16,max=256"`
	Regions         []string           `json:"regions" binding:"required,min=1"`
	Description     string             `json:"description" binding:"max=500"`
	Config          CloudAccountConfig `json:"config"`
	TenantID        int64              `json:"tenant_id"`
}

// UpdateCloudAccountRequest 更新云账号请求
type UpdateCloudAccountRequest struct {
	Name            *string             `json:"name,omitempty"`
	Environment     *Environment        `json:"environment,omitempty"`
	AccessKeyID     *string             `json:"access_key_id,omitempty"`
	AccessKeySecret *string             `json:"access_key_secret,omitempty"`
	Regions         []string            `json:"regions,omitempty"`
	Description     *string             `json:"description,omitempty"`
	Config          *CloudAccountConfig `json:"config,omitempty"`
	// 此处**不得**再有 TenantID 字段：更新操作不具备"改变账号归属租户"的语义。
	// 该字段可被 JSON 绑定，留着等于给客户端保留一条改写归属的写路径
	// （距离成为注入通道只差一次 ShouldBindJSON），故连同 service 层两处
	// `if req.TenantID != nil` 门一并删除。归属只在创建时由会话租户确定。
}

// ConnectionTestResult 连接测试结果
type ConnectionTestResult struct {
	Status   string    `json:"status"`    // success, failed
	Message  string    `json:"message"`   // 测试结果消息
	Regions  []string  `json:"regions"`   // 可用地域列表
	TestTime time.Time `json:"test_time"` // 测试时间
}

// SyncAccountRequest 同步账号请求
type SyncAccountRequest struct {
	AssetTypes []string `json:"asset_types"` // 同步的资产类型
	Regions    []string `json:"regions"`     // 同步的地域
}

// SyncResult 同步结果
type SyncResult struct {
	SyncID    string    `json:"sync_id"`    // 同步任务ID
	Status    string    `json:"status"`     // running, success, failed, pending
	Message   string    `json:"message"`    // 提示信息
	StartTime time.Time `json:"start_time"` // 开始时间
}

// 领域方法

// IsActive 判断账号是否为活跃状态
func (a *CloudAccount) IsActive() bool {
	return a.Status == CloudAccountStatusActive
}

// IsReadOnly 判断账号是否为只读权限
func (a *CloudAccount) IsReadOnly() bool {
	return a.Config.ReadOnly
}

// CanAutoSync 判断是否可以自动同步
func (a *CloudAccount) CanAutoSync() bool {
	return a.IsActive() && a.Config.EnableAutoSync
}

// MaskSensitiveData 脱敏敏感数据
func (a *CloudAccount) MaskSensitiveData() *CloudAccount {
	masked := *a
	masked.AccessKeySecret = "***"
	if len(a.AccessKeyID) > 8 {
		masked.AccessKeyID = a.AccessKeyID[:6] + "***" + a.AccessKeyID[len(a.AccessKeyID)-4:]
	} else {
		masked.AccessKeyID = "***"
	}
	return &masked
}

// UpdateSyncStatus 更新同步状态
func (a *CloudAccount) UpdateSyncStatus(syncTime time.Time, assetCount int64) {
	a.LastSyncTime = &syncTime
	a.AssetCount = assetCount
	a.UpdateTime = time.Now()
	a.UTime = a.UpdateTime.Unix()
}

// UpdateTestStatus 更新测试状态
func (a *CloudAccount) UpdateTestStatus(testTime time.Time, status CloudAccountStatus, errorMsg string) {
	a.LastTestTime = &testTime
	a.Status = status
	a.ErrorMessage = errorMsg
	a.UpdateTime = time.Now()
	a.UTime = a.UpdateTime.Unix()
}

// Validate 验证云账号数据
func (a *CloudAccount) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("account name cannot be empty")
	}
	if a.Provider == "" {
		return fmt.Errorf("provider cannot be empty")
	}
	if a.AccessKeyID == "" {
		return fmt.Errorf("access key id cannot be empty")
	}
	if a.AccessKeySecret == "" {
		return fmt.Errorf("access key secret cannot be empty")
	}
	if a.TenantID == 0 {
		return fmt.Errorf("tenant id cannot be empty")
	}
	return nil
}
