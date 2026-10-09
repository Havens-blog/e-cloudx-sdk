package cloudx

import (
	"fmt"
	"slices"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

var (
	// 全局适配器注册表
	adapterRegistry = &registry{
		creators: make(map[domain.CloudProvider]AdapterCreator),
	}
)

// registry 适配器注册表
type registry struct {
	mu       sync.RWMutex
	creators map[domain.CloudProvider]AdapterCreator
}

// RegisterAdapter 注册适配器创建函数
// 各云厂商包在 init() 中调用此函数注册自己的适配器
func RegisterAdapter(provider domain.CloudProvider, creator AdapterCreator) {
	adapterRegistry.mu.Lock()
	defer adapterRegistry.mu.Unlock()
	adapterRegistry.creators[provider] = creator
}

// GetAdapterCreator 获取适配器创建函数
func GetAdapterCreator(provider domain.CloudProvider) (AdapterCreator, error) {
	adapterRegistry.mu.RLock()
	defer adapterRegistry.mu.RUnlock()

	creator, ok := adapterRegistry.creators[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider)
	}
	return creator, nil
}

// GetRegisteredProviders 获取已注册的云厂商列表
func GetRegisteredProviders() []domain.CloudProvider {
	adapterRegistry.mu.RLock()
	defer adapterRegistry.mu.RUnlock()

	providers := make([]domain.CloudProvider, 0, len(adapterRegistry.creators))
	for provider := range adapterRegistry.creators {
		providers = append(providers, provider)
	}
	return providers
}

// RegisteredPrimaryProviders 返回已注册的"主"云厂商（排除别名键），按固定顺序排序。
//
// 展示层（mcp enum、topology ValidProviders 等）用本函数派生清单，而不是各自硬编码，
// 避免"azure 有无"这类注册表与实际清单漂移的矛盾（历史教训见 2026-09 架构审查）。
// 当前别名键：volcengine 是 volcano 的同源别名（同 creator 注册两键），展示层只保留 volcano。
func RegisteredPrimaryProviders() []domain.CloudProvider {
	providers := make([]domain.CloudProvider, 0, len(adapterRegistry.creators))
	adapterRegistry.mu.RLock()
	for provider := range adapterRegistry.creators {
		if provider == domain.CloudProviderVolcengine {
			continue // 别名键，展示层收敛为主键
		}
		providers = append(providers, provider)
	}
	adapterRegistry.mu.RUnlock()

	slices.Sort(providers)
	return providers
}

// IsProviderRegistered 检查云厂商是否已注册
func IsProviderRegistered(provider domain.CloudProvider) bool {
	adapterRegistry.mu.RLock()
	defer adapterRegistry.mu.RUnlock()
	_, ok := adapterRegistry.creators[provider]
	return ok
}
