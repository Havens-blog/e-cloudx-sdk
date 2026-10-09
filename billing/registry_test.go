package billing

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterBillingAdapter(t *testing.T) {
	testProvider := domain.CloudProvider("test_billing_provider")
	called := false
	RegisterBillingAdapter(testProvider, func(account *domain.CloudAccount) (BillingAdapter, error) {
		called = true
		return nil, nil
	})
	defer func() {
		billingAdapterRegistry.mu.Lock()
		delete(billingAdapterRegistry.creators, testProvider)
		billingAdapterRegistry.mu.Unlock()
	}()

	assert.True(t, IsBillingProviderRegistered(testProvider))

	creator, err := GetBillingAdapter(testProvider)
	require.NoError(t, err)
	assert.NotNil(t, creator)

	_, _ = creator(nil)
	assert.True(t, called)
}

func TestGetBillingAdapter_UnsupportedProvider(t *testing.T) {
	creator, err := GetBillingAdapter("nonexistent_billing_provider")
	assert.Nil(t, creator)
	assert.ErrorIs(t, err, ErrUnsupportedBillingProvider)
}

func TestIsBillingProviderRegistered(t *testing.T) {
	assert.False(t, IsBillingProviderRegistered("definitely_not_registered"))
}

func TestGetRegisteredBillingProviders(t *testing.T) {
	providers := GetRegisteredBillingProviders()
	assert.NotNil(t, providers)
}

func TestRegisterBillingAdapter_Overwrite(t *testing.T) {
	testProvider := domain.CloudProvider("billing_overwrite_test")
	callCount := 0
	defer func() {
		billingAdapterRegistry.mu.Lock()
		delete(billingAdapterRegistry.creators, testProvider)
		billingAdapterRegistry.mu.Unlock()
	}()

	RegisterBillingAdapter(testProvider, func(account *domain.CloudAccount) (BillingAdapter, error) {
		callCount = 1
		return nil, nil
	})

	RegisterBillingAdapter(testProvider, func(account *domain.CloudAccount) (BillingAdapter, error) {
		callCount = 2
		return nil, nil
	})

	creator, err := GetBillingAdapter(testProvider)
	require.NoError(t, err)
	_, _ = creator(nil)
	assert.Equal(t, 2, callCount, "应该使用最后注册的创建函数")
}
