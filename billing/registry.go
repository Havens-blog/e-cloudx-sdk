package billing

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

var (
	// ErrUnsupportedBillingProvider 不支持的计费云厂商
	ErrUnsupportedBillingProvider = errors.New("unsupported billing provider")
)

// 全局计费适配器注册表
var billingAdapterRegistry = &billingRegistry{
	creators: make(map[domain.CloudProvider]BillingAdapterCreator),
}

// billingRegistry 计费适配器注册表
type billingRegistry struct {
	mu       sync.RWMutex
	creators map[domain.CloudProvider]BillingAdapterCreator
}

// RegisterBillingAdapter 注册计费适配器创建函数
// 各云厂商包在 init() 中调用此函数注册自己的计费适配器
func RegisterBillingAdapter(provider domain.CloudProvider, creator BillingAdapterCreator) {
	billingAdapterRegistry.mu.Lock()
	defer billingAdapterRegistry.mu.Unlock()
	billingAdapterRegistry.creators[provider] = creator
}

// GetBillingAdapter 获取计费适配器创建函数
func GetBillingAdapter(provider domain.CloudProvider) (BillingAdapterCreator, error) {
	billingAdapterRegistry.mu.RLock()
	defer billingAdapterRegistry.mu.RUnlock()

	creator, ok := billingAdapterRegistry.creators[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedBillingProvider, provider)
	}
	return creator, nil
}

// GetRegisteredBillingProviders 获取已注册的计费云厂商列表
func GetRegisteredBillingProviders() []domain.CloudProvider {
	billingAdapterRegistry.mu.RLock()
	defer billingAdapterRegistry.mu.RUnlock()

	providers := make([]domain.CloudProvider, 0, len(billingAdapterRegistry.creators))
	for provider := range billingAdapterRegistry.creators {
		providers = append(providers, provider)
	}
	return providers
}

// IsBillingProviderRegistered 检查计费云厂商是否已注册
func IsBillingProviderRegistered(provider domain.CloudProvider) bool {
	billingAdapterRegistry.mu.RLock()
	defer billingAdapterRegistry.mu.RUnlock()
	_, ok := billingAdapterRegistry.creators[provider]
	return ok
}
