package asset

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"

	// 厂商注册统一入口（import manifest），替代原分散 blank import
	_ "github.com/Havens-blog/e-cloudx-sdk/providers"

)

// AdapterFactory 资产适配器工厂
// 桥接层：复用统一的 cloudx.AdapterFactory
type AdapterFactory struct {
	logger         *elog.Component
	unifiedFactory *cloudx.AdapterFactory
}

// NewAdapterFactory 创建适配器工厂
func NewAdapterFactory(logger *elog.Component) *AdapterFactory {
	if logger == nil {
		logger = elog.DefaultLogger
		if logger == nil {
			logger = elog.EgoLogger
		}
	}
	return &AdapterFactory{
		logger:         logger,
		unifiedFactory: cloudx.NewAdapterFactory(logger),
	}
}

// CreateAdapter 根据云账号创建适配器
func (f *AdapterFactory) CreateAdapter(account *CloudAccount) (CloudAssetAdapter, error) {
	if account == nil {
		return nil, fmt.Errorf("账号配置不能为空")
	}

	// 验证账号配置
	if err := account.Validate(); err != nil {
		return nil, fmt.Errorf("账号配置验证失败: %w", err)
	}

	// 检查账号是否启用
	if !account.Enabled {
		return nil, ErrAccountDisabled
	}

	// 检查账号是否过期
	if account.IsExpired() {
		return nil, ErrAccountExpired
	}

	// 转换为 domain.CloudAccount 并使用统一工厂
	domainAccount := &domain.CloudAccount{
		ID:              account.ID,
		Name:            account.Name,
		Provider:        domain.CloudProvider(account.Provider),
		AccessKeyID:     account.AccessKeyID,
		AccessKeySecret: account.AccessKeySecret,
		Regions:         []string{account.DefaultRegion},
		Status:          domain.CloudAccountStatusActive,
	}

	// 使用统一工厂创建适配器
	unifiedAdapter, err := f.unifiedFactory.CreateAdapter(domainAccount)
	if err != nil {
		return nil, err
	}

	// 返回资产适配器包装器
	return &unifiedAssetAdapterWrapper{
		adapter:  unifiedAdapter,
		provider: account.Provider,
	}, nil
}

// CreateAdapterFromDomain 从 domain.CloudAccount 创建适配器
func (f *AdapterFactory) CreateAdapterFromDomain(account *domain.CloudAccount) (CloudAssetAdapter, error) {
	if account == nil {
		return nil, fmt.Errorf("账号配置不能为空")
	}

	// 使用统一工厂创建适配器
	unifiedAdapter, err := f.unifiedFactory.CreateAdapter(account)
	if err != nil {
		return nil, err
	}

	// 返回资产适配器包装器
	return &unifiedAssetAdapterWrapper{
		adapter:  unifiedAdapter,
		provider: types.CloudProvider(account.Provider),
	}, nil
}

// ClearAccountCache 清空指定账号的适配器缓存（透传到底层共享缓存，
// 凭证轮换后由云账号服务调用，见 cam/service/account.go UpdateAccount）
func (f *AdapterFactory) ClearAccountCache(provider domain.CloudProvider, accountID int64) {
	f.unifiedFactory.ClearAccountCache(provider, accountID)
}

// CreateAdapterByProvider 根据云厂商类型和凭证创建适配器（用于测试）
func (f *AdapterFactory) CreateAdapterByProvider(
	provider types.CloudProvider,
	accessKeyID string,
	accessKeySecret string,
	defaultRegion string,
) (CloudAssetAdapter, error) {
	account := &CloudAccount{
		Provider:        provider,
		AccessKeyID:     accessKeyID,
		AccessKeySecret: accessKeySecret,
		DefaultRegion:   defaultRegion,
		Enabled:         true,
	}
	return f.CreateAdapter(account)
}

// unifiedAssetAdapterWrapper 统一适配器包装器
// 将 cloudx.CloudAdapter 包装为 CloudAssetAdapter 接口
type unifiedAssetAdapterWrapper struct {
	adapter  cloudx.CloudAdapter
	provider types.CloudProvider
}

// GetProvider 获取云厂商类型
func (w *unifiedAssetAdapterWrapper) GetProvider() types.CloudProvider {
	return w.provider
}

// ValidateCredentials 验证凭证
func (w *unifiedAssetAdapterWrapper) ValidateCredentials(ctx context.Context) error {
	return w.adapter.ValidateCredentials(ctx)
}

// GetRegions 获取支持的地域列表
func (w *unifiedAssetAdapterWrapper) GetRegions(ctx context.Context) ([]types.Region, error) {
	return w.adapter.Asset().GetRegions(ctx)
}

// GetECSInstances 获取云主机实例列表
func (w *unifiedAssetAdapterWrapper) GetECSInstances(ctx context.Context, region string) ([]types.ECSInstance, error) {
	return w.adapter.Asset().GetECSInstances(ctx, region)
}
