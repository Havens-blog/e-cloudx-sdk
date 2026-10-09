// cert_test.go Azure 完整证书适配器单元测试：fake KV 写面/ARM 绑定面窄接口覆盖
// 上传（PEM 形态导入 + 私钥副本零化）、两产品绑定（Front Door 自定义域名 HTTPS
// 配置 / App Gateway 监听器 KV 引用）、孤儿清理（幂等 + KV 软删除语义）与真实
// REST 写客户端形态（cert-multicloud-deployers 任务 3）。
package azure

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// ==================== 测试公共设施 ====================

// newTestCertAdapter 构造可注入 fake 写面客户端的完整适配器（限流器放宽避免测试排队；
// vault 目标经显式 Option 注入，供上传路径断言）
func newTestCertAdapter(t *testing.T) *CertAdapter {
	t.Helper()
	adapter := NewCertAdapter(elog.DefaultLogger, WithKeyVaultName("vault-main"))
	adapter.rateLimiter = rate.NewLimiter(5000, 5000)
	return adapter
}

// azureTestKeyPEM 测试明文私钥（PEM 形态；fake 环境不参与真实加解密）
const azureTestKeyPEM = "-----BEGIN PRIVATE KEY-----\nZmFrZS1rZXktbWF0ZXJpYWw=\n-----END PRIVATE KEY-----\n"

// ==================== 测试替身：KV 写面 / ARM 绑定面 ====================

// kvImportRecord 一次导入调用的快照（params.Value 与调用方共享底层数组，
// 供 Zeroize 契约断言；valueCopy 为调用时点独立副本，供内容断言）
type kvImportRecord struct {
	vault     string
	name      string
	params    azureKVCertImport
	valueCopy []byte
}

// fakeKVCertWriter mock KV 证书写面（导入/删除），记录调用序列
type fakeKVCertWriter struct {
	mu           sync.Mutex
	vault        string
	importCalls  []kvImportRecord
	importResult azureKVCertImportResult
	importErrFn  func(call int) error
	deletes      []string
	deleteErrFn  func(call int) error
	deletedNames map[string]bool // 已删除名集合（再次删除模拟软删除后 404）
}

func (f *fakeKVCertWriter) importCertificate(_ context.Context, certName string, params azureKVCertImport) (azureKVCertImportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importCalls = append(f.importCalls, kvImportRecord{
		vault:     f.vault,
		name:      certName,
		params:    params,
		valueCopy: append([]byte(nil), params.Value...),
	})
	if f.importErrFn != nil {
		if err := f.importErrFn(len(f.importCalls)); err != nil {
			return azureKVCertImportResult{}, err
		}
	}
	if f.importResult.SecretID == "" && f.importResult.CertificateBase64 == "" {
		return azureKVCertImportResult{
			KeyID:    f.vault + "/keys/" + certName + "/1",
			SecretID: f.vault + "/secrets/" + certName + "/1",
		}, nil
	}
	return f.importResult, nil
}

func (f *fakeKVCertWriter) deleteCertificate(_ context.Context, certName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, certName)
	if f.deleteErrFn != nil {
		if err := f.deleteErrFn(len(f.deletes)); err != nil {
			return err
		}
	}
	if f.deletedNames == nil {
		f.deletedNames = map[string]bool{}
	}
	if f.deletedNames[certName] {
		return errAzureNotFound // 模拟 KV 软删除后重复删除 404
	}
	f.deletedNames[certName] = true
	return nil
}

// armWriteCall 一次管理面写调用的快照
type armWriteCall struct {
	url  string
	body []byte
}

// fakeARMBinder mock ARM 绑定面（订阅级列举 + PATCH/PUT 写），记录调用序列
type fakeARMBinder struct {
	*fakeARMLister // 复用订阅级 list fake（按资源类型返回预置 items）
	mu             sync.Mutex
	patchCalls     []armWriteCall
	putCalls       []armWriteCall
	writeErr       error
}

func (f *fakeARMBinder) patchResource(_ context.Context, resourceURL string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patchCalls = append(f.patchCalls, armWriteCall{url: resourceURL, body: body})
	return f.writeErr
}

func (f *fakeARMBinder) putResource(_ context.Context, resourceURL string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls = append(f.putCalls, armWriteCall{url: resourceURL, body: body})
	return f.writeErr
}

// injectFakeBinder 注入 fake ARM 绑定面工厂
func injectFakeBinder(adapter *CertAdapter, fake *fakeARMBinder) {
	adapter.newARMBinder = func(context.Context, *domain.CloudAccount) (azureARMBinder, error) {
		return fake, nil
	}
}

// injectFakeKVWriter 注入 fake KV 写面工厂（记录 vault 定位）
func injectFakeKVWriter(adapter *CertAdapter, fake *fakeKVCertWriter) {
	adapter.newKVCertWriter = func(_ context.Context, _ *domain.CloudAccount, vaultURI string) (azureKVCertWriter, error) {
		fake.mu.Lock()
		fake.vault = vaultURI
		fake.mu.Unlock()
		return fake, nil
	}
}

// ARM 夹具：Application Gateway（KV 引用证书 + 内联证书 + 双监听器）
const agwBindFixture = `{
	"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1",
	"name": "agw-1",
	"properties": {
		"sslCertificates": [
			{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert",
			 "name": "kvcert",
			 "properties": {"keyVaultSecretId": "https://vault-old.vault.azure.net/secrets/old-cert/ver1"}},
			{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/inlinecert",
			 "name": "inlinecert",
			 "properties": {"data": "QkFTRTY0"}}
		],
		"httpListeners": [
			{"name": "lsn-kv", "properties": {"sslCertificate": {"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert"}}},
			{"name": "lsn-inline", "properties": {"sslCertificate": {"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/inlinecert"}}}
		]
	}
}`

// ARM 夹具：Front Door（KV 自定义 HTTPS 终结点 + 组合字段终结点 + 托管终结点）
const frontDoorBindFixture = `{
	"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/frontDoors/fd-1",
	"name": "fd-1",
	"properties": {
		"frontendEndpoints": [
			{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/frontDoors/fd-1/frontendEndpoints/fe-kv",
			 "name": "fe-kv",
			 "properties": {"customHttpsConfiguration": {
				"certificateSource": "AzureKeyVault",
				"secretId": "https://vault-old.vault.azure.net/secrets/old-cert/ver1"}}},
			{"name": "fe-compose",
			 "properties": {"customHttpsConfiguration": {
				"certificateSource": "azurekeyvault",
				"azureKeyVaultCertificateSecret": {
					"vaultId": "https://vault-old.vault.azure.net",
					"secretName": "old-cert",
					"secretVersion": "ver1"}}}}
		]
	}
}`

const vaultFixture = `{
	"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/vault-main",
	"name": "vault-main"
}`

// ==================== UploadCert：KV 证书导入 ====================

func TestCertAdapterUploadCert(t *testing.T) {
	pemStr, fingerprint := genTestCertPEM(t)

	t.Run("PEM形态导入并返回secretID引用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{}
		injectFakeKVWriter(adapter, fake)

		certID, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN,
			"ecam-abcd1234-1765432100-0a1b", pemStr, azureTestKeyPEM)
		require.NoError(t, err)
		assert.Equal(t, "https://vault-main.vault.azure.net/secrets/ecam-abcd1234-1765432100-0a1b/1", certID,
			"云证书 ID=KV secret ID（映射表直接承载形态）")

		require.Len(t, fake.importCalls, 1)
		rec := fake.importCalls[0]
		assert.Equal(t, "https://vault-main.vault.azure.net", rec.vault, "上传目标 vault=显式注入")
		assert.Equal(t, "ecam-abcd1234-1765432100-0a1b", rec.name)
		assert.Equal(t, certKVPemContentType, rec.params.ContentType, "PEM 形态 contentType")

		// 导入束内容：base64 解码后为证书束（叶在前）+ 私钥（KV 导入 API 含私钥形态）
		decoded, err := base64.StdEncoding.DecodeString(string(rec.valueCopy))
		require.NoError(t, err)
		combined := string(decoded)
		assert.Contains(t, combined, "-----BEGIN CERTIFICATE-----")
		assert.Contains(t, combined, "-----BEGIN PRIVATE KEY-----")
		assert.Less(t, strings.Index(combined, "-----BEGIN CERTIFICATE-----"),
			strings.Index(combined, "-----BEGIN PRIVATE KEY-----"), "证书束在前、私钥在后（叶在前口径）")
		assert.Equal(t, fingerprint, mustLeafFingerprint(t, pemStr), "夹具指纹自洽（对齐台账 SHA256 口径）")

		// 私钥副本零化契约：导入调用返回后 base64 值（含私钥材料）已全零
		for i, b := range rec.params.Value {
			require.Zero(t, b, "base64 值第 %d 字节未归零", i)
		}
	})

	t.Run("导入结果缺sid时组合versionless回退", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		// CertificateBase64 非空以旁路 fake 缺省结果（模拟真实 sid 缺失响应）
		fake := &fakeKVCertWriter{importResult: azureKVCertImportResult{CertificateBase64: "AAEC"}}
		injectFakeKVWriter(adapter, fake)

		certID, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductALB,
			"ecam-abcd1234-1-0a1b", pemStr, azureTestKeyPEM)
		require.NoError(t, err)
		assert.Equal(t, "https://vault-main.vault.azure.net/secrets/ecam-abcd1234-1-0a1b", certID,
			"回退=versionless secret ID（发现引用兼容形态）")
	})

	t.Run("私钥非PEM形态显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{}
		injectFakeKVWriter(adapter, fake)

		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN,
			"ecam-abcd1234-1-0a1b", pemStr, "raw-key-material")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no PEM block")
		assert.Empty(t, fake.importCalls, "校验失败不产生云侧调用")
	})

	t.Run("vault目标未配置显式报错", func(t *testing.T) {
		adapter := NewCertAdapter(elog.DefaultLogger) // 无 Option 无 env
		factoryCalled := false
		adapter.newKVCertWriter = func(context.Context, *domain.CloudAccount, string) (azureKVCertWriter, error) {
			factoryCalled = true
			return &fakeKVCertWriter{}, nil
		}

		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN,
			"ecam-abcd1234-1-0a1b", pemStr, azureTestKeyPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "key vault target required")
		assert.False(t, factoryCalled, "vault 未配置不构造写客户端")
	})

	t.Run("限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{importErrFn: func(int) error {
			return fmt.Errorf("%w: kv import throttled", errAzureThrottled)
		}}
		injectFakeKVWriter(adapter, fake)

		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN,
			"ecam-abcd1234-1-0a1b", pemStr, azureTestKeyPEM)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("非法证书名与不支持产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{}
		injectFakeKVWriter(adapter, fake)

		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN,
			"bad name!", pemStr, azureTestKeyPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "naming constraint")

		_, err = adapter.UploadCert(context.Background(), certTestCreds(), "waf",
			"ecam-abcd1234-1-0a1b", pemStr, azureTestKeyPEM)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
		assert.Empty(t, fake.importCalls)
	})
}

// mustLeafFingerprint 解析 PEM 叶证书 SHA256 指纹（测试辅助）
func mustLeafFingerprint(t *testing.T, pemStr string) string {
	t.Helper()
	leaf, ok := parseCertLeafPEM(pemStr)
	require.True(t, ok)
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:])
}

// ==================== BindResource：ALB (Application Gateway) ====================

func TestCertAdapterBindAppGateway(t *testing.T) {
	newTarget := "https://vault-main.vault.azure.net/secrets/new-cert/v1"

	t.Run("监听器SSL证书资源PUT更新KV引用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/applicationGateways": {json.RawMessage(agwBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, "agw-1/lsn-kv", newTarget)
		require.NoError(t, err)

		require.Len(t, fake.putCalls, 1)
		call := fake.putCalls[0]
		assert.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert",
			call.url, "监听器经既有 SSL 证书资源引用 KV secret（非直接上传 ID）")
		var body struct {
			Name       string `json:"name"`
			Properties struct {
				KeyVaultSecretID string `json:"keyVaultSecretId"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(call.body, &body))
		assert.Equal(t, "kvcert", body.Name)
		assert.Equal(t, newTarget, body.Properties.KeyVaultSecretID)
	})

	t.Run("KV引用已等值幂等跳过", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/applicationGateways": {json.RawMessage(agwBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB,
			"agw-1/lsn-kv", "https://vault-old.vault.azure.net/secrets/old-cert/ver1")
		require.NoError(t, err)
		assert.Empty(t, fake.putCalls, "已引用目标 KV secret → no-op（回滚重放安全）")
	})

	t.Run("内联证书资源显式拒绝改绑", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/applicationGateways": {json.RawMessage(agwBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB,
			"agw-1/lsn-inline", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inline")
		assert.Empty(t, fake.putCalls, "盲区显式失败不猜测")
	})

	t.Run("网关与监听器缺失显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.Network/applicationGateways": {json.RawMessage(agwBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, "agw-x/lsn-kv", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		err = adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, "agw-1/lsn-x", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no ssl certificate reference")
	})

	t.Run("资源ID与引用形态非法显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		injectFakeBinder(adapter, &fakeARMBinder{fakeARMLister: &fakeARMLister{}})

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, "agw-1", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "composite form")

		err = adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, "agw-1/lsn", "not-a-kv-secret-id")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a key vault secret id")

		err = adapter.BindResource(context.Background(), certTestCreds(), "nlb", "agw-1/lsn", newTarget)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})
}

// ==================== BindResource：CDN (Front Door) ====================

func TestCertAdapterBindFrontDoor(t *testing.T) {
	newTarget := "https://vault-main.vault.azure.net/secrets/new-cert/v1"

	t.Run("vault资源ID解析与终结点PATCH启用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.KeyVault/vaults":    {json.RawMessage(vaultFixture)},
			"Microsoft.Network/frontdoors": {json.RawMessage(frontDoorBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "fd-1/fe-kv", newTarget)
		require.NoError(t, err)

		require.Len(t, fake.patchCalls, 1)
		call := fake.patchCalls[0]
		assert.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/frontDoors/fd-1/frontendEndpoints/fe-kv",
			call.url, "终结点级 PATCH（自定义域名 HTTPS 配置）")

		var body struct {
			Properties struct {
				CustomHTTPSConfiguration *struct {
					CertificateSource              string `json:"certificateSource"`
					ProtocolType                   string `json:"protocolType"`
					AzureKeyVaultCertificateSecret *struct {
						Vault struct {
							ID string `json:"id"`
						} `json:"vault"`
						SecretName    string `json:"secretName"`
						SecretVersion string `json:"secretVersion"`
					} `json:"azureKeyVaultCertificateSecret"`
				} `json:"customHttpsConfiguration"`
				CustomHTTPSProvisioningState string `json:"customHttpsProvisioningState"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(call.body, &body))
		cfg := body.Properties.CustomHTTPSConfiguration
		require.NotNil(t, cfg)
		assert.Equal(t, "AzureKeyVault", cfg.CertificateSource)
		assert.Equal(t, "ServerNameIndication", cfg.ProtocolType)
		require.NotNil(t, cfg.AzureKeyVaultCertificateSecret)
		assert.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/vault-main",
			cfg.AzureKeyVaultCertificateSecret.Vault.ID, "vault.id=ARM 资源 ID（订阅级清单按名解析）")
		assert.Equal(t, "new-cert", cfg.AzureKeyVaultCertificateSecret.SecretName)
		assert.Equal(t, "v1", cfg.AzureKeyVaultCertificateSecret.SecretVersion)
		assert.Equal(t, "Enabling", body.Properties.CustomHTTPSProvisioningState)
	})

	t.Run("secretId与组合字段两种幂等等值", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.KeyVault/vaults":    {json.RawMessage(vaultFixture)},
			"Microsoft.Network/frontdoors": {json.RawMessage(frontDoorBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		// secretId 直接等值
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN,
			"fd-1/fe-kv", "https://vault-old.vault.azure.net/secrets/old-cert/ver1")
		require.NoError(t, err)
		// vault/secretName/secretVersion 组合等值（大小写不敏感来源）
		err = adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN,
			"fd-1/fe-compose", "https://vault-old.vault.azure.net/secrets/old-cert/ver1")
		require.NoError(t, err)
		assert.Empty(t, fake.patchCalls, "两种配置形态等值均 no-op")
	})

	t.Run("vault缺失与终结点缺失显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.KeyVault/vaults":    {json.RawMessage(`{"id": "/v/other", "name": "other-vault"}`)},
			"Microsoft.Network/frontdoors": {json.RawMessage(frontDoorBindFixture)},
		}}}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "fd-1/fe-kv", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		adapter2 := newTestCertAdapter(t)
		fake2 := &fakeARMBinder{fakeARMLister: &fakeARMLister{items: map[string][]json.RawMessage{
			"Microsoft.KeyVault/vaults":    {json.RawMessage(vaultFixture)},
			"Microsoft.Network/frontdoors": {json.RawMessage(frontDoorBindFixture)},
		}}}
		injectFakeBinder(adapter2, fake2)
		err = adapter2.BindResource(context.Background(), certTestCreds(), CertProductCDN, "fd-1/fe-x", newTarget)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "frontend endpoint")
	})

	t.Run("限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeARMBinder{
			fakeARMLister: &fakeARMLister{err: fmt.Errorf("%w: arm throttled", errAzureThrottled)},
		}
		injectFakeBinder(adapter, fake)

		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "fd-1/fe-kv", newTarget)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

// ==================== CleanupOrphan：KV 证书删除 ====================

func TestCertAdapterCleanupOrphan(t *testing.T) {
	t.Run("按secretID解析证书名删除", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{}
		injectFakeKVWriter(adapter, fake)

		err := adapter.CleanupOrphan(context.Background(), certTestCreds(),
			"https://vault-main.vault.azure.net/secrets/ecam-abcd1234-1-0a1b/1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ecam-abcd1234-1-0a1b"}, fake.deletes, "删除目标=证书名（KV 删除按证书对象全体版本）")
		assert.Equal(t, "https://vault-main.vault.azure.net", fake.vault, "vault 取自 secret ID 自身（清理不依赖上传目标配置）")
	})

	t.Run("软删除后重复删除幂等成功", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{}
		injectFakeKVWriter(adapter, fake)

		secretID := "https://vault-main.vault.azure.net/secrets/ecam-abcd1234-1-0a1b/1"
		require.NoError(t, adapter.CleanupOrphan(context.Background(), certTestCreds(), secretID))
		require.NoError(t, adapter.CleanupOrphan(context.Background(), certTestCreds(), secretID),
			"已删除（404）→ 幂等成功，清理队列重放安全")
		assert.Len(t, fake.deletes, 2)
	})

	t.Run("限流映射哨兵与非法引用显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeKVCertWriter{deleteErrFn: func(int) error {
			return fmt.Errorf("%w: kv delete throttled", errAzureThrottled)
		}}
		injectFakeKVWriter(adapter, fake)

		err := adapter.CleanupOrphan(context.Background(), certTestCreds(),
			"https://vault-main.vault.azure.net/secrets/cert-1/1")
		assert.ErrorIs(t, err, ErrCloudRateLimited)

		err = adapter.CleanupOrphan(context.Background(), certTestCreds(), "arn-form-id")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a key vault secret id")
	})
}

// ==================== vault 目标解析与辅助 ====================

func TestEffectiveVaultURI(t *testing.T) {
	t.Run("Option优先于环境变量", func(t *testing.T) {
		adapter := NewCertAdapter(nil)
		assert.Empty(t, adapter.effectiveVaultURI(), "未配置为空（上传路径显式报错）")

		t.Setenv(envKeyVaultName, "env-vault")
		assert.Equal(t, "https://env-vault.vault.azure.net", adapter.effectiveVaultURI())

		t.Setenv(envKeyVaultURI, "https://env-uri.vault.azure.net/")
		assert.Equal(t, "https://env-uri.vault.azure.net", adapter.effectiveVaultURI(), "env URI 优先于 env name 且去尾斜杠")

		byName := NewCertAdapter(nil, WithKeyVaultName("opt-name"))
		assert.Equal(t, "https://opt-name.vault.azure.net", byName.effectiveVaultURI())
		byURI := NewCertAdapter(nil, WithKeyVaultURI("https://opt-uri.vault.azure.net"))
		assert.Equal(t, "https://opt-uri.vault.azure.net", byURI.effectiveVaultURI(), "显式 URI 优先级最高")
		byBoth := NewCertAdapter(nil, WithKeyVaultName("opt-name"), WithKeyVaultURI("https://opt-uri.vault.azure.net"))
		assert.Equal(t, "https://opt-uri.vault.azure.net", byBoth.effectiveVaultURI())
	})
}

func TestParseKvSecretID(t *testing.T) {
	loc, err := parseKvSecretID("https://Vault-Main.vault.azure.net/secrets/cert-1/v1")
	require.NoError(t, err)
	assert.Equal(t, "vault-main.vault.azure.net", loc.VaultHost)
	assert.Equal(t, "vault-main", loc.VaultName)
	assert.Equal(t, "cert-1", loc.SecretName)
	assert.Equal(t, "v1", loc.SecretVersion)

	loc, err = parseKvSecretID("https://vault.vault.azure.net/secrets/cert-1")
	require.NoError(t, err)
	assert.Equal(t, "cert-1", loc.SecretName)
	assert.Empty(t, loc.SecretVersion, "versionless 合法（发现引用兼容形态）")

	loc, err = parseKvSecretID("https://myvault.vault.azure.cn/secrets/cert-1/v1")
	require.NoError(t, err)
	assert.Equal(t, "myvault", loc.VaultName, "主权云后缀不影响首段提取")

	for _, bad := range []string{"", "arn:aws:acm:us-east-1:1:certificate/c1", "http://vault.vault.azure.net/secrets/c/1",
		"https://vault.vault.azure.net/certificates/c", "https://vault.vault.azure.net/secrets/"} {
		_, err := parseKvSecretID(bad)
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "not a key vault secret id")
	}
}

func TestVaultNameFromHost(t *testing.T) {
	assert.Equal(t, "vault-a", vaultNameFromHost("vault-a.vault.azure.net"))
	assert.Equal(t, "vault-a", vaultNameFromHost("Vault-A.Vault.Azure.Net"))
	assert.Equal(t, "v", vaultNameFromHost("v.vault.azure.cn"))
	assert.Equal(t, "bare", vaultNameFromHost("bare"))
}

func TestSplitCompositeResourceID(t *testing.T) {
	owner, sub, err := splitCompositeResourceID("agw-1/lsn-kv")
	require.NoError(t, err)
	assert.Equal(t, "agw-1", owner)
	assert.Equal(t, "lsn-kv", sub)

	owner, sub, err = splitCompositeResourceID(" fd-1 / fe-kv ")
	require.NoError(t, err)
	assert.Equal(t, "fd-1", owner)
	assert.Equal(t, "fe-kv", sub)

	for _, bad := range []string{"", "single", "/leading", "trailing/"} {
		_, _, err := splitCompositeResourceID(bad)
		require.Error(t, err, bad)
	}
}

// ==================== 真实 REST 写客户端（httptest 假端点） ====================

func TestKVCertWriterREST(t *testing.T) {
	t.Run("导入请求体形态与sid返回", func(t *testing.T) {
		var gotMethod, gotPath, gotQuery, gotAuth string
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
			gotAuth = r.Header.Get("Authorization")
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kid":"k1","sid":"https://v/secrets/cert-1/v1","cer":"AAEC"}`))
		}))
		defer server.Close()

		client := &kvRESTClient{token: &fakeTokenProvider{}, httpClient: server.Client(), vaultURI: server.URL}
		result, err := client.importCertificate(context.Background(), "cert-1", azureKVCertImport{
			Value:       []byte("QkFTRTY0"), // base64("BASE64")
			ContentType: certKVPemContentType,
		})
		require.NoError(t, err)
		assert.Equal(t, http.MethodPut, gotMethod)
		assert.Equal(t, "/certificates/cert-1", gotPath)
		assert.Equal(t, "api-version="+kvAPIVersion, gotQuery)
		assert.Equal(t, "Bearer fake-token", gotAuth)
		assert.Equal(t, "QkFTRTY0", gotBody["value"], "导入值=base64 文本（单层编码）")
		policy, _ := gotBody["policy"].(map[string]any)
		secretProps, _ := policy["secret_properties"].(map[string]any)
		assert.Equal(t, certKVPemContentType, secretProps["contentType"])
		assert.Equal(t, "https://v/secrets/cert-1/v1", result.SecretID)
		assert.Equal(t, "k1", result.KeyID)
		assert.Equal(t, "AAEC", result.CertificateBase64)
	})

	t.Run("删除404归类不存在与429归类限流", func(t *testing.T) {
		for _, tc := range []struct {
			status int
			want   error
		}{
			{http.StatusNotFound, errAzureNotFound},
			{http.StatusTooManyRequests, errAzureThrottled},
		} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				w.WriteHeader(tc.status)
			}))
			client := &kvRESTClient{token: &fakeTokenProvider{}, httpClient: server.Client(), vaultURI: server.URL}
			err := client.deleteCertificate(context.Background(), "cert-1")
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
			server.Close()
		}
	})
}

func TestARMRESTBinder(t *testing.T) {
	t.Run("PATCH与PUT携带api-version与Bearer", func(t *testing.T) {
		type rec struct{ method, query, auth, ct string }
		var calls []rec
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, rec{r.Method, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type")})
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		binder := &armRESTBinder{armRESTLister: &armRESTLister{
			token:        &fakeTokenProvider{},
			mgmtEndpoint: server.URL,
			httpClient:   server.Client(),
		}}
		require.NoError(t, binder.patchResource(context.Background(), "/sub/rg/providers/Microsoft.Network/frontDoors/fd-1/frontendEndpoints/fe-1", []byte(`{}`)))
		require.NoError(t, binder.putResource(context.Background(), "/sub/rg/providers/Microsoft.Network/applicationGateways/agw-1/sslCertificates/kvcert", []byte(`{}`)))

		require.Len(t, calls, 2)
		assert.Equal(t, http.MethodPatch, calls[0].method)
		assert.Equal(t, http.MethodPut, calls[1].method)
		for _, c := range calls {
			assert.Equal(t, "api-version="+armAPIVersion, c.query)
			assert.Equal(t, "Bearer fake-token", c.auth)
			assert.Equal(t, "application/json", c.ct)
		}
	})

	t.Run("写失败按状态码归类", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()
		binder := &armRESTBinder{armRESTLister: &armRESTLister{
			token: &fakeTokenProvider{}, mgmtEndpoint: server.URL, httpClient: server.Client(),
		}}
		err := binder.patchResource(context.Background(), "/x", []byte(`{}`))
		require.Error(t, err)
		assert.ErrorIs(t, err, errAzureThrottled)
	})
}

// ==================== 编译期接口满足断言 ====================

var (
	_ azureKVCertWriter = (*kvRESTClient)(nil)
	_ azureARMBinder    = (*armRESTBinder)(nil)
)
