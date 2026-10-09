package cloudx_test

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// 触发各云厂商 init() 注册
	_ "github.com/Havens-blog/e-cloudx-sdk/aliyun"
	_ "github.com/Havens-blog/e-cloudx-sdk/aws"
	_ "github.com/Havens-blog/e-cloudx-sdk/huawei"
	_ "github.com/Havens-blog/e-cloudx-sdk/tencent"
	_ "github.com/Havens-blog/e-cloudx-sdk/volcano"
)

func newTestAccount(provider domain.CloudProvider) *domain.CloudAccount {
	return &domain.CloudAccount{
		ID:              1,
		Name:            "test-account",
		Provider:        provider,
		AccessKeyID:     "test-access-key-id-12345",
		AccessKeySecret: "test-access-key-secret-12345",
		Regions:         []string{"cn-hangzhou"},
		Status:          domain.CloudAccountStatusActive,
		TenantID:        6,
	}
}

func TestNewAdapterFactory(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	assert.NotNil(t, factory)
}

func TestAdapterFactory_CreateAdapter_NilAccount(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	adapter, err := factory.CreateAdapter(nil)
	assert.Nil(t, adapter)
	assert.ErrorIs(t, err, cloudx.ErrInvalidConfig)
}

func TestAdapterFactory_CreateAdapter_EmptyCredentials(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)

	tests := []struct {
		name    string
		account *domain.CloudAccount
	}{
		{
			name: "空AccessKeyID",
			account: &domain.CloudAccount{
				ID:              1,
				Provider:        domain.CloudProviderAliyun,
				AccessKeyID:     "",
				AccessKeySecret: "secret-12345678901234",
				Status:          domain.CloudAccountStatusActive,
			},
		},
		{
			name: "空AccessKeySecret",
			account: &domain.CloudAccount{
				ID:              1,
				Provider:        domain.CloudProviderAliyun,
				AccessKeyID:     "key-12345678901234",
				AccessKeySecret: "",
				Status:          domain.CloudAccountStatusActive,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := factory.CreateAdapter(tt.account)
			assert.Nil(t, adapter)
			assert.ErrorIs(t, err, cloudx.ErrInvalidConfig)
		})
	}
}

func TestAdapterFactory_CreateAdapter_DisabledAccount(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	account := newTestAccount(domain.CloudProviderAliyun)
	account.Status = domain.CloudAccountStatusDisabled

	adapter, err := factory.CreateAdapter(account)
	assert.Nil(t, adapter)
	assert.ErrorIs(t, err, cloudx.ErrAccountDisabled)
}

func TestAdapterFactory_CreateAdapter_UnsupportedProvider(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	account := newTestAccount("unsupported_cloud")

	adapter, err := factory.CreateAdapter(account)
	assert.Nil(t, adapter)
	assert.ErrorIs(t, err, cloudx.ErrUnsupportedProvider)
}

func TestAdapterFactory_CreateAdapter_AllProviders(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)

	providers := []struct {
		name     string
		provider domain.CloudProvider
	}{
		{"阿里云", domain.CloudProviderAliyun},
		{"AWS", domain.CloudProviderAWS},
		{"华为云", domain.CloudProviderHuawei},
		{"腾讯云", domain.CloudProviderTencent},
		{"火山引擎", domain.CloudProviderVolcano},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			account := newTestAccount(p.provider)
			adapter, err := factory.CreateAdapter(account)
			require.NoError(t, err)
			require.NotNil(t, adapter)

			// 验证 provider 正确
			assert.Equal(t, p.provider, domain.CloudProvider(adapter.GetProvider()))

			// 验证所有子适配器不为 nil
			assert.NotNil(t, adapter.ECS(), "%s ECS adapter should not be nil", p.name)
			assert.NotNil(t, adapter.RDS(), "%s RDS adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Redis(), "%s Redis adapter should not be nil", p.name)
			assert.NotNil(t, adapter.MongoDB(), "%s MongoDB adapter should not be nil", p.name)
			assert.NotNil(t, adapter.VPC(), "%s VPC adapter should not be nil", p.name)
			assert.NotNil(t, adapter.EIP(), "%s EIP adapter should not be nil", p.name)
			assert.NotNil(t, adapter.IAM(), "%s IAM adapter should not be nil", p.name)
			assert.NotNil(t, adapter.SecurityGroup(), "%s SecurityGroup adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Image(), "%s Image adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Disk(), "%s Disk adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Snapshot(), "%s Snapshot adapter should not be nil", p.name)
			assert.NotNil(t, adapter.NAS(), "%s NAS adapter should not be nil", p.name)
			assert.NotNil(t, adapter.OSS(), "%s OSS adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Kafka(), "%s Kafka adapter should not be nil", p.name)
			assert.NotNil(t, adapter.Elasticsearch(), "%s Elasticsearch adapter should not be nil", p.name)
		})
	}
}

func TestAdapterFactory_Cache(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	account := newTestAccount(domain.CloudProviderAliyun)

	adapter1, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	adapter2, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	assert.Same(t, adapter1, adapter2)
}

func TestAdapterFactory_ClearCache(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	account := newTestAccount(domain.CloudProviderAliyun)

	adapter1, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	factory.ClearCache()

	adapter2, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	assert.NotSame(t, adapter1, adapter2)
}

func TestAdapterFactory_ClearAccountCache(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)
	account := newTestAccount(domain.CloudProviderAliyun)

	adapter1, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	factory.ClearAccountCache(account.Provider, account.ID)

	adapter2, err := factory.CreateAdapter(account)
	require.NoError(t, err)

	assert.NotSame(t, adapter1, adapter2)
}

func TestAdapterFactory_DifferentAccounts_DifferentCache(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)

	account1 := newTestAccount(domain.CloudProviderAliyun)
	account1.ID = 1

	account2 := newTestAccount(domain.CloudProviderAliyun)
	account2.ID = 2

	adapter1, err := factory.CreateAdapter(account1)
	require.NoError(t, err)

	adapter2, err := factory.CreateAdapter(account2)
	require.NoError(t, err)

	assert.NotSame(t, adapter1, adapter2)
}
