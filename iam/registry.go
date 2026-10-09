package iam

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// ErrUnsupportedIAMProvider 未注册 IAM 适配器的云厂商。
var ErrUnsupportedIAMProvider = errors.New("unsupported iam provider")

// iamRegistry 云厂商 IAM 适配器注册表（各厂商包 init() 自注册，
// 与 cloudx/billing/logquery 注册表同构；新增云厂商在实现包 init() 注册，
// 并在 internal/shared/cloudx/providers 补 blank import）。
var (
	registryMu       sync.RWMutex
	registryCreators = make(map[domain.CloudProvider]CloudIAMAdapterCreator)
)

// RegisterIAMAdapter 注册云厂商 IAM 适配器创建器（幂等覆盖）。
func RegisterIAMAdapter(provider domain.CloudProvider, creator CloudIAMAdapterCreator) {
	if provider == "" || creator == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryCreators[provider] = creator
}

// GetIAMAdapterCreator 获取云厂商 IAM 适配器创建器。
func GetIAMAdapterCreator(provider domain.CloudProvider) (CloudIAMAdapterCreator, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	creator, ok := registryCreators[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedIAMProvider, provider)
	}
	return creator, nil
}
