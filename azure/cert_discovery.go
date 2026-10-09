// Package azure Azure 只读证书发现最小适配（任务 3.3 新建目录）。
// Azure 无既有 cloudx client 基座：本包不做全量资源适配，仅实现证书发现所需的
// 最小凭证加载（复用平台云账号存储的 azure 凭证字段：AccessKeyID=Client ID、
// AccessKeySecret=Client Secret）与只读 ARM/Key Vault REST 调用（net/http 直调，
// 不引入 Azure SDK 依赖）；租户/订阅 ID 平台账号模型未存储，经 Option 注入或
// 环境变量（AZURE_TENANT_ID / AZURE_SUBSCRIPTION_ID）回退解析。
//
// 适配形态对齐 tech-design Interface 2: CloudDeployer 的 discovery-only 实现：
// 仅实现 ListReferences / GetCert 两个只读方法，UploadCert / BindResource /
// CleanupOrphan 一律返回哨兵 ErrDiscoveryOnly（ERR_DISCOVERY_ONLY 语义），
// 不产生任何云侧写操作（代码评审口径的只读硬约束）。
package azure

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"golang.org/x/time/rate"
)

// 产品映射（枚举对齐 schema.sql cert_references.product，各云以实际产品映射）：
//   - cdn：Front Door（classic）前端终结点自定义 HTTPS 证书（Key Vault 证书引用）
//   - alb：Application Gateway 监听器 SSL 证书（Key Vault 引用形态；内联上传证书
//     无云侧证书 ID，属发现盲区，跳过并计数声明）
//
// Azure WAF policy 附加于 Front Door / Application Gateway，自身无证书引用面，
// 不入支持集（首期盲区声明见 PRD 引用发现口径）；Azure Load Balancer 为四层
// 转发、无 TLS 终结证书，同样不入支持集。

// 证书产品枚举（对齐 schema.sql cert_references.product Azure 引用面）
const (
	// CertProductCDN Front Door 前端终结点自定义 HTTPS 证书
	CertProductCDN = "cdn"
	// CertProductALB Application Gateway 监听器 SSL 证书
	CertProductALB = "alb"
)

// certSupportedProducts 只读发现支持的产品集合（未实现产品显式报错而非静默）
var certSupportedProducts = map[string]bool{
	CertProductCDN: true,
	CertProductALB: true,
}

// 证书域哨兵错误
var (
	// ErrCloudRateLimited 云 API 限流/流控（CLOUD_API_RATELIMITED 语义）。
	// 定义位于 cloudx 公共层（tech-design Error Handling），本包再导出以对齐 3.1/3.2 调用形态。
	ErrCloudRateLimited = cloudx.ErrCloudRateLimited
	// ErrDiscoveryOnly 只读发现哨兵（ERR_DISCOVERY_ONLY 语义）：本适配无部署器，
	// 三个写方法一律返回，不产生任何云侧写操作。
	ErrDiscoveryOnly = cloudx.ErrDiscoveryOnly
	// ErrCertProductNotSupported 未实现的证书产品/接口（显式报错而非静默）
	ErrCertProductNotSupported = errors.New("azure cert product not supported")
)

// CloudCertRef 云侧证书引用（对齐 CertReference 落库字段需求：
// cloud/product/resourceId/referencedCloudCertId/accountKey，见 schema.sql cert_references）
type CloudCertRef struct {
	Cloud                 string // 固定 "azure"
	Product               string // cdn|alb
	ResourceID            string // 云资源标识：CDN="{FrontDoor}/{FrontendEndpoint}"；ALB="{Gateway}/{Listener}"
	ReferencedCloudCertID string // 云侧证书标识：Key Vault secret ID（可带或不带版本）
	AccountKey            string // 云账号标识（取 CloudAccount.Name）
}

// CloudCertInfo GetCert 返回的云侧证书在库状态（只读）
type CloudCertInfo struct {
	Exists      bool      // Key Vault 中该 secret 是否存在
	NotAfter    time.Time // 云侧证书有效期截止；Exists=false 时零值
	Fingerprint string    // PEM 解析的 SHA256 hex（对齐台账指纹 ^[0-9a-f]{64}$）；非证书 secret 为空
	// CertChainPEM 仅 CERTIFICATE 块的净化序列（叶在前 fullchain 口径）：
	// Key Vault secret 全量值（exportable 密钥策略下含私钥 bundle）必须走本净化，
	// 块级过滤构造性保证（cloudx.SanitizeCertChainPEM），不含 PRIVATE KEY 等
	// 任何非证书内容；非证书 secret 为空。发现导入材料通道（cert-cloud-discovery-import）。
	CertChainPEM string
}

// REST 端点与 API 版本常量（Azure 全球云；中国区经 Option 覆盖）
const (
	defaultLoginEndpoint = "https://login.microsoftonline.com"
	defaultMgmtEndpoint  = "https://management.azure.com"
	armAPIVersion        = "2024-02-01"
	kvAPIVersion         = "7.4"
	// armScopeValue ARM 管理面 OAuth scope
	armScopeValue = "https://management.azure.com/.default"
	// kvScopeValue Key Vault 数据面 OAuth scope
	kvScopeValue = "https://vault.azure.net/.default"
	// certDefaultPageSize ARM $top 分页大小
	certDefaultPageSize = 50
	// tokenEarlyExpiry 提前失效时间（取新令牌的安全余量）
	tokenEarlyExpiry = 2 * time.Minute
)

// azureTokenProvider OAuth2 client_credentials 令牌抽象（真实实现走 AAD v2 端点，测试注入 fake）
type azureTokenProvider interface {
	token(ctx context.Context, scope string) (string, error)
}

// azureARMLister ARM 订阅级资源列举抽象（真实实现走 ARM REST，测试注入 fake）
type azureARMLister interface {
	list(ctx context.Context, resourceType, apiVersion string) ([]json.RawMessage, error)
}

// azureKVSecretGetter Key Vault secret 读取抽象（真实实现走 KV 数据面 REST，
// 测试注入 fake）。返回原始字节而非 string：secret 全量值（exportable 密钥策略
// 下含私钥 bundle）由调用方净化后对原始 buffer 执行 Zeroize——string 不可变，
// 字节切片才能满足"净化前原始 buffer 用后归零"的构造性安全口径。
type azureKVSecretGetter interface {
	getSecret(ctx context.Context, secretID string) ([]byte, error)
}

// CertDiscoveryAdapter Azure 只读证书发现适配器：按产品分发。
// 凭证复用既有云账号体系（*domain.CloudAccount），逐调用传入；
// tenantID / subscriptionID / 端点经 Option 注入（缺省回退环境变量），
// REST 客户端工厂字段可被测试注入 fake。
type CertDiscoveryAdapter struct {
	logger       *elog.Component
	rateLimiter  *rate.Limiter
	listPageSize int

	tenantID       string
	subscriptionID string
	loginEndpoint  string
	mgmtEndpoint   string

	newToken func(creds *domain.CloudAccount) (azureTokenProvider, error)
	newARM   func(creds *domain.CloudAccount) (azureARMLister, error)
	newKV    func(creds *domain.CloudAccount) (azureKVSecretGetter, error)
}

// CertDiscoveryOption 适配器可选配置
type CertDiscoveryOption func(*CertDiscoveryAdapter)

// WithTenantID 注入 Azure 租户 ID（平台账号模型未存储，调用方必须显式提供或经环境变量）
func WithTenantID(tenantID string) CertDiscoveryOption {
	return func(a *CertDiscoveryAdapter) { a.tenantID = strings.TrimSpace(tenantID) }
}

// WithSubscriptionID 注入 Azure 订阅 ID（同上）
func WithSubscriptionID(subscriptionID string) CertDiscoveryOption {
	return func(a *CertDiscoveryAdapter) { a.subscriptionID = strings.TrimSpace(subscriptionID) }
}

// WithEndpoints 覆盖 AAD 登录与 ARM 管理端点（中国区等环境）
func WithEndpoints(loginEndpoint, mgmtEndpoint string) CertDiscoveryOption {
	return func(a *CertDiscoveryAdapter) {
		if strings.TrimSpace(loginEndpoint) != "" {
			a.loginEndpoint = strings.TrimSpace(loginEndpoint)
		}
		if strings.TrimSpace(mgmtEndpoint) != "" {
			a.mgmtEndpoint = strings.TrimSpace(mgmtEndpoint)
		}
	}
}

// 环境变量名常量
const (
	envTenantID       = "AZURE_TENANT_ID"
	envSubscriptionID = "AZURE_SUBSCRIPTION_ID"
)

// NewCertDiscoveryAdapter 创建 Azure 只读证书发现适配器（默认真实 REST 客户端工厂；
// tenant/subscription 缺省经环境变量回退解析，20 QPS 限流口径与其他云一致）
func NewCertDiscoveryAdapter(logger *elog.Component, opts ...CertDiscoveryOption) *CertDiscoveryAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	adapter := &CertDiscoveryAdapter{
		logger:        logger,
		rateLimiter:   rate.NewLimiter(20, 20),
		listPageSize:  certDefaultPageSize,
		loginEndpoint: defaultLoginEndpoint,
		mgmtEndpoint:  defaultMgmtEndpoint,
	}
	for _, opt := range opts {
		opt(adapter)
	}
	if adapter.tenantID == "" {
		adapter.tenantID = strings.TrimSpace(os.Getenv(envTenantID))
	}
	if adapter.subscriptionID == "" {
		adapter.subscriptionID = strings.TrimSpace(os.Getenv(envSubscriptionID))
	}
	adapter.newToken = func(creds *domain.CloudAccount) (azureTokenProvider, error) {
		return newRESTTokenProvider(adapter, creds)
	}
	adapter.newARM = func(creds *domain.CloudAccount) (azureARMLister, error) {
		return newARMRESTLister(adapter, creds)
	}
	adapter.newKV = func(creds *domain.CloudAccount) (azureKVSecretGetter, error) {
		return newKVRESTClient(adapter, creds)
	}
	return adapter
}

// ==================== 只读硬约束：三个写方法一律返回哨兵 ====================

// UploadCert 两段式第一段（discovery-only 云未实现）：Azure 首期无部署器，一律返回
// ErrDiscoveryOnly，不产生任何云侧写操作（PRD Out of Scope：三云部署器二期）
func (a *CertDiscoveryAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	_ = ctx
	_ = creds
	_ = name
	_ = certPEM
	_ = keyPEM
	return "", fmt.Errorf("%w: azure %s cert upload not implemented", ErrDiscoveryOnly, product)
}

// BindResource 两段式第二段（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = resourceID
	_ = cloudCertID
	return fmt.Errorf("%w: azure %s cert bind not implemented", ErrDiscoveryOnly, product)
}

// CleanupOrphan 孤儿清理（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = cloudCertID
	return fmt.Errorf("%w: azure cert cleanup not implemented", ErrDiscoveryOnly)
}

// ==================== 只读发现 ====================

// ListReferences 只读发现：列出产品下全部证书引用（按产品分发）
func (a *CertDiscoveryAdapter) ListReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	switch product {
	case CertProductCDN:
		return a.listFrontDoorReferences(ctx, creds)
	case CertProductALB:
		return a.listAppGatewayReferences(ctx, creds)
	default:
		return nil, certProductNotSupported(product)
	}
}

// listFrontDoorReferences 遍历 Front Door（classic）前端终结点自定义 HTTPS 证书：
// 仅 certificateSource=AzureKeyVault 的 Key Vault 引用构成自持证书引用
// （FrontDoor 托管证书无自持引用）；secretId 缺失时回退 vault/name/version 组合。
func (a *CertDiscoveryAdapter) listFrontDoorReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("azure frontdoor cert list: nil creds")
	}
	lister, err := a.newARM(creds)
	if err != nil {
		return nil, err
	}
	items, err := lister.list(ctx, "Microsoft.Network/frontdoors", armAPIVersion)
	if err != nil {
		return nil, wrapCertCloudErr(CertProductCDN, err)
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, item := range items {
		var door frontDoorResource
		if err := json.Unmarshal(item, &door); err != nil {
			a.logger.Warn("解析FrontDoor资源失败，跳过", elog.FieldErr(err))
			continue
		}
		for _, endpoint := range door.Properties.FrontendEndpoints {
			cfg := endpoint.Properties.CustomHTTPSConfiguration
			if cfg == nil || !strings.EqualFold(cfg.CertificateSource, "AzureKeyVault") {
				// 托管证书/未配置自定义 HTTPS 不构成自持证书引用
				continue
			}
			secretID := cfg.SecretID
			if secretID == "" {
				secretID = composeKvSecretID(cfg.AzureKeyVaultCertificateSecret)
			}
			if secretID == "" || endpoint.Name == "" || door.Name == "" {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "azure",
				Product:               CertProductCDN,
				ResourceID:            door.Name + "/" + endpoint.Name,
				ReferencedCloudCertID: secretID,
				AccountKey:            accountKey,
			})
		}
	}
	a.logger.Info("获取Azure FrontDoor证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// kvSecretRef Front Door Key Vault 证书引用组合字段
type kvSecretRef struct {
	VaultID       string `json:"vaultId"`
	SecretName    string `json:"secretName"`
	SecretVersion string `json:"secretVersion"`
}

// frontDoorResource Front Door（classic）资源最小解析结构（仅证书发现所需字段）
type frontDoorResource struct {
	Name       string `json:"name"`
	Properties struct {
		FrontendEndpoints []struct {
			Name       string `json:"name"`
			Properties struct {
				CustomHTTPSConfiguration *struct {
					CertificateSource              string       `json:"certificateSource"`
					SecretID                       string       `json:"secretId"`
					AzureKeyVaultCertificateSecret *kvSecretRef `json:"azureKeyVaultCertificateSecret"`
				} `json:"customHttpsConfiguration"`
			} `json:"properties"`
		} `json:"frontendEndpoints"`
	} `json:"properties"`
}

// composeKvSecretID 组合 Key Vault secret 标识（vaultId + secretName + secretVersion；
// 版本缺省为 versionless 形态，读取时返回最新版本）
func composeKvSecretID(kv *kvSecretRef) string {
	if kv == nil || kv.VaultID == "" || kv.SecretName == "" {
		return ""
	}
	secretID := strings.TrimSuffix(kv.VaultID, "/") + "/secrets/" + kv.SecretName
	if kv.SecretVersion != "" {
		secretID += "/" + kv.SecretVersion
	}
	return secretID
}

// listAppGatewayReferences 遍历 Application Gateway 监听器 SSL 证书：
// 仅 Key Vault 引用形态（keyVaultSecretId）构成云侧证书标识；
// 内联上传证书（data 字段）无云侧证书 ID，属发现盲区，跳过并计数声明。
func (a *CertDiscoveryAdapter) listAppGatewayReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("azure appgateway cert list: nil creds")
	}
	lister, err := a.newARM(creds)
	if err != nil {
		return nil, err
	}
	items, err := lister.list(ctx, "Microsoft.Network/applicationGateways", armAPIVersion)
	if err != nil {
		return nil, wrapCertCloudErr(CertProductALB, err)
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	inlineSkipped := 0
	for _, item := range items {
		var gateway appGatewayResource
		if err := json.Unmarshal(item, &gateway); err != nil {
			a.logger.Warn("解析ApplicationGateway资源失败，跳过", elog.FieldErr(err))
			continue
		}
		// 监听器引用的 SSL 证书资源 ID → Key Vault secret ID 映射
		kvByCertID := make(map[string]string, len(gateway.Properties.SSLCertificates))
		for _, cert := range gateway.Properties.SSLCertificates {
			if cert.ID != "" && cert.Properties.KeyVaultSecretID != "" {
				kvByCertID[cert.ID] = cert.Properties.KeyVaultSecretID
			}
		}
		for _, listener := range gateway.Properties.HTTPListeners {
			if listener.Name == "" || gateway.Name == "" {
				continue
			}
			certRef := listener.Properties.SSLCertificate
			if certRef == nil || certRef.ID == "" {
				// 非 HTTPS 监听器不构成证书引用
				continue
			}
			secretID, ok := kvByCertID[certRef.ID]
			if !ok {
				// 内联上传证书无云侧证书 ID：发现盲区，跳过并计数（视图层盲区声明口径）
				inlineSkipped++
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "azure",
				Product:               CertProductALB,
				ResourceID:            gateway.Name + "/" + listener.Name,
				ReferencedCloudCertID: secretID,
				AccountKey:            accountKey,
			})
		}
	}
	if inlineSkipped > 0 {
		a.logger.Warn("ApplicationGateway存在内联上传证书监听器，无云侧证书ID，计为发现盲区",
			elog.Int("skipped", inlineSkipped))
	}
	a.logger.Info("获取Azure ApplicationGateway证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// appGatewayResource Application Gateway 资源最小解析结构（仅证书发现所需字段）
type appGatewayResource struct {
	Name       string `json:"name"`
	Properties struct {
		SSLCertificates []struct {
			ID         string `json:"id"`
			Properties struct {
				KeyVaultSecretID string `json:"keyVaultSecretId"`
			} `json:"properties"`
		} `json:"sslCertificates"`
		HTTPListeners []struct {
			Name       string `json:"name"`
			Properties struct {
				SSLCertificate *struct {
					ID string `json:"id"`
				} `json:"sslCertificate"`
			} `json:"properties"`
		} `json:"httpListeners"`
	} `json:"properties"`
}

// GetCert 查询 Key Vault secret 在库状态（只读）：证书类 secret 的全量值经
// 仅 CERTIFICATE 块净化（exportable 密钥策略下 secret 值含私钥 bundle，必须
// 丢弃私钥并按叶在前口径拼装），解析出 SHA256 指纹与有效期；secret 不存在 →
// Exists=false；非证书类 secret（普通机密）Exists=true 且指纹为空（上层按
// "无法复核"处理）。原始 secret buffer 净化后即刻 Zeroize。
func (a *CertDiscoveryAdapter) GetCert(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("azure cert get: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return CloudCertInfo{}, fmt.Errorf("azure cert get: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return CloudCertInfo{}, err
	}
	kv, err := a.newKV(creds)
	if err != nil {
		return CloudCertInfo{}, err
	}
	raw, err := kv.getSecret(ctx, strings.TrimSpace(cloudCertID))
	if err != nil {
		if errors.Is(err, errAzureNotFound) {
			// 云侧已删除 → Exists=false（非错误）
			return CloudCertInfo{Exists: false}, nil
		}
		return CloudCertInfo{}, wrapCertCloudErr("keyvault", err)
	}
	info := CloudCertInfo{Exists: true}
	// PEM 通道净化（构造性保证）：仅保留 CERTIFICATE 块的净化序列；
	// 原始 secret buffer（可能含私钥 bundle）净化后即刻归零。
	info.CertChainPEM = cloudx.SanitizeCertChainPEM(raw)
	cloudx.Zeroize(raw)
	if leaf, ok := parseCertLeafPEM(info.CertChainPEM); ok {
		sum := sha256.Sum256(leaf.Raw)
		info.Fingerprint = hex.EncodeToString(sum[:])
		info.NotAfter = leaf.NotAfter
	}
	return info, nil
}

// ==================== 真实 REST 客户端（net/http 直调，无 Azure SDK 依赖） ====================

// azureREST 包内错误哨兵（REST 层到适配层的限流/非存在信号）
var (
	errAzureNotFound  = errors.New("azure resource not found")
	errAzureThrottled = errors.New("azure request throttled")
)

// restTokenProvider OAuth2 client_credentials 令牌提供者（按 scope 缓存至临近过期）
type restTokenProvider struct {
	loginEndpoint string
	tenantID      string
	clientID      string
	clientSecret  string
	httpClient    *http.Client

	mu     sync.Mutex
	tokens map[string]cachedToken
}

// cachedToken 缓存的访问令牌
type cachedToken struct {
	value     string
	expiresAt time.Time
}

// newRESTTokenProvider 构建 AAD v2 令牌提供者（凭证/租户缺失显式报错）
func newRESTTokenProvider(a *CertDiscoveryAdapter, creds *domain.CloudAccount) (azureTokenProvider, error) {
	if creds == nil {
		return nil, fmt.Errorf("azure cert discovery: nil creds")
	}
	if creds.AccessKeyID == "" || creds.AccessKeySecret == "" {
		return nil, fmt.Errorf("azure cert discovery: client id/secret required")
	}
	if a.tenantID == "" {
		return nil, fmt.Errorf("azure cert discovery: tenant id required (option WithTenantID or env %s)", envTenantID)
	}
	return &restTokenProvider{
		loginEndpoint: a.loginEndpoint,
		tenantID:      a.tenantID,
		clientID:      creds.AccessKeyID,
		clientSecret:  creds.AccessKeySecret,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		tokens:        make(map[string]cachedToken),
	}, nil
}

// token 获取指定 scope 的访问令牌（命中未过期缓存直接返回）
func (p *restTokenProvider) token(ctx context.Context, scope string) (string, error) {
	p.mu.Lock()
	cached, ok := p.tokens[scope]
	p.mu.Unlock()
	if ok && time.Now().Before(cached.expiresAt.Add(-tokenEarlyExpiry)) {
		return cached.value, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("scope", scope)
	endpoint := strings.TrimSuffix(p.loginEndpoint, "/") + "/" + p.tenantID + "/oauth2/v2.0/token"

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("构建Azure令牌请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("请求Azure令牌失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取Azure令牌响应失败: %w", err)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: aad token endpoint rate limited", errAzureThrottled)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("azure aad token error: http %d: %s", response.StatusCode, truncateForLog(string(body)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("解析Azure令牌响应失败: %w", err)
	}
	expiresAt := time.Now()
	if payload.ExpiresIn > 0 {
		expiresAt = expiresAt.Add(time.Duration(payload.ExpiresIn) * time.Second)
	} else {
		expiresAt = expiresAt.Add(time.Hour)
	}
	p.mu.Lock()
	p.tokens[scope] = cachedToken{value: payload.AccessToken, expiresAt: expiresAt}
	p.mu.Unlock()
	return payload.AccessToken, nil
}

// newARMRESTLister 构建 ARM 订阅级资源列举客户端（订阅缺失显式报错）
func newARMRESTLister(a *CertDiscoveryAdapter, creds *domain.CloudAccount) (azureARMLister, error) {
	if a.subscriptionID == "" {
		return nil, fmt.Errorf("azure cert discovery: subscription id required (option WithSubscriptionID or env %s)", envSubscriptionID)
	}
	token, err := newRESTTokenProvider(a, creds)
	if err != nil {
		return nil, err
	}
	return &armRESTLister{
		token:          token,
		mgmtEndpoint:   a.mgmtEndpoint,
		subscriptionID: a.subscriptionID,
		pageSize:       a.certPageSize(),
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// armRESTLister ARM REST 订阅级列举客户端（$top 分页 + nextLink 跟随）
type armRESTLister struct {
	token          azureTokenProvider
	mgmtEndpoint   string
	subscriptionID string
	pageSize       int
	httpClient     *http.Client
}

// list 列举订阅下指定资源类型全部实例（跟随 nextLink 翻页）
func (l *armRESTLister) list(ctx context.Context, resourceType, apiVersion string) ([]json.RawMessage, error) {
	next := fmt.Sprintf("%s/subscriptions/%s/providers/%s?api-version=%s&$top=%d",
		strings.TrimSuffix(l.mgmtEndpoint, "/"), l.subscriptionID, resourceType, apiVersion, l.pageSize)
	var items []json.RawMessage
	visited := make(map[string]bool)
	for next != "" {
		if visited[next] {
			return nil, fmt.Errorf("azure arm list: nextLink loop detected")
		}
		visited[next] = true
		body, err := azureGET(ctx, l.httpClient, l.token, armScopeValue, next, "ARM")
		if err != nil {
			return nil, err
		}
		var payload struct {
			Value    []json.RawMessage `json:"value"`
			NextLink string            `json:"nextLink"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("解析Azure ARM响应失败: %w", err)
		}
		items = append(items, payload.Value...)
		next = relativeAzureURL(l.mgmtEndpoint, payload.NextLink)
	}
	return items, nil
}

// newKVRESTClient 构建 Key Vault 数据面客户端
func newKVRESTClient(a *CertDiscoveryAdapter, creds *domain.CloudAccount) (azureKVSecretGetter, error) {
	token, err := newRESTTokenProvider(a, creds)
	if err != nil {
		return nil, err
	}
	return &kvRESTClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// kvRESTClient Key Vault secret 读取客户端（数据面 REST，Bearer kvScope）
type kvRESTClient struct {
	token      azureTokenProvider
	httpClient *http.Client
	// vaultURI 数据面基址（完整适配 cert.go 写路径——证书导入/删除——所需；
	// secret 读取路径按传入 secretID 全 URL 走，本字段不参与）
	vaultURI string
}

// getSecret 读取指定 secretID 的最新（或固定版本）值（404 → 不存在哨兵）。
// 返回 secret 值的字节副本；含 secret 明文的原始 HTTP 响应 buffer 解析后
// 即刻 Zeroize（副本由调用方净化后同样归零）。
func (c *kvRESTClient) getSecret(ctx context.Context, secretID string) ([]byte, error) {
	endpoint := strings.TrimSuffix(secretID, "/") + "?api-version=" + kvAPIVersion
	body, err := azureGET(ctx, c.httpClient, c.token, kvScopeValue, endpoint, "KeyVault")
	if err != nil {
		return nil, err
	}
	defer cloudx.Zeroize(body)
	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析Azure KeyVault响应失败: %w", err)
	}
	return []byte(payload.Value), nil
}

// readAzureResponse 读取响应体并按状态码归类错误（404/429 → 包内哨兵）
func readAzureResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取Azure响应失败: %w", err)
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, errAzureNotFound
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: http %d", errAzureThrottled, response.StatusCode)
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, fmt.Errorf("azure api error: http %d: %s", response.StatusCode, truncateForLog(string(body)))
	}
	return body, nil
}

// azureGET 授权 GET 并读取响应体（ARM/KeyVault REST 共用：Bearer token +
// Accept JSON；errPrefix 为请求构建/发送失败的语境前缀，错误文案与既有口径一致）。
func azureGET(ctx context.Context, httpClient *http.Client, token azureTokenProvider, scope, url, errPrefix string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构建Azure %s请求失败: %w", errPrefix, err)
	}
	accessToken, err := token.token(ctx, scope)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求Azure %s失败: %w", errPrefix, err)
	}
	return readAzureResponse(response)
}

// relativeAzureURL 将 nextLink 归一化为可请求 URL：绝对 http(s) 链接原样
// 返回（mgmtEndpoint 恒带 scheme，同源绝对 nextLink 天然命中该分支），
// 相对路径拼接管理端点。
func relativeAzureURL(mgmtEndpoint, nextLink string) string {
	if nextLink == "" {
		return ""
	}
	base := strings.TrimSuffix(mgmtEndpoint, "/")
	if strings.HasPrefix(nextLink, "http://") || strings.HasPrefix(nextLink, "https://") {
		return nextLink
	}
	return base + "/" + strings.TrimPrefix(nextLink, "/")
}

// truncateForLog 截断错误响应体（避免大响应进日志，且不回显机密内容全量）
func truncateForLog(s string) string {
	const max = 256
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// parseCertLeafPEM 解析 PEM 证书内容（GetCert 按 PEM 计算 SHA256 指纹）
func parseCertLeafPEM(pemStr string) (*x509.Certificate, bool) {
	if pemStr == "" {
		return nil, false
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false
	}
	return leaf, true
}

// ==================== 公共辅助 ====================

// certPageSize 获取列举分页大小（零值回退默认）
func (a *CertDiscoveryAdapter) certPageSize() int {
	if a.listPageSize <= 0 {
		return certDefaultPageSize
	}
	return a.listPageSize
}

// waitRateLimit 等待限流令牌
func (a *CertDiscoveryAdapter) waitRateLimit(ctx context.Context) error {
	if a.rateLimiter == nil {
		return nil
	}
	return a.rateLimiter.Wait(ctx)
}

// certProductNotSupported 未支持产品显式报错（ListReferences 分发兜底）
func certProductNotSupported(product string) error {
	return fmt.Errorf("%w: %q", ErrCertProductNotSupported, product)
}

// wrapCertCloudErr 云 API 错误统一包装：限流/流控映射哨兵 ErrCloudRateLimited，其余带服务上下文透传
func wrapCertCloudErr(service string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errAzureThrottled) {
		return fmt.Errorf("%w: azure %s api throttled: %v", ErrCloudRateLimited, service, err)
	}
	return fmt.Errorf("azure %s api error: %w", service, err)
}
