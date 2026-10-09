package cloudx

import (
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// adapterCache 全局适配器缓存：所有 AdapterFactory 实例共享。
//
// 凭证轮换（UpdateAccount 改 AK/SK）后必须失效缓存，否则已缓存的适配器仍持旧凭证。
// 历史：各工厂实例各自持 sync.Map 缓存，account/tag/dns/sync 各建实例互不相通，
// 单点 ClearAccountCache 无效——故收拢为包级共享缓存（与 adapterRegistry 同模式），
// 任一实例的 ClearCache/ClearAccountCache 即全量失效。
var adapterCache sync.Map // map[string]CloudAdapter, key = provider_accountID

// AdapterFactory 统一适配器工厂
type AdapterFactory struct {
	logger *elog.Component
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
		logger: logger,
	}
}

// CreateAdapter 根据云账号创建适配器
func (f *AdapterFactory) CreateAdapter(account *domain.CloudAccount) (CloudAdapter, error) {
	if account == nil {
		return nil, ErrInvalidConfig
	}

	// 验证账号配置
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, ErrInvalidConfig
	}

	// 检查账号是否启用
	if account.Status != domain.CloudAccountStatusActive {
		return nil, ErrAccountDisabled
	}

	// 生成缓存 key
	cacheKey := fmt.Sprintf("%s_%d", account.Provider, account.ID)

	// 尝试从缓存获取
	if cached, ok := adapterCache.Load(cacheKey); ok {
		return cached.(CloudAdapter), nil
	}

	// 从注册表获取创建函数
	creator, err := GetAdapterCreator(account.Provider)
	if err != nil {
		return nil, err
	}

	// 创建适配器
	adapter, err := creator(account)
	if err != nil {
		return nil, err
	}

	// 缓存适配器
	adapterCache.Store(cacheKey, adapter)

	f.logger.Info("创建云适配器成功",
		elog.String("provider", string(account.Provider)),
		elog.Int64("account_id", account.ID))

	return adapter, nil
}

// ClearCache 清空适配器缓存（全局共享缓存，任一实例调用即全量失效）
func (f *AdapterFactory) ClearCache() {
	adapterCache.Range(func(key, _ any) bool {
		adapterCache.Delete(key)
		return true
	})
	f.logger.Info("清空适配器缓存")
}

// ClearAccountCache 清空指定账号的适配器缓存（全局共享缓存，任一实例调用即失效该账号）
func (f *AdapterFactory) ClearAccountCache(provider domain.CloudProvider, accountID int64) {
	cacheKey := fmt.Sprintf("%s_%d", provider, accountID)
	adapterCache.Delete(cacheKey)
	f.logger.Info("清空账号适配器缓存",
		elog.String("provider", string(provider)),
		elog.Int64("account_id", accountID))
}
