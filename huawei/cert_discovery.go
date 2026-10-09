package huawei

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	cloudxhuawei "github.com/Havens-blog/e-cloudx-sdk/common/huawei"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	cdnv2 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	cdnregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/region"
	elbv3 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3"
	elbmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	elbregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/region"
	scmv3 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/scm/v3"
	scmmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/scm/v3/model"
	scmregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/scm/v3/region"
	wafv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1"
	wafmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/model"
	wafregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/region"
)

// 华为云只读证书发现适配（任务 3.3，tech-design Interface 2: CloudDeployer 的
// discovery-only 实现）：仅实现 ListReferences / GetCert 两个只读方法，
// UploadCert / BindResource / CleanupOrphan 一律返回哨兵 ErrDiscoveryOnly
// （ERR_DISCOVERY_ONLY 语义），不产生任何云侧写操作（代码评审口径的只读硬约束）。
// 三云引用可进台账与覆盖率分母，但进入变更清单时为不可执行项（5.2 处理）。
//
// 产品映射（枚举对齐 schema.sql cert_references.product）：
//   - cdn：CDN 加速域名 HTTPS 证书（ShowCertificatesHttpsInfo，全局服务）；
//     华为云 CDN 证书无独立证书 ID，以 cert_name 标识（certificate_type=2 为 SCM 托管）
//   - waf：WAF 云模式防护域名（ListHost → 逐域名 ShowHost 取 certificateid，即 SCM 证书 ID）
//   - alb：ELB v3 L7 监听（HTTPS / TERMINATED_HTTPS）的服务器证书与 SNI 扩展证书
//   - nlb：ELB v3 L4 监听（TLS）的服务器证书与 SNI 扩展证书；
//     华为云 ELB 无独立 alb/nlb 产品线，按监听协议归类映射到枚举（独享型 elb v3 API）

// 证书产品枚举（对齐 schema.sql cert_references.product 华为云引用面）
const (
	// CertProductCDN CDN 加速域名 HTTPS 证书
	CertProductCDN = "cdn"
	// CertProductWAF WAF 云模式防护域名证书
	CertProductWAF = "waf"
	// CertProductALB ELB L7（HTTPS/TERMINATED_HTTPS）监听证书
	CertProductALB = "alb"
	// CertProductNLB ELB L4（TLS）监听证书
	CertProductNLB = "nlb"
)

// certSupportedProducts 只读发现支持的产品集合（未实现产品显式报错而非静默）
var certSupportedProducts = map[string]bool{
	CertProductCDN: true,
	CertProductWAF: true,
	CertProductALB: true,
	CertProductNLB: true,
}

// 证书域哨兵错误
var (
	// ErrCloudRateLimited 云 API 限流/流控（CLOUD_API_RATELIMITED 语义）。
	// 定义位于 cloudx 公共层（tech-design Error Handling），本包再导出以对齐 3.1/3.2 调用形态。
	ErrCloudRateLimited = cloudx.ErrCloudRateLimited
	// ErrDiscoveryOnly 只读发现哨兵（ERR_DISCOVERY_ONLY 语义）：本适配无部署器，
	// 三个写方法一律返回，不产生任何云侧写操作。
	ErrDiscoveryOnly = cloudx.ErrDiscoveryOnly
	// ErrCertPEMUnsupported PEM 通道不支持哨兵（发现导入降级标记，非通用失败）：
	// SCM ShowCertificate 无 PEM 导出字段（指纹为 SHA-1 口径，无法支撑仅
	// CERTIFICATE 块净化序列），GetCert 一律返回本哨兵，上层识别为"该云证书
	// 暂不支持自动解析"降级标记（预览整组不可选、导入记因跳过）。
	ErrCertPEMUnsupported = cloudx.ErrCertPEMUnsupported
	// ErrCertProductNotSupported 未实现的证书产品/接口（显式报错而非静默）
	ErrCertProductNotSupported = errors.New("huawei cert product not supported")
)

// CloudCertRef 云侧证书引用（对齐 CertReference 落库字段需求：
// cloud/product/resourceId/referencedCloudCertId/accountKey，见 schema.sql cert_references）
type CloudCertRef struct {
	Cloud                 string // 固定 "huawei"
	Product               string // cdn|waf|alb|nlb
	ResourceID            string // 云资源标识：CDN/WAF=域名；ELB="{LoadBalancerId}/{ListenerId}"
	ReferencedCloudCertID string // 云侧证书标识：CDN=cert_name；WAF=SCM 证书 ID；ELB=ELB 证书 ID
	AccountKey            string // 云账号标识（取 CloudAccount.Name）
}

// CloudCertInfo GetCert 返回的云侧证书在库状态（只读）。
// 发现适配（discovery-only）口径：GetCert 一律返回 ErrCertPEMUnsupported 降级
// 标记、不填充任何字段（SCM ShowCertificate 无 PEM 导出字段，原生指纹为 SHA-1
// 口径，无法通过上层 ^[0-9a-f]{64}$ 对齐校验）；完整部署适配（cert.go
// CertAdapter，任务 1）口径：字段为真实在库状态，Fingerprint 优先导出材料解析
// 的 SHA-256 对齐口径（导出失败降级 SHA-1，上层按无法复核处理）。
type CloudCertInfo struct {
	Exists      bool      // 在库存在性（发现适配口径恒 false；部署适配填真实值）
	NotAfter    time.Time // 有效期截止（发现适配口径恒为零值；部署适配填真实值）
	Fingerprint string    // 指纹（发现适配口径恒为空；部署适配填 SHA-256/SHA-1 归一化值）
}

// cdnCertAPI CDN SDK 窄接口（只读：域名 HTTPS 证书信息）
type cdnCertAPI interface {
	ShowCertificatesHttpsInfo(request *cdnmodel.ShowCertificatesHttpsInfoRequest) (*cdnmodel.ShowCertificatesHttpsInfoResponse, error)
}

// wafCertAPI WAF SDK 窄接口（只读：防护域名列表 + 单域名详情）
type wafCertAPI interface {
	ListHost(request *wafmodel.ListHostRequest) (*wafmodel.ListHostResponse, error)
	ShowHost(request *wafmodel.ShowHostRequest) (*wafmodel.ShowHostResponse, error)
}

// elbCertAPI ELB SDK 窄接口（只读：监听器列表）
type elbCertAPI interface {
	ListListeners(request *elbmodel.ListListenersRequest) (*elbmodel.ListListenersResponse, error)
}

// scmCertAPI SCM（云证书管理服务）SDK 窄接口（只读：证书详情）
type scmCertAPI interface {
	ShowCertificate(request *scmmodel.ShowCertificateRequest) (*scmmodel.ShowCertificateResponse, error)
}

// CertDiscoveryAdapter 华为云只读证书发现适配器，按产品分发。
// 凭证复用既有云账号体系（*domain.CloudAccount，与既有华为云适配器同风格），
// 逐调用传入，不在适配层新建凭证存储；SDK 客户端工厂字段可被测试注入 fake。
type CertDiscoveryAdapter struct {
	logger       *elog.Component
	rateLimiter  *ratelimit.RateLimiter
	listPageSize int32 // ListReferences 分页大小（默认 certDiscoveryPageSize，测试可缩小以覆盖翻页分支）

	newCdnClient func(creds *domain.CloudAccount) (cdnCertAPI, error)
	newWafClient func(creds *domain.CloudAccount, region string) (wafCertAPI, error)
	newElbClient func(creds *domain.CloudAccount, region string) (elbCertAPI, error)
	newScmClient func(creds *domain.CloudAccount) (scmCertAPI, error)
}

// certDiscoveryPageSize ListReferences 默认分页大小
const certDiscoveryPageSize = int32(50)

// NewCertDiscoveryAdapter 创建华为云只读证书发现适配器（默认真实 SDK 客户端工厂，
// 与既有华为云适配器 20 QPS 限流口径一致）
func NewCertDiscoveryAdapter(logger *elog.Component) *CertDiscoveryAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertDiscoveryAdapter{
		logger:       logger,
		rateLimiter:  ratelimit.NewRateLimiter(20),
		listPageSize: certDiscoveryPageSize,
		newCdnClient: func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return newCertDiscoveryCDNClient(creds)
		},
		newWafClient: func(creds *domain.CloudAccount, region string) (wafCertAPI, error) {
			return newCertDiscoveryWAFClient(creds, region)
		},
		newElbClient: func(creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return newCertDiscoveryELBClient(creds, region)
		},
		newScmClient: func(creds *domain.CloudAccount) (scmCertAPI, error) {
			return newCertDiscoverySCMClient(creds)
		},
	}
}

// ==================== 只读硬约束：三个写方法一律返回哨兵 ====================

// UploadCert 两段式第一段（discovery-only 云未实现）：华为云首期无部署器，一律返回
// ErrDiscoveryOnly，不产生任何云侧写操作（PRD Out of Scope：三云部署器二期）
func (a *CertDiscoveryAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	_ = ctx
	_ = creds
	_ = name
	_ = certPEM
	_ = keyPEM
	return "", fmt.Errorf("%w: huawei %s cert upload not implemented", ErrDiscoveryOnly, product)
}

// BindResource 两段式第二段（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = resourceID
	_ = cloudCertID
	return fmt.Errorf("%w: huawei %s cert bind not implemented", ErrDiscoveryOnly, product)
}

// CleanupOrphan 孤儿清理（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = cloudCertID
	return fmt.Errorf("%w: huawei cert cleanup not implemented", ErrDiscoveryOnly)
}

// ==================== 只读发现 ====================

// ListReferences 只读发现：列出产品下全部证书引用（按产品分发）
func (a *CertDiscoveryAdapter) ListReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	switch product {
	case CertProductCDN:
		return a.listCDNReferences(ctx, creds)
	case CertProductWAF:
		return a.listWAFReferences(ctx, creds)
	case CertProductALB, CertProductNLB:
		return a.listELBReferences(ctx, creds, product)
	default:
		return nil, certProductNotSupported(product)
	}
}

// listCDNReferences 遍历 CDN 域名 HTTPS 证书配置（ShowCertificatesHttpsInfo 分页）。
// 华为云 CDN 证书无独立证书 ID，以 cert_name 作为 referencedCloudCertId。
func (a *CertDiscoveryAdapter) listCDNReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("huawei cdn cert list: nil creds")
	}
	client, err := a.newCdnClient(creds)
	if err != nil {
		return nil, err
	}
	accountKey := creds.Name
	pageSize := a.certPageSize()
	var refs []CloudCertRef
	page := int32(1)
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		response, err := client.ShowCertificatesHttpsInfo(&cdnmodel.ShowCertificatesHttpsInfoRequest{
			PageNumber: &page,
			PageSize:   &pageSize,
		})
		if err != nil {
			return nil, wrapCertCloudErr(CertProductCDN, err)
		}
		if response == nil || response.Https == nil || len(*response.Https) == 0 {
			break
		}
		for _, detail := range *response.Https {
			// HTTPS 未启用（HttpsStatus=0）的存量配置不构成在用证书引用
			if detail.HttpsStatus == nil || *detail.HttpsStatus == 0 {
				continue
			}
			domain := derefString(detail.DomainName)
			certName := derefString(detail.CertName)
			if domain == "" || certName == "" {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "huawei",
				Product:               CertProductCDN,
				ResourceID:            domain,
				ReferencedCloudCertID: certName,
				AccountKey:            accountKey,
			})
		}
		if int32(len(*response.Https)) < pageSize {
			break
		}
		page++
	}
	a.logger.Info("获取华为云CDN证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// listWAFReferences 按地域遍历 WAF 云模式防护域名：ListHost 分页取域名 id，
// 逐域名 ShowHost 读取 certificateid（SCM 证书 ID；列表接口不返回证书字段，N+1 只读展开）
func (a *CertDiscoveryAdapter) listWAFReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("huawei waf cert list: nil creds")
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range certCredsRegions(creds) {
		client, err := a.newWafClient(creds, region)
		if err != nil {
			return nil, err
		}
		pageSize := a.certPageSize()
		page := int32(1)
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			response, err := client.ListHost(&wafmodel.ListHostRequest{
				Page:     &page,
				Pagesize: &pageSize,
			})
			if err != nil {
				return nil, wrapCertCloudErr(CertProductWAF, err)
			}
			if response == nil || response.Items == nil || len(*response.Items) == 0 {
				break
			}
			for _, host := range *response.Items {
				hostID := derefString(host.Id)
				if hostID == "" {
					continue
				}
				ref, ok := a.wafHostReference(ctx, client, hostID, accountKey)
				if !ok {
					continue
				}
				refs = append(refs, ref)
			}
			if int32(len(*response.Items)) < pageSize {
				break
			}
			page++
		}
	}
	a.logger.Info("获取华为云WAF证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// wafHostReference 读取单个防护域名的证书引用（未配置证书 / 详情失败跳过，不中断整体扫描）
func (a *CertDiscoveryAdapter) wafHostReference(ctx context.Context, client wafCertAPI, hostID, accountKey string) (CloudCertRef, bool) {
	if err := a.waitRateLimit(ctx); err != nil {
		a.logger.Warn("等待限流令牌失败，跳过WAF域名", elog.String("host_id", hostID), elog.FieldErr(err))
		return CloudCertRef{}, false
	}
	response, err := client.ShowHost(&wafmodel.ShowHostRequest{InstanceId: hostID})
	if err != nil {
		// 单域名详情失败跳过继续，避免整体扫描失败
		a.logger.Warn("获取WAF防护域名详情失败，跳过",
			elog.String("host_id", hostID),
			elog.FieldErr(err))
		return CloudCertRef{}, false
	}
	if response == nil {
		return CloudCertRef{}, false
	}
	certID := derefString(response.Certificateid)
	hostname := derefString(response.Hostname)
	if certID == "" || hostname == "" {
		// 未配置服务器证书的防护域名不构成证书引用
		return CloudCertRef{}, false
	}
	return CloudCertRef{
		Cloud:                 "huawei",
		Product:               CertProductWAF,
		ResourceID:            hostname,
		ReferencedCloudCertID: certID,
		AccountKey:            accountKey,
	}, true
}

// listELBReferences 按地域遍历 ELB v3 监听器证书（独享型 ELB）：
// 主证书（default_tls_container_ref）与 SNI 扩展证书（sni_container_refs）均为引用；
// 监听协议 HTTPS/TERMINATED_HTTPS → alb（L7），TLS → nlb（L4），按调用方 product 过滤
func (a *CertDiscoveryAdapter) listELBReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("huawei elb cert list: nil creds")
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range certCredsRegions(creds) {
		client, err := a.newElbClient(creds, region)
		if err != nil {
			return nil, err
		}
		limit := a.certPageSize()
		var marker *string
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			response, err := client.ListListeners(&elbmodel.ListListenersRequest{
				Limit:  &limit,
				Marker: marker,
			})
			if err != nil {
				return nil, wrapCertCloudErr(product, err)
			}
			if response == nil || response.Listeners == nil || len(*response.Listeners) == 0 {
				break
			}
			for _, listener := range *response.Listeners {
				refs = append(refs, elbListenerRefs(listener, product, accountKey)...)
			}
			next := nextListMarker(response.PageInfo, len(*response.Listeners), int(limit))
			if next == nil {
				break
			}
			marker = next
		}
	}
	a.logger.Info("获取华为云ELB证书引用成功",
		elog.String("account", accountKey),
		elog.String("product", product),
		elog.Int("count", len(refs)))
	return refs, nil
}

// elbListenerRefs 展开单个监听器的证书引用（按协议归类产品，无证书监听器不产出引用）
func elbListenerRefs(listener elbmodel.Listener, product, accountKey string) []CloudCertRef {
	listenerProduct := elbListenerProduct(listener.Protocol)
	if listenerProduct == "" || listenerProduct != product {
		return nil
	}
	resourceID := elbListenerResourceID(listener)
	if resourceID == "" {
		return nil
	}
	var refs []CloudCertRef
	if listener.DefaultTlsContainerRef != "" {
		refs = append(refs, CloudCertRef{
			Cloud:                 "huawei",
			Product:               product,
			ResourceID:            resourceID,
			ReferencedCloudCertID: listener.DefaultTlsContainerRef,
			AccountKey:            accountKey,
		})
	}
	for _, sniRef := range listener.SniContainerRefs {
		if sniRef == "" || sniRef == listener.DefaultTlsContainerRef {
			continue
		}
		refs = append(refs, CloudCertRef{
			Cloud:                 "huawei",
			Product:               product,
			ResourceID:            resourceID,
			ReferencedCloudCertID: sniRef,
			AccountKey:            accountKey,
		})
	}
	return refs
}

// elbListenerProduct 按监听协议归类证书产品（无证书协议返回空）：
// HTTPS/TERMINATED_HTTPS 为 L7 终结 → alb；TLS 为 L4 → nlb
func elbListenerProduct(protocol string) string {
	switch protocol {
	case "HTTPS", "TERMINATED_HTTPS":
		return CertProductALB
	case "TLS":
		return CertProductNLB
	default:
		return ""
	}
}

// elbListenerResourceID 组合 ELB 监听器资源 ID "{LoadBalancerId}/{ListenerId}"
// （监听器 ID 仅在地域内唯一，需与所属负载均衡联合寻址）
func elbListenerResourceID(listener elbmodel.Listener) string {
	if listener.Id == "" {
		return ""
	}
	for _, lb := range listener.Loadbalancers {
		if lb.Id != nil && *lb.Id != "" {
			return *lb.Id + "/" + listener.Id
		}
	}
	return ""
}

// GetCert 华为云证书 PEM 通道不支持标记（只读）：SCM ShowCertificate 无 PEM
// 导出字段（指纹为 SHA-1 口径，无法支撑"仅 CERTIFICATE 块净化序列"材料通道），
// 一律返回 ErrCertPEMUnsupported 哨兵供上层识别为降级标记（预览整组不可选、
// 导入记因跳过），不发起任何云 API 调用、不消耗限流令牌。
// 历史行为说明：本方法原先调用 SCM 读取 SHA-1 指纹，但该口径永不匹配台账
// ^[0-9a-f]{64}$ 对齐校验（扫描侧落占位指纹），移除后各消费方行为不变；
// 等 SCM API 提供 PEM 导出后恢复详情读取（proposal Out of Scope）。
func (a *CertDiscoveryAdapter) GetCert(_ context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("huawei cert get: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return CloudCertInfo{}, fmt.Errorf("huawei cert get: empty cloud cert id")
	}
	return CloudCertInfo{}, fmt.Errorf("%w: huawei scm certificate has no pem export", ErrCertPEMUnsupported)
}

// ==================== 客户端工厂（真实 SDK，构建不发起网络请求） ====================

// newCertDiscoveryCDNClient 构建 CDN 只读客户端（CDN 为全局服务，与既有 CDNAdapter 同口径）
func newCertDiscoveryCDNClient(creds *domain.CloudAccount) (*cdnv2.CdnClient, error) {
	auth, err := global.NewCredentialsBuilder().
		WithAk(creds.AccessKeyID).
		WithSk(creds.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	client, err := cdnv2.CdnClientBuilder().
		WithRegion(cdnregion.CN_NORTH_1).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云CDN客户端失败: %w", err)
	}
	return cdnv2.NewCdnClient(client), nil
}

// newCertDiscoveryWAFClient 构建 WAF 只读客户端（区域服务，与既有 WAFAdapter 同口径）
func newCertDiscoveryWAFClient(creds *domain.CloudAccount, region string) (*wafv1.WafClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(creds.AccessKeyID).
		WithSk(creds.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	r, err := wafregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的WAF地域: %s", region)
	}
	client, err := wafv1.WafClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云WAF客户端失败: %w", err)
	}
	return wafv1.NewWafClient(client), nil
}

// newCertDiscoveryELBClient 构建 ELB v3 只读客户端（区域服务，与既有 LBAdapter 同口径）
func newCertDiscoveryELBClient(creds *domain.CloudAccount, region string) (*elbv3.ElbClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(creds.AccessKeyID).
		WithSk(creds.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	r, err := elbregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的ELB地域: %s", region)
	}
	client, err := elbv3.ElbClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云ELB客户端失败: %w", err)
	}
	return elbv3.NewElbClient(client), nil
}

// newCertDiscoverySCMClient 构建 SCM 只读客户端（SCM 为全局服务，cn-north-4 接入）
func newCertDiscoverySCMClient(creds *domain.CloudAccount) (*scmv3.ScmClient, error) {
	auth, err := global.NewCredentialsBuilder().
		WithAk(creds.AccessKeyID).
		WithSk(creds.AccessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}
	client, err := scmv3.ScmClientBuilder().
		WithRegion(scmregion.CN_NORTH_4).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云SCM客户端失败: %w", err)
	}
	return scmv3.NewScmClient(client), nil
}

// ==================== 公共辅助 ====================

// certPageSize 获取列举分页大小（零值回退默认）
func (a *CertDiscoveryAdapter) certPageSize() int32 {
	if a.listPageSize <= 0 {
		return certDiscoveryPageSize
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

// wrapCertCloudErr 云 API 错误统一包装：限流/流控映射哨兵 ErrCloudRateLimited，其余带产品上下文透传
func wrapCertCloudErr(product string, err error) error {
	if err == nil {
		return nil
	}
	if cloudxhuawei.IsThrottlingError(err) {
		return fmt.Errorf("%w: huawei %s api throttled: %v", ErrCloudRateLimited, product, err)
	}
	return fmt.Errorf("huawei %s api error: %w", product, err)
}

// isCertDiscoveryNotFound 判定证书不存在错误（HTTP 404 / 错误码与文案的非存在语义）
func isCertDiscoveryNotFound(err error) bool {
	if err == nil {
		return false
	}
	var sdkErr *sdkerr.ServiceResponseError
	if errors.As(err, &sdkErr) {
		if sdkErr.StatusCode == 404 {
			return true
		}
		return cloudxhuawei.IsNotFoundError(err) || isCertDiscoveryNotFoundText(sdkErr.ErrorMessage)
	}
	return isCertDiscoveryNotFoundText(err.Error())
}

// isCertDiscoveryNotFoundText 错误码/文案的非存在语义判定（大小写不敏感子串，含中文文案）
func isCertDiscoveryNotFoundText(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "notfound") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "not exist") ||
		strings.Contains(lower, "notexist") ||
		strings.Contains(s, "不存在")
}

// nextListMarker 计算 marker 分页下一游标（NextMarker 缺失或未满页时终止翻页）
func nextListMarker(pageInfo *elbmodel.PageInfo, count, limit int) *string {
	if count < limit {
		return nil
	}
	if pageInfo == nil || pageInfo.NextMarker == nil || *pageInfo.NextMarker == "" {
		return nil
	}
	return pageInfo.NextMarker
}

// derefString 解引用字符串指针（nil 返回空串）
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// normalizeCloudCertFingerprint 云侧冒号分隔指纹归一化为小写无分隔 hex（尽力对齐台账指纹形态）
func normalizeCloudCertFingerprint(fingerprint string) string {
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

// parseCloudCertTime 解析云侧时间字段（兼容 RFC3339 / 空格与 T 分隔 / 日期布局）
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

// certCredsRegion 账号默认地域（Regions[0]，缺省 cn-north-4，与既有华为云 Adapter 同口径）
func certCredsRegion(creds *domain.CloudAccount) string {
	if creds == nil || len(creds.Regions) == 0 {
		return "cn-north-4"
	}
	return creds.Regions[0]
}

// certCredsRegions 账号地域清单（WAF/ELB 按地域遍历发现；缺省回退默认地域）
func certCredsRegions(creds *domain.CloudAccount) []string {
	if creds == nil || len(creds.Regions) == 0 {
		return []string{certCredsRegion(creds)}
	}
	return creds.Regions
}
