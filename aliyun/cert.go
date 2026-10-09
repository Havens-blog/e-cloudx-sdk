package aliyun

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/alb"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cas"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dcdn"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/nlb"
	"github.com/gotomicro/ego/core/elog"
)

// 证书域公共类型与入口（tech-design Interface 2: CloudDeployer 的阿里云 SDK 适配层）。
// 本层仅做单次云 API 调用封装：两段式第一段（UploadCert）/第二段（BindResource）、
// 只读发现（ListReferences）、回滚目标校验（GetCert）、孤儿清理（CleanupOrphan）；
// 不做业务编排（上传+绑定编排、批次、验证窗口等属 cert 功能域 service/deployer 层）。

// 证书产品枚举（对齐 schema.sql cert_references.product 首期阿里云可部署产品集）
const (
	// CertProductCDN CDN 加速域名
	CertProductCDN = "cdn"
	// CertProductDCDN 全站加速域名
	CertProductDCDN = "dcdn"
	// CertProductWAF Web 应用防火墙（3.0）
	CertProductWAF = "waf"
	// CertProductALB 应用型负载均衡 ALB
	CertProductALB = "alb"
	// CertProductNLB 网络型负载均衡 NLB
	CertProductNLB = "nlb"
	// CertProductCAS CAS 证书库（SSL 证书服务「我的证书」）清单形态引用：
	// 仅 ListReferences 发现语义（cert-cas-library-scan），无资源绑定语义——
	// 不入 certSupportedProducts（UploadCert/BindResource 显式报错）
	CertProductCAS = "cas"
)

// certSupportedProducts 首期支持的证书可部署产品（clb 等未实现产品显式报错）
var certSupportedProducts = map[string]bool{
	CertProductCDN:  true,
	CertProductDCDN: true,
	CertProductWAF:  true,
	CertProductALB:  true,
	CertProductNLB:  true,
}

// 证书域哨兵错误
var (
	// ErrCloudRateLimited 云 API 限流/流控（CLOUD_API_RATELIMITED 语义）。
	// 定义位于 cloudx 公共层 cert_errors.go（任务 5.3 收敛：五云哨兵统一再导出，
	// 对齐 tencent/huawei/aws/azure 调用形态，见 3.summary Deviations）；
	// 限流重试与退避策略属变更执行编排层（tech-design Error Handling），本层仅映射哨兵。
	ErrCloudRateLimited = cloudx.ErrCloudRateLimited
	// ErrCertProductNotSupported 未实现的证书产品/接口（显式报错而非静默）
	ErrCertProductNotSupported = errors.New("aliyun cert product not supported")
)

// CloudCertRef 云侧证书引用（对齐 CertReference 落库字段需求：
// cloud/product/resourceId/referencedCloudCertId/accountKey，见 schema.sql cert_references）
type CloudCertRef struct {
	Cloud                 string   // 固定 "aliyun"
	Product               string   // cdn|dcdn|waf|alb|nlb|cas
	ResourceID            string   // 云资源标识：CDN/DCDN/WAF=域名，ALB/NLB="{LoadBalancerId}/{ListenerId}" 复合形态，cas=证书名称
	ReferencedCloudCertID string   // 云侧证书 ID（ALB/NLB 为 "{certId}-{region}" 形态，cas 为数字证书 ID 串）
	AccountKey            string   // 云账号标识（取 CloudAccount.Name）
	ServedDomains         []string // ALB 监听规则提取的 served hostname（Host 条件值；非 ALB 产品为空）
}

// CloudCertInfo GetCert 返回的云侧证书在库状态（回滚目标有效性校验依据）
type CloudCertInfo struct {
	Exists      bool      // 云证书库中该 cloudCertId 是否存在
	NotAfter    time.Time // 云侧证书有效期截止；Exists=false 时零值
	Fingerprint string    // 优先 PEM 解析的 SHA256 hex（对齐台账指纹 ^[0-9a-f]{64}$）；无 PEM 时回退 CAS 原始指纹
	// CertChainPEM 仅 CERTIFICATE 块的净化序列（叶在前 fullchain 口径）：
	// 块级过滤构造性保证（cloudx.SanitizeCertChainPEM），不含 PRIVATE KEY 等
	// 任何非证书内容；云侧未返回 PEM 时为空。发现导入材料通道（cert-cloud-discovery-import）。
	CertChainPEM string
}

// casCertAPI CAS（SSL 证书服务）SDK 窄接口：上传/详情/删除/证书库清单，
// *cas.Client 天然满足。ListUserCertificateOrder 仅用于只读发现
// （cert-cas-library-scan 的 cas 产品线），不引入任何写通路。
type casCertAPI interface {
	UploadUserCertificate(request *cas.UploadUserCertificateRequest) (*cas.UploadUserCertificateResponse, error)
	GetUserCertificateDetail(request *cas.GetUserCertificateDetailRequest) (*cas.GetUserCertificateDetailResponse, error)
	DeleteUserCertificate(request *cas.DeleteUserCertificateRequest) (*cas.DeleteUserCertificateResponse, error)
	ListUserCertificateOrder(request *cas.ListUserCertificateOrderRequest) (*cas.ListUserCertificateOrderResponse, error)
}

// cdnCertAPI CDN SDK 窄接口（cert_cdn.go 使用）
type cdnCertAPI interface {
	DescribeCdnHttpsDomainList(request *cdn.DescribeCdnHttpsDomainListRequest) (*cdn.DescribeCdnHttpsDomainListResponse, error)
	SetCdnDomainSSLCertificate(request *cdn.SetCdnDomainSSLCertificateRequest) (*cdn.SetCdnDomainSSLCertificateResponse, error)
}

// dcdnCertAPI DCDN SDK 窄接口（cert_dcdn.go 使用）
type dcdnCertAPI interface {
	DescribeDcdnHttpsDomainList(request *dcdn.DescribeDcdnHttpsDomainListRequest) (*dcdn.DescribeDcdnHttpsDomainListResponse, error)
	SetDcdnDomainSSLCertificate(request *dcdn.SetDcdnDomainSSLCertificateRequest) (*dcdn.SetDcdnDomainSSLCertificateResponse, error)
}

// albCertAPI ALB SDK 窄接口（cert_lb.go 使用）
type albCertAPI interface {
	ListListeners(request *alb.ListListenersRequest) (*alb.ListListenersResponse, error)
	ListListenerCertificates(request *alb.ListListenerCertificatesRequest) (*alb.ListListenerCertificatesResponse, error)
	UpdateListenerAttribute(request *alb.UpdateListenerAttributeRequest) (*alb.UpdateListenerAttributeResponse, error)
	ListRules(request *alb.ListRulesRequest) (*alb.ListRulesResponse, error)
	ListTagResources(request *alb.ListTagResourcesRequest) (*alb.ListTagResourcesResponse, error)
}

// nlbCertAPI NLB SDK 窄接口（cert_lb.go 使用）
type nlbCertAPI interface {
	ListListeners(request *nlb.ListListenersRequest) (*nlb.ListListenersResponse, error)
	UpdateListenerAttribute(request *nlb.UpdateListenerAttributeRequest) (*nlb.UpdateListenerAttributeResponse, error)
}

// CertAdapter 阿里云证书适配器：按产品分发五方法。
// 凭证复用既有云账号体系（*domain.CloudAccount，与 CreateRAMClientFromAccount 同风格），
// 逐调用传入，不在适配层新建凭证存储；SDK 客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	logger       *elog.Component
	rateLimiter  *RateLimiter
	listPageSize int // ListReferences 分页大小（默认 certDefaultPageSize，测试可缩小以覆盖翻页分支）

	newCasClient  func(creds *domain.CloudAccount) (casCertAPI, error)
	newCdnClient  func(creds *domain.CloudAccount) (cdnCertAPI, error)
	newDcdnClient func(creds *domain.CloudAccount) (dcdnCertAPI, error)
	newAlbClient  func(creds *domain.CloudAccount, region string) (albCertAPI, error)
	newNlbClient  func(creds *domain.CloudAccount, region string) (nlbCertAPI, error)
	newWafCaller  func(creds *domain.CloudAccount, region string) (wafCertCaller, error)
}

// certDefaultPageSize ListReferences 默认分页大小
const certDefaultPageSize = 50

// NewCertAdapter 创建阿里云证书适配器（默认真实 SDK 客户端工厂，与既有适配器 20 QPS 限流口径一致）
func NewCertAdapter(logger *elog.Component) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertAdapter{
		logger:       logger,
		rateLimiter:  NewRateLimiter(20),
		listPageSize: certDefaultPageSize,
		newCasClient: func(creds *domain.CloudAccount) (casCertAPI, error) {
			client, err := cas.NewClientWithAccessKey(certCASRegion(creds), creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建CAS客户端失败: %w", err)
			}
			return client, nil
		},
		newCdnClient: func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			client, err := cdn.NewClientWithAccessKey(credsRegion(creds), creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建CDN客户端失败: %w", err)
			}
			return client, nil
		},
		newDcdnClient: func(creds *domain.CloudAccount) (dcdnCertAPI, error) {
			client, err := dcdn.NewClientWithAccessKey(credsRegion(creds), creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建DCDN客户端失败: %w", err)
			}
			return client, nil
		},
		newAlbClient: func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			client, err := alb.NewClientWithAccessKey(region, creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建ALB客户端失败: %w", err)
			}
			return client, nil
		},
		newNlbClient: func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) {
			client, err := nlb.NewClientWithAccessKey(region, creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建NLB客户端失败: %w", err)
			}
			return client, nil
		},
		newWafCaller: func(creds *domain.CloudAccount, region string) (wafCertCaller, error) {
			client, err := newWafSDKClient(region, creds.AccessKeyID, creds.AccessKeySecret)
			if err != nil {
				return nil, fmt.Errorf("创建WAF客户端失败: %w", err)
			}
			return &wafRPCInvoker{client: client, region: region}, nil
		},
	}
}

// Stop 停止限流器（进程退出或适配器弃用时调用，避免令牌协程泄漏）
func (a *CertAdapter) Stop() {
	if a.rateLimiter != nil {
		a.rateLimiter.Stop()
	}
}

// waitRateLimit 等待限流令牌
func (a *CertAdapter) waitRateLimit(ctx context.Context) error {
	if a.rateLimiter == nil {
		return nil
	}
	return a.rateLimiter.Wait(ctx)
}

// certPageSize 获取列举分页大小（零值回退默认）
func (a *CertAdapter) certPageSize() int {
	if a.listPageSize <= 0 {
		return certDefaultPageSize
	}
	return a.listPageSize
}

// UploadCert 两段式第一段：上传证书到 CAS（SSL 证书服务）证书库，返回 cloudCertId。
// 五产品统一经 CAS 上传：CDN/DCDN/WAF 返回纯数字 ID；
// ALB/NLB 返回 "{certId}-{region}" 形态（两类监听引用证书的标准 ID 形态）。
// name 为 CAS 侧证书名称；certPEM/keyPEM 为 PEM 内容（仅透传云 API，不落日志）。
func (a *CertAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	if err := checkCertProduct(product); err != nil {
		return "", err
	}
	if creds == nil {
		return "", fmt.Errorf("aliyun cert upload: nil creds")
	}
	if name == "" || certPEM == "" || keyPEM == "" {
		return "", fmt.Errorf("aliyun cert upload: name/cert/key required")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}

	client, err := a.newCasClient(creds)
	if err != nil {
		return "", err
	}
	request := cas.CreateUploadUserCertificateRequest()
	request.Scheme = "https"
	request.Name = name
	request.Cert = certPEM
	request.Key = keyPEM

	response, err := client.UploadUserCertificate(request)
	if err != nil {
		return "", wrapCertCloudErr(product, err)
	}
	if response.CertId <= 0 {
		return "", fmt.Errorf("aliyun %s upload cert: empty cloud cert id", product)
	}
	cloudCertID := strconv.FormatInt(response.CertId, 10)
	if product == CertProductALB || product == CertProductNLB {
		cloudCertID = cloudCertID + "-" + certCASRegion(creds)
	}
	a.logger.Info("阿里云证书上传成功",
		elog.String("product", product),
		elog.String("cloud_cert_id", cloudCertID))
	return cloudCertID, nil
}

// BindResource 两段式第二段：将 cloudCertId 绑定到产品资源（按产品分发，详见各 cert_*.go）
func (a *CertAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	switch product {
	case CertProductCDN:
		return a.bindCDN(ctx, creds, resourceID, cloudCertID)
	case CertProductDCDN:
		return a.bindDCDN(ctx, creds, resourceID, cloudCertID)
	case CertProductWAF:
		return a.bindWAF(ctx, creds, resourceID, cloudCertID)
	case CertProductALB:
		return a.bindALB(ctx, creds, resourceID, cloudCertID)
	case CertProductNLB:
		return a.bindNLB(ctx, creds, resourceID, cloudCertID)
	default:
		return certProductNotSupported(product)
	}
}

// ListReferences 只读发现：列出产品下全部证书引用（按产品分发，详见各 cert_*.go）
func (a *CertAdapter) ListReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	switch product {
	case CertProductCDN:
		return a.listCDNReferences(ctx, creds)
	case CertProductDCDN:
		return a.listDCDNReferences(ctx, creds)
	case CertProductWAF:
		return a.listWAFReferences(ctx, creds)
	case CertProductALB:
		return a.listALBReferences(ctx, creds)
	case CertProductNLB:
		return a.listNLBReferences(ctx, creds)
	case CertProductCAS:
		return a.listCASReferences(ctx, creds)
	default:
		return nil, certProductNotSupported(product)
	}
}

// GetCert 查询云侧证书在库状态（回滚目标有效性校验依据，只读）。
// 产品无关：ALB/NLB 的 "{certId}-{region}" 形态 ID 自动归一化为 CAS 数字 ID。
func (a *CertAdapter) GetCert(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("aliyun cert get: nil creds")
	}
	certID, err := parseCASCertID(cloudCertID)
	if err != nil {
		return CloudCertInfo{}, err
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return CloudCertInfo{}, err
	}

	client, err := a.newCasClient(creds)
	if err != nil {
		return CloudCertInfo{}, err
	}
	request := cas.CreateGetUserCertificateDetailRequest()
	request.Scheme = "https"
	request.CertId = requests.NewInteger64(certID)
	// CertFilter=false：官方语义为 true 时 Cert/Key 等内容字段不返回（默认 false）。
	// PEM 材料通道依赖 Cert 字段；false 时响应可能携带 Key 等私钥字符串，仅经
	// SanitizeCertChainPEM 提取 CERTIFICATE 块后使用，结构体其余字段不落库不入日志。
	request.CertFilter = requests.NewBoolean(false)

	response, err := client.GetUserCertificateDetail(request)
	if err != nil {
		if isCertNotFoundError(err) {
			// 云侧已删除 → Exists=false（非错误，回滚目标校验按无效处理）
			return CloudCertInfo{Exists: false}, nil
		}
		return CloudCertInfo{}, wrapCertCloudErr("cas", err)
	}

	info := CloudCertInfo{Exists: true}
	// PEM 通道净化（构造性保证）：仅保留 CERTIFICATE 块的净化序列，
	// 原始字节副本净化后即刻归零（私钥等非证书内容不驻留）。
	rawCert := []byte(response.Cert)
	info.CertChainPEM = cloudx.SanitizeCertChainPEM(rawCert)
	cloudx.Zeroize(rawCert)
	if leaf, ok := parseCertLeafPEM(info.CertChainPEM); ok {
		sum := sha256.Sum256(leaf.Raw)
		info.Fingerprint = hex.EncodeToString(sum[:])
		info.NotAfter = leaf.NotAfter
	} else {
		// 云侧未返回 PEM 内容时回退 CAS 字段（指纹归一化为小写去冒号 hex）
		info.Fingerprint = normalizeCloudFingerprint(response.Fingerprint)
		if notAfter, ok := parseCloudCertTime(response.EndDate); ok {
			info.NotAfter = notAfter
		}
	}
	return info, nil
}

// CleanupOrphan 孤儿清理：删除 CAS 证书库中不再被引用的云证书（幂等：已不存在视为成功）
func (a *CertAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aliyun cert cleanup: nil creds")
	}
	certID, err := parseCASCertID(cloudCertID)
	if err != nil {
		return err
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}

	client, err := a.newCasClient(creds)
	if err != nil {
		return err
	}
	request := cas.CreateDeleteUserCertificateRequest()
	request.Scheme = "https"
	request.CertId = requests.NewInteger64(certID)

	if _, err := client.DeleteUserCertificate(request); err != nil {
		if isCertNotFoundError(err) {
			// 已被删除 → 幂等成功（清理队列重放场景）
			return nil
		}
		return wrapCertCloudErr("cas", err)
	}
	a.logger.Info("阿里云孤儿证书清理成功",
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// ==================== 公共辅助 ====================

// checkCertProduct 校验产品是否在首期支持集合
func checkCertProduct(product string) error {
	if certSupportedProducts[product] {
		return nil
	}
	return certProductNotSupported(product)
}

func certProductNotSupported(product string) error {
	return fmt.Errorf("%w: %q", ErrCertProductNotSupported, product)
}

// wrapCertCloudErr 云 API 错误统一包装：限流/流控映射哨兵 ErrCloudRateLimited，其余带产品上下文透传
func wrapCertCloudErr(product string, err error) error {
	if err == nil {
		return nil
	}
	if IsThrottlingError(err) {
		return fmt.Errorf("%w: aliyun %s api throttled: %v", ErrCloudRateLimited, product, err)
	}
	return fmt.Errorf("aliyun %s api error: %w", product, err)
}

// isCertNotFoundError 判定证书不存在错误（CAS 对缺失证书返回 404 或 NotExist 类错误码）
func isCertNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var serverErr *sdkerrors.ServerError
	if errors.As(err, &serverErr) {
		if serverErr.HttpStatus() == 404 {
			return true
		}
		return isNotFoundText(serverErr.ErrorCode()) || isNotFoundText(serverErr.Message())
	}
	return isNotFoundText(err.Error())
}

// isNotFoundText 错误码/文案的非存在语义判定（大小写不敏感子串）
func isNotFoundText(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "notfound") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "not exist") ||
		strings.Contains(lower, "notexist")
}

// parseCASCertID 解析云证书 ID 为 CAS 数字 ID：兼容 "8089870" 与 ALB/NLB 的 "8089870-cn-hangzhou" 形态
func parseCASCertID(cloudCertID string) (int64, error) {
	id := strings.TrimSpace(cloudCertID)
	if idx := strings.IndexByte(id, '-'); idx >= 0 {
		id = id[:idx]
	}
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("aliyun cert: invalid cloud cert id %q", cloudCertID)
	}
	return value, nil
}

// parseCertLeafPEM 解析 PEM 证书内容（GetCert 优先按 PEM 计算 SHA256 指纹）
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

// normalizeCloudFingerprint 云侧冒号分隔指纹归一化为小写无分隔 hex（尽力对齐台账指纹形态）
func normalizeCloudFingerprint(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	lower := strings.ToLower(fingerprint)
	if !strings.Contains(lower, ":") {
		return strings.TrimSpace(lower)
	}
	parts := strings.Split(lower, ":")
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strings.TrimSpace(part))
	}
	return b.String()
}

// parseCloudCertTime 解析云侧时间字段（兼容 RFC3339 / 日期 / 空格分隔多种布局）
func parseCloudCertTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// credsRegion 账号默认地域（Regions[0]，缺省 cn-hangzhou，与 Adapter 同口径）
func credsRegion(creds *domain.CloudAccount) string {
	if creds == nil || len(creds.Regions) == 0 {
		return "cn-hangzhou"
	}
	return creds.Regions[0]
}

// certCASRegion CAS（SSL 证书服务）接入地域：仅 cn-hangzhou / ap-southeast-1（国际站）
func certCASRegion(creds *domain.CloudAccount) string {
	return casRegion(credsRegion(creds))
}

// casRegion CAS 证书所在地域（CDN/DCDN cas 绑定的 CertRegion 取值），与 certCASRegion 同映射
func casRegion(region string) string {
	if region == "ap-southeast-1" {
		return "ap-southeast-1"
	}
	return "cn-hangzhou"
}

// credsRegions 账号地域清单（ALB/NLB 按地域遍历发现；缺省回退默认地域）
func credsRegions(creds *domain.CloudAccount) []string {
	if creds == nil || len(creds.Regions) == 0 {
		return []string{credsRegion(creds)}
	}
	return creds.Regions
}
