package cloudx

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAdapter(t *testing.T) {
	// 注册一个测试适配器（使用不会与真实云厂商冲突的名称）
	testProvider := domain.CloudProvider("test_provider_reg")
	called := false
	RegisterAdapter(testProvider, func(account *domain.CloudAccount) (CloudAdapter, error) {
		called = true
		return nil, nil
	})
	// 测试结束后清理
	defer func() {
		adapterRegistry.mu.Lock()
		delete(adapterRegistry.creators, testProvider)
		adapterRegistry.mu.Unlock()
	}()

	// 验证注册成功
	assert.True(t, IsProviderRegistered(testProvider))

	// 获取创建函数并调用
	creator, err := GetAdapterCreator(testProvider)
	require.NoError(t, err)
	assert.NotNil(t, creator)

	_, _ = creator(nil)
	assert.True(t, called)
}

func TestGetAdapterCreator_UnsupportedProvider(t *testing.T) {
	creator, err := GetAdapterCreator("nonexistent_provider")
	assert.Nil(t, creator)
	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestIsProviderRegistered(t *testing.T) {
	assert.False(t, IsProviderRegistered("definitely_not_registered"))
}

func TestGetRegisteredProviders(t *testing.T) {
	providers := GetRegisteredProviders()
	assert.NotNil(t, providers)
}

func TestRegisteredPrimaryProviders(t *testing.T) {
	got := RegisteredPrimaryProviders()

	// 去重：volcengine 别名键不得出现
	for _, p := range got {
		assert.NotEqual(t, domain.CloudProviderVolcengine, p)
	}
	// azure 尚未注册 CloudAdapter，不得出现（接入后此断言需更新）
	assert.NotContains(t, got, domain.CloudProviderAzure)
	// 固定顺序，保证输出稳定
	assert.IsIncreasing(t, got)
}

// TestPrimaryAssetProviders_MatchesRegistry 漂移守卫：
// shared/domain.PrimaryAssetProviders（展示层单源）必须与注册表的主厂商集合一致。
// azure 接入 CloudAdapter 时本测试会红，提醒先更新 PrimaryAssetProviders 再收尾展示层。
func TestPrimaryAssetProviders_MatchesRegistry(t *testing.T) {
	registered := RegisteredPrimaryProviders()
	require.ElementsMatch(t, domain.PrimaryAssetProviders, registered,
		"domain.PrimaryAssetProviders 与 cloudx 注册表主厂商漂移：先更新 shared/domain/account.go 的 PrimaryAssetProviders")
}

func TestRegisterAdapter_Overwrite(t *testing.T) {
	testProvider := domain.CloudProvider("overwrite_test")
	callCount := 0
	defer func() {
		adapterRegistry.mu.Lock()
		delete(adapterRegistry.creators, testProvider)
		adapterRegistry.mu.Unlock()
	}()

	// 第一次注册
	RegisterAdapter(testProvider, func(account *domain.CloudAccount) (CloudAdapter, error) {
		callCount = 1
		return nil, nil
	})

	// 第二次注册（覆盖）
	RegisterAdapter(testProvider, func(account *domain.CloudAccount) (CloudAdapter, error) {
		callCount = 2
		return nil, nil
	})

	creator, err := GetAdapterCreator(testProvider)
	require.NoError(t, err)
	_, _ = creator(nil)
	assert.Equal(t, 2, callCount, "应该使用最后注册的创建函数")
}
