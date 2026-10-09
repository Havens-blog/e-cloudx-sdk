package azure

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// ==================== 测试公共设施 ====================

// newTestCertDiscoveryAdapter 构造可注入 fake REST 客户端的发现适配器（限流器放宽避免测试排队）
func newTestCertDiscoveryAdapter(t *testing.T) *CertDiscoveryAdapter {
	t.Helper()
	adapter := NewCertDiscoveryAdapter(elog.DefaultLogger,
		WithTenantID("11111111-1111-1111-1111-111111111111"),
		WithSubscriptionID("22222222-2222-2222-2222-222222222222"))
	adapter.rateLimiter = rate.NewLimiter(5000, 5000)
	return adapter
}

func certTestCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "azure-main",
		Provider:        domain.CloudProviderAzure,
		AccessKeyID:     "33333333-3333-3333-3333-333333333333",
		AccessKeySecret: "test-client-secret",
		Regions:         []string{"eastus"},
	}
}

// fakeTokenProvider 令牌 fake（记录 scope）
type fakeTokenProvider struct {
	scopes []string
}

func (f *fakeTokenProvider) token(ctx context.Context, scope string) (string, error) {
	f.scopes = append(f.scopes, scope)
	return "fake-token", nil
}

// fakeARMLister ARM fake（按资源类型返回预置 items；listCalls 记录调用）
type fakeARMLister struct {
	items     map[string][]json.RawMessage
	listCalls []string
	err       error
}

func (f *fakeARMLister) list(ctx context.Context, resourceType, apiVersion string) ([]json.RawMessage, error) {
	f.listCalls = append(f.listCalls, resourceType)
	if f.err != nil {
		return nil, f.err
	}
	return f.items[resourceType], nil
}

// fakeKVGetter Key Vault fake（secretID → 原始 secret 字节或错误）。
// getSecret 返回存储切片原样（共享底层数组），供净化后 Zeroize 契约断言
// （GetCert 返回后存储切片应已全零）。
type fakeKVGetter struct {
	secrets map[string][]byte
	errs    map[string]error
	err     error
	calls   int
	lastID  string
}

func (f *fakeKVGetter) getSecret(ctx context.Context, secretID string) ([]byte, error) {
	f.calls++
	f.lastID = secretID
	if f.err != nil {
		return nil, f.err
	}
	if err, ok := f.errs[secretID]; ok {
		return nil, err
	}
	return f.secrets[secretID], nil
}

// genTestCertPEM 生成自签测试证书，返回 PEM 与 SHA256 指纹（hex 小写）
func genTestCertPEM(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cert-test.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{"cert-test.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}

// ==================== 只读硬约束：三写方法返回哨兵且不触达云侧 ====================

func TestCertDiscoveryWritesReturnSentinel(t *testing.T) {
	t.Run("三写方法一律返回ErrDiscoveryOnly且不构造REST客户端", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		failFactory := func(name string) {
			t.Helper()
			t.Fatalf("discovery-only 写方法不得构造 %s REST 客户端", name)
		}
		adapter.newToken = func(creds *domain.CloudAccount) (azureTokenProvider, error) {
			failFactory("token")
			return nil, nil
		}
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) {
			failFactory("ARM")
			return nil, nil
		}
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) {
			failFactory("KeyVault")
			return nil, nil
		}

		ctx := context.Background()
		creds := certTestCreds()
		for _, product := range []string{CertProductCDN, CertProductALB} {
			_, err := adapter.UploadCert(ctx, creds, product, "n", "pem", "key")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
			assert.ErrorIs(t, err, cloudx.ErrDiscoveryOnly, product)
			assert.NotErrorIs(t, err, cloudx.ErrCloudRateLimited, product)

			err = adapter.BindResource(ctx, creds, product, "res-1", "kv-secret-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)

			err = adapter.CleanupOrphan(ctx, creds, "kv-secret-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
		}
	})
}

// ==================== ListReferences: CDN (Front Door) ====================

func TestCertDiscoveryListFrontDoorReferences(t *testing.T) {
	t.Run("展开KeyVault引用并跳过托管证书", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/frontdoors": {
				json.RawMessage(`{
					"name": "fd-1",
					"properties": {"frontendEndpoints": [
						{"name": "fe-kv", "properties": {"customHttpsConfiguration": {
							"certificateSource": "AzureKeyVault",
							"secretId": "https://vault-a.vault.azure.net/secrets/cert1/version1"}}},
						{"name": "fe-managed", "properties": {"customHttpsConfiguration": {
							"certificateSource": "FrontDoor"}}},
						{"name": "fe-none"}
					]}
				}`),
				json.RawMessage(`{
					"name": "fd-2",
					"properties": {"frontendEndpoints": [
						{"name": "fe-compose", "properties": {"customHttpsConfiguration": {
							"certificateSource": "azurekeyvault",
							"azureKeyVaultCertificateSecret": {
								"vaultId": "https://vault-b.vault.azure.net",
								"secretName": "cert2",
								"secretVersion": "v2"}}}}
					]}
				}`),
			},
		}}
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 2) // 托管证书与未配置 HTTPS 的终结点不产出引用
		assert.Equal(t, []string{"Microsoft.Network/frontdoors"}, fake.listCalls)

		first := refs[0]
		assert.Equal(t, "azure", first.Cloud)
		assert.Equal(t, CertProductCDN, first.Product)
		assert.Equal(t, "fd-1/fe-kv", first.ResourceID)
		assert.Equal(t, "https://vault-a.vault.azure.net/secrets/cert1/version1", first.ReferencedCloudCertID)
		assert.Equal(t, "azure-main", first.AccountKey)

		// secretId 缺失时回退 vault/name/version 组合（versionless）
		assert.Equal(t, "fd-2/fe-compose", refs[1].ResourceID)
		assert.Equal(t, "https://vault-b.vault.azure.net/secrets/cert2/v2", refs[1].ReferencedCloudCertID)
	})

	t.Run("解析失败的资源跳过", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/frontdoors": {json.RawMessage(`not-json`)},
		}}
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) {
			return &fakeARMLister{err: fmt.Errorf("%w: arm throttled", errAzureThrottled)}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductCDN)
		require.Error(t, err)
	})
}

// ==================== ListReferences: ALB (Application Gateway) ====================

func TestCertDiscoveryListAppGatewayReferences(t *testing.T) {
	t.Run("展开KV引用并将内联证书计为盲区", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/applicationGateways": {
				json.RawMessage(`{
					"name": "agw-1",
					"properties": {
						"sslCertificates": [
							{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert",
							 "name": "kvcert",
							 "properties": {"keyVaultSecretId": "https://vault.vault.azure.net/secrets/agw-cert"}},
							{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/inlinecert",
							 "name": "inlinecert",
							 "properties": {"data": "BASE64"}}
						],
						"httpListeners": [
							{"name": "lsn-kv", "properties": {"sslCertificate": {"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert"}}},
							{"name": "lsn-inline", "properties": {"sslCertificate": {"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/inlinecert"}}},
							{"name": "lsn-http"}
						]
					}
				}`),
			},
		}}
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err)
		require.Len(t, refs, 1) // 内联证书无云侧证书 ID（盲区跳过）；非 HTTPS 监听器不产出
		ref := refs[0]
		assert.Equal(t, "azure", ref.Cloud)
		assert.Equal(t, CertProductALB, ref.Product)
		assert.Equal(t, "agw-1/lsn-kv", ref.ResourceID)
		assert.Equal(t, "https://vault.vault.azure.net/secrets/agw-cert", ref.ReferencedCloudCertID)
		assert.Equal(t, "azure-main", ref.AccountKey)
	})

	t.Run("空资源清单返回空引用", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) { return &fakeARMLister{}, nil }
		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductALB)
		require.Error(t, err)
	})
}

// ==================== GetCert (Key Vault) ====================

func TestCertDiscoveryGetCert(t *testing.T) {
	secretID := "https://vault.vault.azure.net/secrets/cert1/version1"

	t.Run("返回PEM解析的SHA256指纹与有效期", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		pemStr, fingerprint := genTestCertPEM(t)
		fake := &fakeKVGetter{secrets: map[string][]byte{secretID: []byte(pemStr)}}
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) { return fake, nil }

		info, err := adapter.GetCert(context.Background(), certTestCreds(), secretID)
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, fingerprint, info.Fingerprint)
		assert.Equal(t, pemStr, info.CertChainPEM)
		assert.True(t, info.NotAfter.After(time.Now().Add(364*24*time.Hour)))
		assert.Equal(t, secretID, fake.lastID)
	})

	t.Run("含私钥bundle的secret净化为仅CERTIFICATE序列且原始buffer归零", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		leaf, leafFingerprint := genTestCertPEM(t)
		intermediate, _ := genTestCertPEM(t)
		root, _ := genTestCertPEM(t)
		keyBlock := "-----BEGIN EC PRIVATE KEY-----\nZmFrZS1rZXk=\n-----END EC PRIVATE KEY-----\n"
		// exportable 密钥策略下的典型 bundle：证书链 + 私钥混排
		bundle := []byte(keyBlock + leaf + intermediate + root + keyBlock)
		fake := &fakeKVGetter{secrets: map[string][]byte{secretID: bundle}}
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) { return fake, nil }

		info, err := adapter.GetCert(context.Background(), certTestCreds(), secretID)
		require.NoError(t, err)
		assert.True(t, info.Exists)
		// 内容级断言：私钥被丢弃、含且仅含 CERTIFICATE 块（叶在前）
		assert.Equal(t, leaf+intermediate+root, info.CertChainPEM)
		assert.NotContains(t, info.CertChainPEM, "PRIVATE KEY")
		assert.True(t, strings.HasPrefix(info.CertChainPEM, "-----BEGIN CERTIFICATE-----"))
		assert.Equal(t, leafFingerprint, info.Fingerprint)
		// 原始 secret buffer（fake 存储切片与适配层共享底层数组）净化后已全零
		for i, b := range bundle {
			require.Zero(t, b, "原始 secret 第 %d 字节未归零", i)
		}
	})

	t.Run("secret不存在返回Exists=false", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) {
			return &fakeKVGetter{err: errAzureNotFound}, nil
		}
		info, err := adapter.GetCert(context.Background(), certTestCreds(), secretID)
		require.NoError(t, err)
		assert.False(t, info.Exists)
		assert.Empty(t, info.Fingerprint)
		assert.True(t, info.NotAfter.IsZero())
		assert.Empty(t, info.CertChainPEM)
	})

	t.Run("非证书secret返回Exists=true且指纹与净化序列为空", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeKVGetter{secrets: map[string][]byte{secretID: []byte("plain-secret-value")}}
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) { return fake, nil }
		info, err := adapter.GetCert(context.Background(), certTestCreds(), secretID)
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Empty(t, info.Fingerprint)
		assert.Empty(t, info.CertChainPEM)
		assert.True(t, info.NotAfter.IsZero())
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) {
			return &fakeKVGetter{err: fmt.Errorf("%w: kv throttled", errAzureThrottled)}, nil
		}
		_, err := adapter.GetCert(context.Background(), certTestCreds(), secretID)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空secretID与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.GetCert(context.Background(), certTestCreds(), " ")
		require.Error(t, err)
		_, err = adapter.GetCert(context.Background(), nil, secretID)
		require.Error(t, err)
	})
}

// ==================== 分发、Option 与辅助 ====================

func TestCertDiscoveryDispatch(t *testing.T) {
	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		// Azure WAF policy 无证书引用面、Load Balancer 无 TLS 终结：均不入支持集
		for _, product := range []string{"waf", "nlb", "clb", "dcdn", "crd", ""} {
			_, err := adapter.ListReferences(context.Background(), certTestCreds(), product)
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrCertProductNotSupported, product)
		}
	})

	t.Run("产品集合枚举对齐schema", func(t *testing.T) {
		for _, product := range []string{CertProductCDN, CertProductALB} {
			assert.True(t, certSupportedProducts[product], product)
		}
		for product := range certSupportedProducts {
			assert.Contains(t, []string{"cdn", "dcdn", "waf", "alb", "clb", "nlb", "crd"}, product)
		}
		assert.False(t, certSupportedProducts["waf"])
	})
}

func TestNewCertDiscoveryAdapterConfig(t *testing.T) {
	t.Run("Option注入与环境变量回退", func(t *testing.T) {
		t.Setenv(envTenantID, "env-tenant")
		t.Setenv(envSubscriptionID, "env-sub")
		adapter := NewCertDiscoveryAdapter(nil)
		assert.Equal(t, "env-tenant", adapter.tenantID)
		assert.Equal(t, "env-sub", adapter.subscriptionID)
		assert.Equal(t, defaultLoginEndpoint, adapter.loginEndpoint)
		assert.Equal(t, defaultMgmtEndpoint, adapter.mgmtEndpoint)

		// Option 显式注入优先于环境变量
		adapter = NewCertDiscoveryAdapter(nil,
			WithTenantID("opt-tenant"),
			WithSubscriptionID("opt-sub"),
			WithEndpoints("https://login.chinacloudapi.cn", "https://management.chinacloudapi.cn"))
		assert.Equal(t, "opt-tenant", adapter.tenantID)
		assert.Equal(t, "opt-sub", adapter.subscriptionID)
		assert.Equal(t, "https://login.chinacloudapi.cn", adapter.loginEndpoint)
		assert.Equal(t, "https://management.chinacloudapi.cn", adapter.mgmtEndpoint)

		// 空白字符串不覆盖端点缺省值
		adapter = NewCertDiscoveryAdapter(nil, WithEndpoints(" ", " "))
		assert.Equal(t, defaultLoginEndpoint, adapter.loginEndpoint)
	})

	t.Run("真实REST工厂缺租户或凭证显式报错", func(t *testing.T) {
		adapter := NewCertDiscoveryAdapter(elog.DefaultLogger) // 无租户/订阅
		_, err := adapter.newToken(certTestCreds())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tenant id required")

		_, err = adapter.newARM(certTestCreds())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "subscription id required")

		full := NewCertDiscoveryAdapter(elog.DefaultLogger,
			WithTenantID("t"), WithSubscriptionID("s"))
		_, err = full.newToken(nil)
		require.Error(t, err)
		_, err = full.newToken(&domain.CloudAccount{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "client id/secret required")
	})
}

func TestComposeKvSecretID(t *testing.T) {
	assert.Equal(t, "https://v.vault.azure.net/secrets/s/ver",
		composeKvSecretID(&kvSecretRef{VaultID: "https://v.vault.azure.net/", SecretName: "s", SecretVersion: "ver"}))
	assert.Equal(t, "https://v.vault.azure.net/secrets/s",
		composeKvSecretID(&kvSecretRef{VaultID: "https://v.vault.azure.net", SecretName: "s"}))
	assert.Empty(t, composeKvSecretID(nil))
	assert.Empty(t, composeKvSecretID(&kvSecretRef{VaultID: "v"}))
	assert.Empty(t, composeKvSecretID(&kvSecretRef{VaultID: "v", SecretName: ""}))
}

func TestRelativeAzureURL(t *testing.T) {
	base := "https://management.azure.com"
	assert.Equal(t, base+"/subs/x", relativeAzureURL(base, base+"/subs/x")) // 同源保持
	assert.Equal(t, "https://other.example.com/x", relativeAzureURL(base, "https://other.example.com/x"))
	assert.Equal(t, base+"/relative", relativeAzureURL(base, "relative"))
	assert.Empty(t, relativeAzureURL(base, ""))
}

// ==================== 真实 REST 客户端（httptest 假端点） ====================

// newFakeAADServer 构造 AAD v2 令牌假端点（记录表单；hit 计数供缓存断言）
func newFakeAADServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		require.Contains(t, r.URL.Path, "/tenant-1/oauth2/v2.0/token")
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
		require.Equal(t, "client-id-1", r.PostForm.Get("client_id"))
		require.Equal(t, "client-secret-1", r.PostForm.Get("client_secret"))
		require.Equal(t, armScopeValue, r.PostForm.Get("scope"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token-abc","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRESTTokenProvider(t *testing.T) {
	t.Run("按scope缓存令牌", func(t *testing.T) {
		hits := 0
		server := newFakeAADServer(t, &hits)
		adapter := NewCertDiscoveryAdapter(elog.DefaultLogger, WithTenantID("tenant-1"), WithEndpoints(server.URL, ""))
		provider, err := newRESTTokenProvider(adapter, &domain.CloudAccount{
			AccessKeyID:     "client-id-1",
			AccessKeySecret: "client-secret-1",
		})
		require.NoError(t, err)

		token, err := provider.token(context.Background(), armScopeValue)
		require.NoError(t, err)
		assert.Equal(t, "token-abc", token)

		// 未过期缓存命中：不再发起 HTTP
		token, err = provider.token(context.Background(), armScopeValue)
		require.NoError(t, err)
		assert.Equal(t, "token-abc", token)
		assert.Equal(t, 1, hits)
	})

	t.Run("429与错误响应归类", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"server_busy"}`))
		}))
		defer server.Close()
		adapter := NewCertDiscoveryAdapter(elog.DefaultLogger, WithTenantID("tenant-1"), WithEndpoints(server.URL, ""))
		provider, err := newRESTTokenProvider(adapter, certTestCreds())
		require.NoError(t, err)
		_, err = provider.token(context.Background(), armScopeValue)
		require.Error(t, err)
		assert.ErrorIs(t, err, errAzureThrottled)

		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		})
		// 失败不落缓存，重试后仍报错
		_, err = provider.token(context.Background(), armScopeValue)
		require.Error(t, err)
		assert.NotErrorIs(t, err, errAzureThrottled)
	})
}

func TestARMRESTLister(t *testing.T) {
	t.Run("跟随nextLink翻页并携带Bearer", func(t *testing.T) {
		var uris []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "Bearer fake-token", r.Header.Get("Authorization"))
			uris = append(uris, r.URL.RequestURI())
			switch len(uris) {
			case 1:
				_, _ = w.Write([]byte(`{"value":[{"name":"a"},{"name":"b"}],"nextLink":"http://` + r.Host + `/subscriptions/sub-1/providers/Microsoft.Network/frontdoors?page=2"}`))
			default:
				_, _ = w.Write([]byte(`{"value":[{"name":"c"}]}`))
			}
		}))
		defer server.Close()

		lister := &armRESTLister{
			token:          &fakeTokenProvider{},
			mgmtEndpoint:   server.URL,
			subscriptionID: "sub-1",
			pageSize:       2,
			httpClient:     server.Client(),
		}
		items, err := lister.list(context.Background(), "Microsoft.Network/frontdoors", armAPIVersion)
		require.NoError(t, err)
		require.Len(t, items, 3)
		require.Len(t, uris, 2)
		// $top 分页参数与 api-version 随请求携带
		first := uris[0]
		assert.Contains(t, first, "$top=2")
		assert.Contains(t, first, "api-version="+armAPIVersion)
	})

	t.Run("nextLink环路保护", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 恒定自指 nextLink：第二次响应后触发 visited 环路保护
			_, _ = w.Write([]byte(`{"value":[],"nextLink":"http://` + r.Host + `/loop"}`))
		}))
		defer server.Close()
		lister := &armRESTLister{
			token:          &fakeTokenProvider{},
			mgmtEndpoint:   server.URL,
			subscriptionID: "sub-1",
			pageSize:       1,
			httpClient:     server.Client(),
		}
		_, err := lister.list(context.Background(), "Microsoft.Network/frontdoors", armAPIVersion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loop")
	})

	t.Run("404与429归类", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()
		lister := &armRESTLister{
			token: &fakeTokenProvider{}, mgmtEndpoint: server.URL,
			subscriptionID: "sub-1", pageSize: 1, httpClient: server.Client(),
		}
		_, err := lister.list(context.Background(), "Microsoft.Network/frontdoors", armAPIVersion)
		assert.ErrorIs(t, err, errAzureNotFound)
	})
}

func TestKVRESTClient(t *testing.T) {
	t.Run("读取secret值并携带api-version", func(t *testing.T) {
		var query string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "Bearer fake-token", r.Header.Get("Authorization"))
			query = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"value":"pem-content"}`))
		}))
		defer server.Close()
		client := &kvRESTClient{token: &fakeTokenProvider{}, httpClient: server.Client()}

		value, err := client.getSecret(context.Background(), server.URL+"/secrets/cert1/version1")
		require.NoError(t, err)
		assert.Equal(t, []byte("pem-content"), value)
		assert.Equal(t, "api-version="+kvAPIVersion, query)
	})

	t.Run("404与429归类", func(t *testing.T) {
		statuses := []int{http.StatusNotFound, http.StatusTooManyRequests}
		for _, status := range statuses {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			client := &kvRESTClient{token: &fakeTokenProvider{}, httpClient: server.Client()}
			_, err := client.getSecret(context.Background(), server.URL+"/secrets/cert1")
			require.Error(t, err)
			if status == http.StatusNotFound {
				assert.ErrorIs(t, err, errAzureNotFound)
			} else {
				assert.ErrorIs(t, err, errAzureThrottled)
			}
			server.Close()
		}
	})
}

func TestTruncateForLog(t *testing.T) {
	assert.Equal(t, "short", truncateForLog("short"))
	long := strings.Repeat("x", 512)
	got := truncateForLog(long)
	assert.Len(t, got, 256+3) // 256 字符 + "..."
	assert.True(t, strings.HasSuffix(got, "..."))
}

// 编译期接口满足断言（真实 REST 客户端实现窄接口，供适配器工厂注入）
var (
	_ azureTokenProvider  = (*restTokenProvider)(nil)
	_ azureARMLister      = (*armRESTLister)(nil)
	_ azureKVSecretGetter = (*kvRESTClient)(nil)
	_ error               = errAzureNotFound
	_                     = errors.New // 保持 errors 导入（哨兵断言使用 errors.Is 语义）
)
