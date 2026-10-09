// cert.go Azure 完整证书适配器（cert-multicloud-deployers 任务 3）。
//
// 分层定位：与 3.1/3.2 aliyun/tencent 及任务 1/2 huawei/aws CertAdapter 同构——
// 五方法（UploadCert/BindResource/ListReferences/GetCert/CleanupOrphan）云 API
// 单次调用封装，供 deployer.AzureDeployer 组装为 CloudDeployer 端口实例（任务 4
// 装配收敛：与扫描适配共享实例，aliyun 模式）。
//
// 与既有 CertDiscoveryAdapter（任务 3.3，discovery-only）的关系：本类型内嵌
// 其指针复用只读发现（ListReferences/GetCert）与限流/工厂设施，并覆盖三个写
// 方法为真实云调用——只读哨兵约束（ErrDiscoveryOnly）仍由发现适配器类型自身
// 承载，本类型为部署能力形态，二者并存（任务 4 装配切换）。
//
// 云侧 ID 形态口径（与发现适配注释一致，Hard Rule：绑定与回滚统一 KV 引用形态，
// 归一化收敛在本适配层单点）：
//   - 证书库 = Key Vault（账号级，无地域/产品语义），云证书 ID = KV secret ID
//     （https://{vault}.vault.azure.net/secrets/{name}/{version}，含版本；sid 缺失
//     时回退 versionless 形态，与发现引用兼容）——映射表直接承载；
//   - ALB（Application Gateway）绑定 = 监听器既有 SSL 证书资源改指 KV secret
//     （keyVaultSecretId，子资源 PUT），不经直接上传 ID；内联 data 证书资源与
//     KV 引用形态互斥（发现侧同口径盲区），显式失败不猜测；
//   - CDN（Front Door classic）绑定 = 前端终结点自定义域名 HTTPS 配置 PATCH
//     （certificateSource=AzureKeyVault + azureKeyVaultCertificateSecret，
//     provisioning state=Enabling 触发配置变更）；
//   - 资源定位：部署目标 resourceId 为发现适配的复合形态（"{资源名}/{子资源名}"），
//     完整 ARM 资源 ID（含资源组）经订阅级清单按名反查（vault/Front Door/
//     Application Gateway 同一解析路径）。
//
// 上传形态：KV 证书导入 API（PUT /certificates/{name}）原生支持 PFX/PEM 含私钥
// 形态——本层材料源为台账 PEM 明文私钥，恒走 PEM 路径（value=base64〔证书束叶在
// 前 + 明文私钥〕，contentType=application/x-pem-file）；PEM 形态使 KV secret
// 值即该 PEM，发现适配 GetCert 的净化/指纹口径（首个 CERTIFICATE 块=叶）原样
// 成立，SHA256 指纹对齐可复核。PFX 分支（value=base64 PFX + pwd）留作单点扩展。
//
// 限流语义：Azure KV 数据面/ARM 管理面均为请求计数限流（HTTP 429），REST 层
// 归一映射 errAzureThrottled → wrapCertCloudErr → ErrCloudRateLimited 哨兵，
// 退避重试有界策略归 deployer 层（复用既有 RetryPolicy，Hard Rule 禁止无限重试）。
//
// 安全口径：私钥明文仅内存传递（导入束与 base64 副本调用完成后即刻归零，
// Hard Rule），禁入日志与错误文案；KV 删除进软删除态（保留期可恢复，幂等重放
// 安全），purge 为不可逆破坏性操作，安全侧不执行。
package azure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// KV 写面/ARM 绑定面窄接口（真实 REST 客户端天然满足；测试注入 fake——与发现
// 适配只读窄接口命名区分）。
type (
	// azureKVCertWriter Key Vault 证书写面窄接口：导入（上传）/删除（清理）。
	azureKVCertWriter interface {
		importCertificate(ctx context.Context, certName string, params azureKVCertImport) (azureKVCertImportResult, error)
		deleteCertificate(ctx context.Context, certName string) error
	}
	// azureARMBinder ARM 管理面绑定窄接口：订阅级资源列举（复用发现口径）+
	// 子资源/终结点级写（PUT/PUT）。PATCH 用于 Front Door 终结点自定义 HTTPS
	// 配置，PUT 用于 Application Gateway SSL 证书子资源。
	azureARMBinder interface {
		azureARMLister
		patchResource(ctx context.Context, resourceURL string, body []byte) error
		putResource(ctx context.Context, resourceURL string, body []byte) error
	}
)

// azureKVCertImport KV 证书导入参数（含私钥材料，调用完成后归零）
type azureKVCertImport struct {
	// Value 证书对象 base64 文本（PEM 形态=base64〔证书束+明文私钥〕；PFX 形态=
	// base64 PFX）。含私钥材料：[]byte 承载以保调用后可归零（string 不可变）。
	Value []byte
	// Password PFX 私钥口令（PEM 路径为空）。
	Password []byte
	// ContentType secret 形态：application/x-pem-file | application/x-pkcs12。
	ContentType string
}

// azureKVCertImportResult KV 证书导入结果
type azureKVCertImportResult struct {
	// KeyID kid（密钥对象 ID）
	KeyID string
	// SecretID sid（secret 对象 ID，含版本）——云证书 ID 形态
	SecretID string
	// CertificateBase64 cer（导入证书 DER base64；指纹自证备用）
	CertificateBase64 string
}

// 证书产品常量补充（写面语境）
const (
	// certKVPemContentType KV 证书 PEM 形态 secret content type（secret 值即
	// PEM 文件——发现适配 GetCert 的 PEM 解析/指纹口径原样成立）
	certKVPemContentType = "application/x-pem-file"
	// certKVPfxContentType KV 证书 PFX 形态（KV 导入 API 原生支持；本层材料源
	// 为台账 PEM 明文私钥，恒走 PEM 路径——分支留作单点扩展）
	certKVPfxContentType = "application/x-pkcs12"
	// certKVCertSourceAzureKeyVault Front Door 自定义 HTTPS 证书来源（KV 引用）
	certKVCertSourceAzureKeyVault = "AzureKeyVault"
	// certKVProtocolSNI 自定义域名 HTTPS SNI 协议类型（多域名共存缺省口径）
	certKVProtocolSNI = "ServerNameIndication"
	// certHTTPSProvisioningEnabling Front Door 自定义 HTTPS 配置变更触发态
	certHTTPSProvisioningEnabling = "Enabling"
	// certDefaultKVSuffix KV 数据面缺省域名后缀（全球云；主权云经 WithKeyVaultURI 覆盖）
	certDefaultKVSuffix = ".vault.azure.net"
)

// envKeyVaultName / envKeyVaultURI KV 目标库环境变量回退（平台账号模型未存储
// vault 定位，与 tenant/subscription 同口径：Option 注入优先，env 回退）
const (
	envKeyVaultName = "AZURE_KEY_VAULT_NAME"
	envKeyVaultURI  = "AZURE_KEY_VAULT_URI"
)

// kvCertNamePattern KV 证书命名约束（1~127 字符 [a-zA-Z0-9-]；本层沿用三云
// 63 字符生成口径，校验为防御性显式失败不猜测）
var kvCertNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,127}$`)

// CertAdapter Azure 完整证书适配器：五方法按产品分发（cdn/alb）。
// 凭证复用既有云账号体系（*domain.CloudAccount），逐调用传入，不在适配层
// 新建凭证存储；REST 写客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	*CertDiscoveryAdapter // ListReferences/GetCert/限流/产品枚举/公共辅助复用（引用形态同口径）

	keyVaultURI  string // 显式注入 KV 数据面基址（优先于 name/env）
	keyVaultName string // 显式注入 KV 名称（缺省后缀组合基址）

	newKVCertWriter func(ctx context.Context, creds *domain.CloudAccount, vaultURI string) (azureKVCertWriter, error)
	newARMBinder    func(ctx context.Context, creds *domain.CloudAccount) (azureARMBinder, error)
}

// CertAdapterOption 完整适配器可选配置（部署面：KV 目标库定位）
type CertAdapterOption func(*CertAdapter)

// WithKeyVaultName 注入上传目标 Key Vault 名称（数据面基址按缺省后缀组合）
func WithKeyVaultName(name string) CertAdapterOption {
	return func(a *CertAdapter) { a.keyVaultName = strings.TrimSpace(name) }
}

// WithKeyVaultURI 注入上传目标 Key Vault 数据面基址（主权云后缀覆盖入口）
func WithKeyVaultURI(vaultURI string) CertAdapterOption {
	return func(a *CertAdapter) { a.keyVaultURI = strings.TrimSpace(vaultURI) }
}

// NewCertAdapter 创建 Azure 完整证书适配器（默认真实 REST 客户端工厂，复用既有
// 云账号凭证体系与 20 QPS 限流口径；vault 目标经 Option/env 注入，上传路径
// 缺失时显式报错——绑定/清理路径不依赖该配置）。
func NewCertAdapter(logger *elog.Component, opts ...CertAdapterOption) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	adapter := &CertAdapter{
		CertDiscoveryAdapter: NewCertDiscoveryAdapter(logger),
	}
	for _, opt := range opts {
		opt(adapter)
	}
	adapter.newKVCertWriter = func(ctx context.Context, creds *domain.CloudAccount, vaultURI string) (azureKVCertWriter, error) {
		return newKVCertWriterREST(adapter.CertDiscoveryAdapter, creds, vaultURI)
	}
	adapter.newARMBinder = func(ctx context.Context, creds *domain.CloudAccount) (azureARMBinder, error) {
		return newARMRESTBinder(adapter.CertDiscoveryAdapter, creds)
	}
	return adapter
}

// effectiveVaultURI 解析上传目标 KV 数据面基址：显式 URI > 显式名称组合 >
// env URI > env 名称组合；未配置返回空（上传路径显式报错）。
func (a *CertAdapter) effectiveVaultURI() string {
	if a.keyVaultURI != "" {
		return strings.TrimSuffix(a.keyVaultURI, "/")
	}
	if a.keyVaultName != "" {
		return "https://" + a.keyVaultName + certDefaultKVSuffix
	}
	if envURI := strings.TrimSpace(os.Getenv(envKeyVaultURI)); envURI != "" {
		return strings.TrimSuffix(envURI, "/")
	}
	if envName := strings.TrimSpace(os.Getenv(envKeyVaultName)); envName != "" {
		return "https://" + envName + certDefaultKVSuffix
	}
	return ""
}

// ==================== 两段式第一段：上传 ====================

// UploadCert 导入证书束至 Key Vault（KV 证书导入 API，PEM 形态含私钥），返回
// KV secret ID（含版本的云证书 ID，各产品绑定引用统一形态）。product 仅作
// 上下文与适配层产品校验（KV 为账号级证书库，无地域/产品语义——与 AWS 的
// CloudFront us-east-1 硬约束不同，上传无地域路由）。
//
// certPEM 为叶在前证书束（可含证书链）；keyPEM 为明文私钥（PEM 形态，仅内存
// 透传；导入束与 base64 副本调用完成后即刻归零，不落日志）。name 为 KV 证书名
// （deployer 层逐次唯一生成，C7——KV 删除按证书名删除全体版本，名称复用会
// 误删其他上传副本的证书）。
func (a *CertAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	if !certSupportedProducts[product] {
		return "", certProductNotSupported(product)
	}
	if creds == nil {
		return "", fmt.Errorf("azure %s cert upload: nil creds", product)
	}
	if name == "" || certPEM == "" || keyPEM == "" {
		return "", fmt.Errorf("azure %s cert upload: name/cert/key required", product)
	}
	if !kvCertNamePattern.MatchString(name) {
		return "", fmt.Errorf("azure %s cert upload: certificate name %q does not match key vault naming constraint [a-zA-Z0-9-]{1,127}", product, name)
	}
	if err := validatePrivateKeyPEM(keyPEM); err != nil {
		return "", fmt.Errorf("azure %s cert upload: %w", product, err)
	}
	vaultURI := a.effectiveVaultURI()
	if vaultURI == "" {
		return "", fmt.Errorf("azure %s cert upload: key vault target required (option WithKeyVaultName/WithKeyVaultURI or env %s)", product, envKeyVaultName)
	}
	// 导入束（PEM 形态，含私钥）：证书束（叶在前 fullchain 口径）在前、明文
	// 私钥在后；base64 单层编码后入 value 字段（含私钥材料，用后归零）。
	bundle := buildImportBundlePEM(certPEM, keyPEM)
	defer cloudx.Zeroize(bundle)
	params := azureKVCertImport{
		Value:       []byte(base64.StdEncoding.EncodeToString(bundle)),
		ContentType: certKVPemContentType,
	}
	defer cloudx.Zeroize(params.Value)
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	writer, err := a.newKVCertWriter(ctx, creds, vaultURI)
	if err != nil {
		return "", err
	}
	result, err := writer.importCertificate(ctx, name, params)
	if err != nil {
		return "", wrapCertCloudErr("keyvault", err)
	}
	certID := strings.TrimSpace(result.SecretID)
	if certID == "" {
		// sid 缺失回退 versionless 组合（发现引用兼容形态）
		certID = vaultURI + "/secrets/" + name
	}
	a.logger.Info("AzureKeyVault证书导入成功",
		elog.String("product", product),
		elog.String("certificate_secret_id", certID))
	return certID, nil
}

// buildImportBundlePEM 组装 KV 导入 PEM 束：证书束（叶在前 fullchain 口径）在
// 前、明文私钥在后（发现适配 GetCert 净化口径：首个 CERTIFICATE 块=叶证书）。
func buildImportBundlePEM(certPEM, keyPEM string) []byte {
	cert := strings.TrimSpace(certPEM)
	key := strings.TrimSpace(keyPEM)
	bundle := make([]byte, 0, len(cert)+len(key)+2)
	bundle = append(bundle, cert...)
	bundle = append(bundle, '\n')
	bundle = append(bundle, key...)
	bundle = append(bundle, '\n')
	return bundle
}

// validatePrivateKeyPEM 校验私钥为 PEM 私钥块形态（导入 API 含私钥要求；
// 显式失败不猜测）
func validatePrivateKeyPEM(keyPEM string) error {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return fmt.Errorf("key pem contains no PEM block")
	}
	if !strings.Contains(block.Type, "PRIVATE KEY") {
		return fmt.Errorf("key pem block type %q is not a private key", block.Type)
	}
	return nil
}

// ==================== 两段式第二段：绑定 ====================

// BindResource 将 KV 证书（secret ID 引用形态）绑定至产品资源（按产品分发，
// Hard Rule：绑定统一走 KV 引用形态，引用归一化在本适配层单点）：
//   - cdn：Front Door（classic）前端终结点自定义域名 HTTPS 配置；
//   - alb：Application Gateway 监听器既有 SSL 证书资源改指 KV secret。
func (a *CertAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	switch product {
	case CertProductCDN:
		return a.bindFrontDoor(ctx, creds, resourceID, cloudCertID)
	case CertProductALB:
		return a.bindAppGateway(ctx, creds, resourceID, cloudCertID)
	default:
		return certProductNotSupported(product)
	}
}

// bindAppGateway 将 KV secret 引用绑定至 Application Gateway 监听器：定位网关
// （订阅级清单按名反查完整 ARM ID）→ 监听器既有 SSL 证书资源 → 子资源 PUT 改指
// 目标 keyVaultSecretId（监听器经证书资源 ID 引用，绑定随之生效；App Gateway
// 经 KV 证书引用绑定而非直接上传 ID）。证书资源已引用目标 secret 时幂等跳过
// （回滚重放/重复绑定安全，避免网关配置传播成本）。
func (a *CertAdapter) bindAppGateway(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	const product = CertProductALB
	if creds == nil {
		return fmt.Errorf("azure %s cert bind: nil creds", product)
	}
	gatewayName, listenerName, err := splitCompositeResourceID(resourceID)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	secretID, err := normalizeKvCertID(cloudCertID)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	if _, err := parseKvSecretID(secretID); err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	binder, err := a.newARMBinder(ctx, creds)
	if err != nil {
		return err
	}
	items, err := binder.list(ctx, "Microsoft.Network/applicationGateways", armAPIVersion)
	if err != nil {
		return wrapCertCloudErr(product, err)
	}
	raw, gatewayID, err := findARMResourceByName(items, "application gateway", gatewayName)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	var gateway appGatewayBindResource
	if err := json.Unmarshal(raw, &gateway); err != nil {
		return fmt.Errorf("azure %s cert bind: 解析ApplicationGateway资源失败: %w", product, err)
	}
	certRefID := ""
	for _, listener := range gateway.Properties.HTTPListeners {
		if strings.EqualFold(listener.Name, listenerName) {
			if listener.Properties.SSLCertificate != nil {
				certRefID = listener.Properties.SSLCertificate.ID
			}
			break
		}
	}
	if certRefID == "" {
		return fmt.Errorf("azure %s cert bind: listener %q has no ssl certificate reference", product, listenerName)
	}
	certName, currentSecretID, inlineData := "", "", ""
	for _, cert := range gateway.Properties.SSLCertificates {
		if cert.ID != "" && strings.EqualFold(cert.ID, certRefID) {
			certName = cert.Name
			currentSecretID = strings.TrimSpace(cert.Properties.KeyVaultSecretID)
			inlineData = cert.Properties.Data
			break
		}
	}
	if certName == "" {
		return fmt.Errorf("azure %s cert bind: ssl certificate %q referenced by listener %q not found", product, certRefID, listenerName)
	}
	// 幂等：证书资源已引用目标 KV secret → no-op 成功
	if strings.TrimSuffix(currentSecretID, "/") == strings.TrimSuffix(secretID, "/") {
		a.logger.Info("AzureAppGateway证书绑定幂等跳过（KV引用已等值）",
			elog.String("gateway", gatewayName),
			elog.String("listener", listenerName),
			elog.String("secret_id", secretID))
		return nil
	}
	if inlineData != "" && currentSecretID == "" {
		// 内联上传证书资源（data 形态）与 KV 引用形态互斥（发现侧同口径盲区）：
		// 改绑将破坏内联证书材料，显式失败不猜测
		return fmt.Errorf("azure %s cert bind: ssl certificate %q is inline (data) form; key vault binding requires a key-vault-backed certificate resource", product, certName)
	}
	body, err := json.Marshal(sslCertificatePut{
		Name:       certName,
		Properties: sslCertificatePutProperties{KeyVaultSecretID: secretID},
	})
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	if err := binder.putResource(ctx, strings.TrimSuffix(gatewayID, "/")+"/sslCertificates/"+url.PathEscape(certName), body); err != nil {
		return wrapCertCloudErr(product, err)
	}
	a.logger.Info("AzureAppGateway证书绑定成功",
		elog.String("gateway", gatewayName),
		elog.String("listener", listenerName),
		elog.String("ssl_certificate", certName),
		elog.String("secret_id", secretID))
	return nil
}

// bindFrontDoor 将 KV secret 引用绑定至 Front Door（classic）前端终结点自定义
// 域名 HTTPS 配置：解析 secret ID（vault 主机/secret 名/版本）→ 订阅级清单解析
// vault ARM 资源 ID（azureKeyVaultCertificateSecret.vault.id 契约要求）→ 终结点
// PATCH（certificateSource=AzureKeyVault + SNI + provisioning state=Enabling
// 触发配置变更；已启用状态下变更 KV 证书引用同形态提交）。引用已等值时幂等跳过
// （secretId 直等或 vault/name/version 组合等值两形态）。
func (a *CertAdapter) bindFrontDoor(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	const product = CertProductCDN
	if creds == nil {
		return fmt.Errorf("azure %s cert bind: nil creds", product)
	}
	doorName, endpointName, err := splitCompositeResourceID(resourceID)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	secretID, err := normalizeKvCertID(cloudCertID)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	loc, err := parseKvSecretID(secretID)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	binder, err := a.newARMBinder(ctx, creds)
	if err != nil {
		return err
	}
	doors, err := binder.list(ctx, "Microsoft.Network/frontdoors", armAPIVersion)
	if err != nil {
		return wrapCertCloudErr(product, err)
	}
	raw, doorID, err := findARMResourceByName(doors, "front door", doorName)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	var door frontDoorBindResource
	if err := json.Unmarshal(raw, &door); err != nil {
		return fmt.Errorf("azure %s cert bind: 解析FrontDoor资源失败: %w", product, err)
	}
	endpointID := ""
	var current *struct {
		CertificateSource              string       `json:"certificateSource"`
		SecretID                       string       `json:"secretId"`
		AzureKeyVaultCertificateSecret *kvSecretRef `json:"azureKeyVaultCertificateSecret"`
	}
	for i := range door.Properties.FrontendEndpoints {
		fe := &door.Properties.FrontendEndpoints[i]
		if !strings.EqualFold(fe.Name, endpointName) {
			continue
		}
		endpointID = fe.ID
		if fe.Properties.CustomHTTPSConfiguration != nil {
			current = &struct {
				CertificateSource              string       `json:"certificateSource"`
				SecretID                       string       `json:"secretId"`
				AzureKeyVaultCertificateSecret *kvSecretRef `json:"azureKeyVaultCertificateSecret"`
			}{
				CertificateSource:              fe.Properties.CustomHTTPSConfiguration.CertificateSource,
				SecretID:                       fe.Properties.CustomHTTPSConfiguration.SecretID,
				AzureKeyVaultCertificateSecret: fe.Properties.CustomHTTPSConfiguration.AzureKeyVaultCertificateSecret,
			}
		}
		break
	}
	if current == nil && endpointID == "" {
		return fmt.Errorf("azure %s cert bind: frontend endpoint %q not found in front door %q", product, endpointName, doorName)
	}
	if endpointID == "" {
		endpointID = strings.TrimSuffix(doorID, "/") + "/frontendEndpoints/" + url.PathEscape(endpointName)
	}
	// 幂等：证书来源已为 KV 引用且等值（secretId 直等 / vault+name+version 组合
	// 等值）→ no-op 成功；先于 vault 解析判定，重复绑定不依赖 vault 清单读权限
	if current != nil && strings.EqualFold(current.CertificateSource, certKVCertSourceAzureKeyVault) {
		currentRef := strings.TrimSpace(current.SecretID)
		if currentRef == "" {
			currentRef = composeKvSecretID(current.AzureKeyVaultCertificateSecret)
		}
		if currentRef == secretID {
			a.logger.Info("AzureFrontDoor证书绑定幂等跳过（KV引用已等值）",
				elog.String("front_door", doorName),
				elog.String("endpoint", endpointName),
				elog.String("secret_id", secretID))
			return nil
		}
	}
	vaults, err := binder.list(ctx, "Microsoft.KeyVault/vaults", armAPIVersion)
	if err != nil {
		return wrapCertCloudErr(product, err)
	}
	vaultID, err := findVaultResourceID(vaults, loc.VaultName)
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w (front door custom https binding requires the key vault resource id)", product, err)
	}
	body, err := json.Marshal(frontendEndpointPatch{
		Properties: frontendEndpointPatchProperties{
			CustomHTTPSConfiguration: &kvCustomHTTPSConfig{
				CertificateSource: certKVCertSourceAzureKeyVault,
				ProtocolType:      certKVProtocolSNI,
				AzureKeyVaultCertificateSecret: &kvBindSecretRef{
					Vault:         kvVaultIDRef{ID: vaultID},
					SecretName:    loc.SecretName,
					SecretVersion: loc.SecretVersion,
				},
			},
			CustomHTTPSProvisioningState: certHTTPSProvisioningEnabling,
		},
	})
	if err != nil {
		return fmt.Errorf("azure %s cert bind: %w", product, err)
	}
	if err := binder.patchResource(ctx, endpointID, body); err != nil {
		return wrapCertCloudErr(product, err)
	}
	a.logger.Info("AzureFrontDoor证书绑定成功",
		elog.String("front_door", doorName),
		elog.String("endpoint", endpointName),
		elog.String("secret_id", secretID))
	return nil
}

// ==================== CleanupOrphan：孤儿证书清理 ====================

// CleanupOrphan 删除 KV 孤儿证书（幂等：已不存在/已软删除视为成功，清理队列
// 重放安全）。vault 定位取自 secret ID 自身（清理路径不依赖上传目标 vault 配置）。
//   - KV 删除进软删除态（保留期内可恢复，幂等重放安全）；purge 为不可逆破坏性
//     操作，安全侧不执行——软删除保留期即误删恢复窗口；
//   - 删除按证书名执行（KV 删除对象=证书全体版本）：孤儿证书 ID 逐次唯一（C7，
//     deployer 层上传名公式保证），按名删除不误伤其他上传副本。
func (a *CertAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("azure cert cleanup: nil creds")
	}
	secretID, err := normalizeKvCertID(cloudCertID)
	if err != nil {
		return fmt.Errorf("azure cert cleanup: %w", err)
	}
	loc, err := parseKvSecretID(secretID)
	if err != nil {
		return fmt.Errorf("azure cert cleanup: %w", err)
	}
	vaultURI := "https://" + loc.VaultHost
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	writer, err := a.newKVCertWriter(ctx, creds, vaultURI)
	if err != nil {
		return err
	}
	if err := writer.deleteCertificate(ctx, loc.SecretName); err != nil {
		if errors.Is(err, errAzureNotFound) {
			// 已被删除（含软删除态重复删除 404）→ 幂等成功（清理队列重放场景）
			a.logger.Info("Azure孤儿证书清理幂等成功（证书已不存在）",
				elog.String("certificate_name", loc.SecretName))
			return nil
		}
		return wrapCertCloudErr("keyvault", err)
	}
	a.logger.Info("Azure孤儿证书清理成功",
		elog.String("certificate_name", loc.SecretName),
		elog.String("secret_id", secretID))
	return nil
}

// ==================== 引用归一化与资源定位（Hard Rule 单点） ====================

// normalizeKvCertID KV 证书引用归一化（Hard Rule：Azure 证书引用统一 KV 引用
// 形态，归一化收敛在本适配层单点；绑定/清理入参经此归一，形态校验交
// parseKvSecretID 按需执行）。
func normalizeKvCertID(cloudCertID string) (string, error) {
	s := strings.TrimSpace(cloudCertID)
	if s == "" {
		return "", fmt.Errorf("cloud cert id is empty")
	}
	return s, nil
}

// kvSecretLocation KV secret ID 解析结果
type kvSecretLocation struct {
	VaultHost     string // vault 数据面主机（{name}.vault.{suffix}）
	VaultName     string // vault 名称（主机首段）
	SecretName    string // secret/证书名
	SecretVersion string // 版本（versionless 时为空）
}

// parseKvSecretID 解析 KV secret ID 形态（https://{vault}/secrets/{name}[/{version}]）：
// 非 https 主机/路径不符 → 显式失败不猜测（发现引用与上传产物均为该形态）。
func parseKvSecretID(secretID string) (kvSecretLocation, error) {
	u, err := url.Parse(secretID)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return kvSecretLocation{}, fmt.Errorf("cloud cert id %q is not a key vault secret id form", secretID)
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || !strings.EqualFold(segs[0], "secrets") || segs[1] == "" {
		return kvSecretLocation{}, fmt.Errorf("cloud cert id %q is not a key vault secret id form", secretID)
	}
	loc := kvSecretLocation{
		VaultHost:  strings.ToLower(u.Host),
		VaultName:  vaultNameFromHost(u.Host),
		SecretName: segs[1],
	}
	if len(segs) >= 3 && segs[2] != "" {
		loc.SecretVersion = segs[2]
	}
	return loc, nil
}

// vaultNameFromHost vault 主机名解析 vault 名称（{name}.vault.{suffix} 首段；
// 全球云/中国云/主权云后缀差异不影响首段提取）。
func vaultNameFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if idx := strings.Index(host, ".vault."); idx > 0 {
		return host[:idx]
	}
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return host
}

// splitCompositeResourceID 拆分发现适配的复合资源 ID（"{资源名}/{子资源名}"，
// CDN="{FrontDoor}/{FrontendEndpoint}"、ALB="{Gateway}/{Listener}"）；缺子段
// 显式失败不猜测。
func splitCompositeResourceID(resourceID string) (owner, sub string, err error) {
	parts := strings.SplitN(strings.TrimSpace(resourceID), "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("resource id %q is not a composite form {resource}/{name}", resourceID)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

// findARMResourceByName 订阅级资源清单按名定位资源（返回完整原始 JSON 与 ARM ID；
// 资源名订阅内唯一， EqualFold 匹配 ARM 大小写不敏感语义）。
func findARMResourceByName(items []json.RawMessage, kind, name string) (json.RawMessage, string, error) {
	for _, item := range items {
		var probe struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			continue
		}
		if strings.EqualFold(probe.Name, name) {
			return item, probe.ID, nil
		}
	}
	return nil, "", fmt.Errorf("%s %q not found in subscription", kind, name)
}

// findVaultResourceID 订阅级清单解析 vault ARM 资源 ID（Front Door 绑定契约要求）
func findVaultResourceID(items []json.RawMessage, vaultName string) (string, error) {
	_, id, err := findARMResourceByName(items, "key vault", vaultName)
	return id, err
}

// ==================== 绑定面最小解析/写入结构 ====================

// frontDoorBindResource Front Door（classic）绑定面最小解析结构（完整 ARM ID +
// 终结点自定义 HTTPS 配置；与发现适配 frontDoorResource 分型，本结构含 id 字段）
type frontDoorBindResource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Properties struct {
		FrontendEndpoints []struct {
			ID         string `json:"id"`
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

// appGatewayBindResource Application Gateway 绑定面最小解析结构（完整 ARM ID +
// SSL 证书资源 + 监听器引用；与发现适配 appGatewayResource 分型，本结构含 id
// 与内联 data 字段——内联形态显式拒绝改绑）
type appGatewayBindResource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Properties struct {
		SSLCertificates []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Properties struct {
				KeyVaultSecretID string `json:"keyVaultSecretId"`
				Data             string `json:"data"`
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

// sslCertificatePut Application Gateway SSL 证书子资源 PUT 体（keyVaultSecretId
// 引用形态，data 内联形态不入写路径）
type sslCertificatePut struct {
	Name       string                      `json:"name"`
	Properties sslCertificatePutProperties `json:"properties"`
}

// sslCertificatePutProperties SSL 证书资源属性（KV 引用形态）
type sslCertificatePutProperties struct {
	KeyVaultSecretID string `json:"keyVaultSecretId"`
}

// frontendEndpointPatch Front Door 前端终结点 PATCH 体（自定义域名 HTTPS 配置
// 变更 + provisioning state=Enabling 触发）
type frontendEndpointPatch struct {
	Properties frontendEndpointPatchProperties `json:"properties"`
}

// frontendEndpointPatchProperties 终结点 PATCH 属性
type frontendEndpointPatchProperties struct {
	CustomHTTPSConfiguration     *kvCustomHTTPSConfig `json:"customHttpsConfiguration,omitempty"`
	CustomHTTPSProvisioningState string               `json:"customHttpsProvisioningState"`
}

// kvCustomHTTPSConfig KV 证书来源的自定义 HTTPS 配置
type kvCustomHTTPSConfig struct {
	CertificateSource              string           `json:"certificateSource"`
	ProtocolType                   string           `json:"protocolType"`
	AzureKeyVaultCertificateSecret *kvBindSecretRef `json:"azureKeyVaultCertificateSecret"`
}

// kvBindSecretRef KV 证书引用组合字段（vault ARM 资源 ID + secret 名/版本）
type kvBindSecretRef struct {
	Vault         kvVaultIDRef `json:"vault"`
	SecretName    string       `json:"secretName"`
	SecretVersion string       `json:"secretVersion"`
}

// kvVaultIDRef vault ARM 资源 ID 参照
type kvVaultIDRef struct {
	ID string `json:"id"`
}

// ==================== 真实 REST 写客户端（net/http 直调，无 Azure SDK 依赖） ====================

// kvCertImportPayload KV 导入请求体（value=base64 证书对象；pwd 仅 PFX 口令场景）
type kvCertImportPayload struct {
	Value  string                 `json:"value"`
	Pwd    string                 `json:"pwd,omitempty"`
	Policy kvCertImportPolicySpec `json:"policy"`
}

// kvCertImportPolicySpec 导入策略（secret 形态声明）
type kvCertImportPolicySpec struct {
	SecretProperties kvCertSecretProperties `json:"secret_properties"`
}

// kvCertSecretProperties secret 形态属性
type kvCertSecretProperties struct {
	ContentType string `json:"contentType"`
}

// importCertificate KV 证书导入（PUT /certificates/{name}；value=base64 证书对象
// 含私钥，请求体构建副本用后归零）。404 不出现于导入路径；429 经 readAzureResponse
// 归一 errAzureThrottled。
func (c *kvRESTClient) importCertificate(ctx context.Context, certName string, params azureKVCertImport) (azureKVCertImportResult, error) {
	endpoint := c.vaultURI + "/certificates/" + url.PathEscape(certName) + "?api-version=" + kvAPIVersion
	body, err := json.Marshal(kvCertImportPayload{
		Value: string(params.Value),
		Pwd:   string(params.Password),
		Policy: kvCertImportPolicySpec{
			SecretProperties: kvCertSecretProperties{ContentType: params.ContentType},
		},
	})
	if err != nil {
		return azureKVCertImportResult{}, fmt.Errorf("azure keyvault import: %w", err)
	}
	defer cloudx.Zeroize(body) // 请求体含私钥材料（base64 证书对象），用后归零
	respBody, err := azureSend(ctx, c.httpClient, c.token, kvScopeValue, endpoint, http.MethodPut, body)
	if err != nil {
		return azureKVCertImportResult{}, err
	}
	var parsed struct {
		KID string `json:"kid"`
		SID string `json:"sid"`
		CER string `json:"cer"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return azureKVCertImportResult{}, fmt.Errorf("解析Azure KeyVault导入响应失败: %w", err)
	}
	return azureKVCertImportResult{KeyID: parsed.KID, SecretID: parsed.SID, CertificateBase64: parsed.CER}, nil
}

// deleteCertificate KV 证书删除（DELETE /certificates/{name}；404 → 不存在哨兵，
// 幂等语义由适配层归一成功）。
func (c *kvRESTClient) deleteCertificate(ctx context.Context, certName string) error {
	endpoint := c.vaultURI + "/certificates/" + url.PathEscape(certName) + "?api-version=" + kvAPIVersion
	_, err := azureSend(ctx, c.httpClient, c.token, kvScopeValue, endpoint, http.MethodDelete, nil)
	return err
}

// newKVCertWriterREST 构建 KV 证书写面客户端（数据面 REST，Bearer kvScope）
func newKVCertWriterREST(a *CertDiscoveryAdapter, creds *domain.CloudAccount, vaultURI string) (azureKVCertWriter, error) {
	if strings.TrimSpace(vaultURI) == "" {
		return nil, fmt.Errorf("azure cert deploy: key vault uri required")
	}
	token, err := newRESTTokenProvider(a, creds)
	if err != nil {
		return nil, err
	}
	return &kvRESTClient{
		token:      token,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		vaultURI:   strings.TrimSuffix(strings.TrimSpace(vaultURI), "/"),
	}, nil
}

// armRESTBinder ARM 管理面绑定客户端（复用订阅级 lister；PATCH/PUT 写路径）
type armRESTBinder struct {
	*armRESTLister // list/令牌/端点/HTTP 客户端复用
}

// patchResource PATCH 管理面资源（相对路径经 mgmtEndpoint 归一 + api-version 附加）
func (b *armRESTBinder) patchResource(ctx context.Context, resourceURL string, body []byte) error {
	return b.send(ctx, http.MethodPatch, resourceURL, body)
}

// putResource PUT 管理面子资源（SSL 证书等 child resource create-or-update）
func (b *armRESTBinder) putResource(ctx context.Context, resourceURL string, body []byte) error {
	return b.send(ctx, http.MethodPut, resourceURL, body)
}

// send 归一 URL 附加 api-version 后授权发送（错误经 readAzureResponse 归一：
// 404 → 不存在哨兵、429 → 限流哨兵）
func (b *armRESTBinder) send(ctx context.Context, method, resourceURL string, body []byte) error {
	endpoint := appendAzureAPIVersion(relativeAzureURL(b.mgmtEndpoint, resourceURL), armAPIVersion)
	_, err := azureSend(ctx, b.httpClient, b.token, armScopeValue, endpoint, method, body)
	return err
}

// newARMRESTBinder 构建 ARM 管理面绑定客户端（订阅缺失显式报错，与发现口径
// 一致；list 路径复用 armRESTLister 形态）
func newARMRESTBinder(a *CertDiscoveryAdapter, creds *domain.CloudAccount) (azureARMBinder, error) {
	if a.subscriptionID == "" {
		return nil, fmt.Errorf("azure cert discovery: subscription id required (option WithSubscriptionID or env %s)", envSubscriptionID)
	}
	token, err := newRESTTokenProvider(a, creds)
	if err != nil {
		return nil, err
	}
	return &armRESTBinder{armRESTLister: &armRESTLister{
		token:          token,
		mgmtEndpoint:   a.mgmtEndpoint,
		subscriptionID: a.subscriptionID,
		pageSize:       a.certPageSize(),
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}}, nil
}

// appendAzureAPIVersion 为管理面资源 URL 附加 api-version 查询参数（已有查询时
// 以 & 连接）
func appendAzureAPIVersion(resourceURL, apiVersion string) string {
	sep := "?"
	if strings.Contains(resourceURL, "?") {
		sep = "&"
	}
	return resourceURL + sep + "api-version=" + apiVersion
}

// azureSend 授权发送带 JSON 体的云 API 请求（PUT/PATCH/POST/DELETE 写路径共用；
// GET 既有 azureGET 保留不动）。body 为 nil 时不携带请求体与 Content-Type。
func azureSend(ctx context.Context, httpClient *http.Client, token azureTokenProvider, scope, target, method string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("构建Azure %s请求失败: %w", method, err)
	}
	accessToken, err := token.token(ctx, scope)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求Azure %s失败: %w", method, err)
	}
	return readAzureResponse(response)
}
