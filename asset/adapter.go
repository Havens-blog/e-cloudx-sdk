package asset

import (
	"context"
	"errors"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// 错误定义
var (
	ErrAccountDisabled = errors.New("云账号已禁用")
	ErrAccountExpired  = errors.New("云账号已过期")
	ErrInvalidConfig   = errors.New("无效的账号配置")
)

// CloudAssetAdapter 云资产适配器接口
type CloudAssetAdapter interface {
	// GetProvider 获取云厂商类型
	GetProvider() types.CloudProvider

	// ValidateCredentials 验证凭证
	ValidateCredentials(ctx context.Context) error

	// GetECSInstances 获取云主机实例列表
	GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error)

	// GetRegions 获取支持的地域列表
	GetRegions(ctx context.Context) ([]types.Region, error)
}

// CloudAccount 云账号配置（用于创建适配器）
type CloudAccount struct {
	ID              int64
	Name            string
	Provider        types.CloudProvider
	AccessKeyID     string
	AccessKeySecret string
	DefaultRegion   string
	Enabled         bool
	ExpireTime      *time.Time
}

// Validate 验证账号配置
func (a *CloudAccount) Validate() error {
	if a.AccessKeyID == "" {
		return errors.New("AccessKeyID 不能为空")
	}
	if a.AccessKeySecret == "" {
		return errors.New("AccessKeySecret 不能为空")
	}
	if a.Provider == "" {
		return errors.New("Provider 不能为空")
	}
	return nil
}

// IsExpired 检查账号是否过期
func (a *CloudAccount) IsExpired() bool {
	if a.ExpireTime == nil {
		return false
	}
	return time.Now().After(*a.ExpireTime)
}

// GetDecryptedSecret 获取解密后的密钥（目前直接返回，后续可添加解密逻辑）
func (a *CloudAccount) GetDecryptedSecret() string {
	return a.AccessKeySecret
}

// FromDomainAccount 从 domain.CloudAccount 转换
func FromDomainAccount(account *domain.CloudAccount) *CloudAccount {
	defaultRegion := ""
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	return &CloudAccount{
		ID:              account.ID,
		Name:            account.Name,
		Provider:        types.CloudProvider(account.Provider),
		AccessKeyID:     account.AccessKeyID,
		AccessKeySecret: account.AccessKeySecret,
		DefaultRegion:   defaultRegion,
		Enabled:         account.Status == domain.CloudAccountStatusActive,
	}
}
