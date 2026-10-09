package iam

import (
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// adapterFactory IAM 适配器工厂：按 provider 缓存适配器实例，
// 创建器经注册表查找（各厂商包 init() 自注册，见 registry.go）。
type adapterFactory struct {
	adapters map[domain.CloudProvider]CloudIAMAdapter
	mu       sync.RWMutex
	logger   *elog.Component
}

func New(logger *elog.Component) CloudIAMAdapterFactory {
	return &adapterFactory{
		adapters: make(map[domain.CloudProvider]CloudIAMAdapter),
		logger:   logger,
	}
}

func (f *adapterFactory) CreateAdapter(provider domain.CloudProvider) (CloudIAMAdapter, error) {
	f.mu.RLock()
	if adapter, exists := f.adapters[provider]; exists {
		f.mu.RUnlock()
		return adapter, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	if adapter, exists := f.adapters[provider]; exists {
		return adapter, nil
	}

	creator, err := GetIAMAdapterCreator(provider)
	if err != nil {
		return nil, err
	}

	adapter, err := creator(f.logger)
	if err != nil {
		return nil, fmt.Errorf("创建适配器失败: %w", err)
	}

	f.adapters[provider] = adapter

	f.logger.Info("创建云平台适配器成功",
		elog.String("provider", string(provider)))

	return adapter, nil
}

func (f *adapterFactory) ClearCache() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adapters = make(map[domain.CloudProvider]CloudIAMAdapter)
	f.logger.Info("清空适配器缓存")
}

func (f *adapterFactory) GetCachedAdapter(provider domain.CloudProvider) (CloudIAMAdapter, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	adapter, exists := f.adapters[provider]
	return adapter, exists
}
