// cert.go 华为云完整证书适配器（cert-multicloud-deployers 任务 1）。
//
// 分层定位：与 3.1/3.2 aliyun/tencent CertAdapter 同构——五方法（UploadCert/
// BindResource/ListReferences/GetCert/CleanupOrphan）SDK 单次调用封装，供
// deployer.HuaweiDeployer 组装为 CloudDeployer 端口实例（任务 4 装配收敛：
// 与扫描适配共享实例，aliyun 模式）。
//
// 与既有 CertDiscoveryAdapter（任务 3.3，discovery-only）的关系：本类型内嵌
// 其指针复用只读发现（ListReferences）与限流/工厂设施，并覆盖三个写方法与
// GetCert 为真实云调用——只读哨兵约束（ErrDiscoveryOnly）仍由发现适配器类型
// 自身承载，本类型为部署能力形态，二者并存（任务 4 装配切换）。
//
// 云侧 ID 形态口径（与发现适配注释一致）：
//   - SCM（SSL 证书管理，全局服务 cn-north-4 接入）证书 ID 为上传/绑定/清理的
//     统一云证书 ID；SCM 证书名（name）非唯一键，仅可观测用途；
//   - CDN 绑定需 cert_name + scm_certificate_id 双字段（证书名经 SCM 详情解析）；
//   - WAF 云模式防护域名绑定需实例 ID（ListHost 按 hostname 过滤精确定位）；
//   - ELB v3 监听引用 default_tls_container_ref = 裸 SCM 证书 ID（SCM 全局唯一，
//     无 aliyun {certId}-{region} 式地域后缀形态）。
//
// 安全口径：私钥/凭证明文仅内存透传云 API，禁入日志与错误文案；GetCert 经
// SCM ExportCertificate 导出证书材料（响应含私钥字段，本层永不读取该字段、
// 原始字节副本净化后即刻归零）。
package huawei

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	elbmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	scmmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/scm/v3/model"
	wafmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/model"
)

// SCM/CDN/WAF/ELB 部署面 SDK 窄接口（真实客户端 *scmv3.ScmClient 等
// 天然满足；测试注入 fake——与发现适配只读窄接口命名区分）。
type (
	// scmDeployCertAPI SCM 部署面窄接口：上传/详情/导出/删除。
	scmDeployCertAPI interface {
		ShowCertificate(request *scmmodel.ShowCertificateRequest) (*scmmodel.ShowCertificateResponse, error)
		ImportCertificate(request *scmmodel.ImportCertificateRequest) (*scmmodel.ImportCertificateResponse, error)
		ExportCertificate(request *scmmodel.ExportCertificateRequest) (*scmmodel.ExportCertificateResponse, error)
		DeleteCertificate(request *scmmodel.DeleteCertificateRequest) (*scmmodel.DeleteCertificateResponse, error)
	}
	// cdnBindCertAPI CDN 绑定面窄接口：HTTPS 证书配置更新。
	cdnBindCertAPI interface {
		UpdateDomainMultiCertificates(request *cdnmodel.UpdateDomainMultiCertificatesRequest) (*cdnmodel.UpdateDomainMultiCertificatesResponse, error)
	}
	// wafBindCertAPI WAF 绑定面窄接口：防护域名定位 + 证书更新。
	wafBindCertAPI interface {
		ListHost(request *wafmodel.ListHostRequest) (*wafmodel.ListHostResponse, error)
		UpdateHost(request *wafmodel.UpdateHostRequest) (*wafmodel.UpdateHostResponse, error)
	}
	// elbBindCertAPI ELB 绑定面窄接口：监听器定位 + 证书更新。
	elbBindCertAPI interface {
		ShowListener(request *elbmodel.ShowListenerRequest) (*elbmodel.ShowListenerResponse, error)
		UpdateListener(request *elbmodel.UpdateListenerRequest) (*elbmodel.UpdateListenerResponse, error)
	}
)

// CertAdapter 华为云完整证书适配器：五方法按产品分发（cdn/waf/alb/nlb）。
// 凭证复用既有云账号体系（*domain.CloudAccount），逐调用传入，不在适配层
// 新建凭证存储；SDK 客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	*CertDiscoveryAdapter // ListReferences/限流/产品枚举/公共辅助复用（上传名与引用形态同口径）

	newScmDeployClient func(creds *domain.CloudAccount) (scmDeployCertAPI, error)
	newCdnBindClient   func(creds *domain.CloudAccount) (cdnBindCertAPI, error)
	newWafBindClient   func(creds *domain.CloudAccount, region string) (wafBindCertAPI, error)
	newElbBindClient   func(creds *domain.CloudAccount, region string) (elbBindCertAPI, error)
}

// NewCertAdapter 创建华为云完整证书适配器（默认真实 SDK 客户端工厂，复用既有
// 云账号凭证体系与 20 QPS 限流口径）。
func NewCertAdapter(logger *elog.Component) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertAdapter{
		CertDiscoveryAdapter: NewCertDiscoveryAdapter(logger),
		newScmDeployClient: func(creds *domain.CloudAccount) (scmDeployCertAPI, error) {
			return newCertDiscoverySCMClient(creds)
		},
		newCdnBindClient: func(creds *domain.CloudAccount) (cdnBindCertAPI, error) {
			return newCertDiscoveryCDNClient(creds)
		},
		newWafBindClient: func(creds *domain.CloudAccount, region string) (wafBindCertAPI, error) {
			return newCertDiscoveryWAFClient(creds, region)
		},
		newElbBindClient: func(creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			return newCertDiscoveryELBClient(creds, region)
		},
	}
}

// ==================== 两段式第一段：上传 ====================

// UploadCert 上传证书束至 SCM 证书库，返回 SCM 证书 ID（各产品绑定引用的
// 统一云证书 ID）。name 为云侧证书名（ecam-{指纹前8}-{unix秒}-{随机后缀}，
// 3~63 字符，SCM name 约束）；certPEM 可含证书链（API 从 certificate 字段取
// 证书本身，无需拆分）；keyPEM 为明文私钥（仅内存透传，不落日志）。
//
// DuplicateCheck=false（同意上传相同证书）：与腾讯 Repeatable=true 同口径——
// 每次更换生成独立云证书副本，孤儿清理按 CloudCertMapping 归属收敛，避免
// 复用可能仍被其他资源引用的存量证书；重试换名后内容重复亦不阻断（C7）。
func (a *CertAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	if !certSupportedProducts[product] {
		return "", certProductNotSupported(product)
	}
	if creds == nil {
		return "", fmt.Errorf("huawei %s cert upload: nil creds", product)
	}
	if name == "" || certPEM == "" || keyPEM == "" {
		return "", fmt.Errorf("huawei %s cert upload: name/cert/key required", product)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	client, err := a.newScmDeployClient(creds)
	if err != nil {
		return "", err
	}
	duplicateCheck := false
	response, err := client.ImportCertificate(&scmmodel.ImportCertificateRequest{
		Body: &scmmodel.ImportCertificateRequestBody{
			Name:           name,
			Certificate:    certPEM,
			PrivateKey:     keyPEM,
			DuplicateCheck: &duplicateCheck,
		},
	})
	if err != nil {
		return "", wrapCertCloudErr(product, err)
	}
	cloudCertID := ""
	if response != nil {
		cloudCertID = derefString(response.CertificateId)
	}
	if cloudCertID == "" {
		return "", fmt.Errorf("huawei %s cert upload: empty cloud cert id", product)
	}
	a.logger.Info("华为云SCM证书上传成功",
		elog.String("product", product),
		elog.String("cloud_cert_id", cloudCertID))
	return cloudCertID, nil
}

// ==================== 两段式第二段：绑定 ====================

// BindResource 将 SCM 证书绑定至产品资源（按产品分发）：
//   - cdn：加速域名级（UpdateDomainMultiCertificates，certificate_type=2 SCM 托管）；
//   - waf：云模式防护域名级（UpdateHost，实例 ID 经 hostname 过滤定位）；
//   - alb/nlb：ELB v3 监听级（ShowListener 定位地域与 SNI 保留集 → UpdateListener，
//     新证书置默认位，SNI 扩展证书保留）。
func (a *CertAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	switch product {
	case CertProductCDN:
		return a.bindCDN(ctx, creds, resourceID, cloudCertID)
	case CertProductWAF:
		return a.bindWAF(ctx, creds, resourceID, cloudCertID)
	case CertProductALB, CertProductNLB:
		return a.bindELB(ctx, creds, product, resourceID, cloudCertID)
	default:
		return certProductNotSupported(product)
	}
}

// bindCDN 将 SCM 证书绑定为 CDN 加速域名 HTTPS 证书（替换该域名证书并保持
// HTTPS 开启）。CDN 绑定必填 cert_name + scm_certificate_id 双字段——证书名
// 经 SCM 详情解析（cloudCertID 为 SCM 证书 ID 口径；发现侧 CDN 引用以证书名
// 标识，回滚恢复旧引用时若传入的是证书名则 SCM 详情不存在 → 显式失败不猜测）。
func (a *CertAdapter) bindCDN(ctx context.Context, creds *domain.CloudAccount, domainName, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("huawei cdn cert bind: nil creds")
	}
	if domainName == "" {
		return fmt.Errorf("huawei cdn cert bind: empty domain")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return fmt.Errorf("huawei cdn cert bind: empty cloud cert id")
	}
	certName, err := a.scmCertNameByID(ctx, creds, CertProductCDN, cloudCertID)
	if err != nil {
		return err
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newCdnBindClient(creds)
	if err != nil {
		return err
	}
	httpsSwitch := int32(1)
	certificateType := int32(2) // 0=自有证书；2=SCM 托管证书
	if _, err := client.UpdateDomainMultiCertificates(&cdnmodel.UpdateDomainMultiCertificatesRequest{
		Body: &cdnmodel.UpdateDomainMultiCertificatesRequestBody{
			Https: &cdnmodel.UpdateDomainMultiCertificatesRequestBodyContent{
				DomainName:       domainName,
				HttpsSwitch:      httpsSwitch,
				CertificateType:  &certificateType,
				ScmCertificateId: &cloudCertID,
				CertName:         &certName,
			},
		},
	}); err != nil {
		return wrapCertCloudErr(CertProductCDN, err)
	}
	a.logger.Info("华为云CDN证书绑定成功",
		elog.String("domain", domainName),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// bindWAF 将 SCM 证书绑定为 WAF 云模式防护域名证书（UpdateHost 证书字段更新）。
// UpdateHost 以防护域名实例 ID 寻址（发现侧引用 ResourceID=hostname）——经
// ListHost hostname 过滤 + 精确匹配跨地域定位；UpdateHost HTTPS 域名必填
// certificateid + certificatename 双字段。
func (a *CertAdapter) bindWAF(ctx context.Context, creds *domain.CloudAccount, hostname, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("huawei waf cert bind: nil creds")
	}
	if hostname == "" {
		return fmt.Errorf("huawei waf cert bind: empty hostname")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return fmt.Errorf("huawei waf cert bind: empty cloud cert id")
	}
	certName, err := a.scmCertNameByID(ctx, creds, CertProductWAF, cloudCertID)
	if err != nil {
		return err
	}
	hostID, err := a.findWAFHostInstance(ctx, creds, hostname)
	if err != nil {
		return err
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newWafBindClient(creds, hostID.region)
	if err != nil {
		return err
	}
	if _, err := client.UpdateHost(&wafmodel.UpdateHostRequest{
		InstanceId: hostID.instanceID,
		Body: &wafmodel.UpdateHostRequestBody{
			Certificateid:   &cloudCertID,
			Certificatename: &certName,
		},
	}); err != nil {
		return wrapCertCloudErr(CertProductWAF, err)
	}
	a.logger.Info("华为云WAF证书绑定成功",
		elog.String("hostname", hostname),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// wafHostInstance 防护域名实例定位（实例 ID + 所属地域）。
type wafHostInstance struct {
	instanceID string
	region     string
}

// findWAFHostInstance 跨地域定位防护域名实例（ListHost hostname 过滤分页 +
// Hostname 精确匹配兜底——过滤语义若为模糊匹配，精确比对防误绑）。
func (a *CertAdapter) findWAFHostInstance(ctx context.Context, creds *domain.CloudAccount, hostname string) (wafHostInstance, error) {
	pageSize := a.certPageSize()
	for _, region := range certCredsRegions(creds) {
		client, err := a.newWafBindClient(creds, region)
		if err != nil {
			return wafHostInstance{}, err
		}
		for page := int32(1); ; page++ {
			if err := a.waitRateLimit(ctx); err != nil {
				return wafHostInstance{}, err
			}
			response, err := client.ListHost(&wafmodel.ListHostRequest{
				Hostname: &hostname,
				Page:     &page,
				Pagesize: &pageSize,
			})
			if err != nil {
				return wafHostInstance{}, wrapCertCloudErr(CertProductWAF, err)
			}
			if response == nil || response.Items == nil || len(*response.Items) == 0 {
				break
			}
			for _, host := range *response.Items {
				if derefString(host.Hostname) == hostname && derefString(host.Id) != "" {
					return wafHostInstance{instanceID: derefString(host.Id), region: region}, nil
				}
			}
			if int32(len(*response.Items)) < pageSize {
				break
			}
		}
	}
	return wafHostInstance{}, fmt.Errorf("huawei waf cert bind: host %s not found in account regions", hostname)
}

// bindELB 将 SCM 证书绑定为 ELB v3 监听器服务器证书（alb=L7 终结协议，
// nlb=TLS）。监听器 ID 仅地域内唯一——ShowListener 跨地域定位（未命中 404
// 续查下一地域）；新证书置 default_tls_container_ref，原 SNI 扩展证书保留
// （新证书若已在 SNI 清单中则剔除，避免默认位与 SNI 重复引用）。
func (a *CertAdapter) bindELB(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("huawei %s cert bind: nil creds", product)
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return fmt.Errorf("huawei %s cert bind: empty cloud cert id", product)
	}
	_, listenerID := parseELBListenerResourceID(resourceID)
	if listenerID == "" {
		return fmt.Errorf("huawei %s cert bind: invalid listener resource id %q", product, resourceID)
	}
	listener, client, err := a.findELBListener(ctx, creds, listenerID)
	if err != nil {
		return err
	}
	switch listener.Protocol {
	case "HTTPS", "TERMINATED_HTTPS", "TLS":
	default:
		return fmt.Errorf("huawei %s cert bind: listener %s is %s (no server certificate)", product, listenerID, listener.Protocol)
	}
	hadSNI := len(listener.SniContainerRefs) > 0
	preservedSNI := make([]string, 0, len(listener.SniContainerRefs))
	for _, sniRef := range listener.SniContainerRefs {
		if sniRef == "" || sniRef == cloudCertID {
			continue
		}
		preservedSNI = append(preservedSNI, sniRef)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	option := &elbmodel.UpdateListenerOption{DefaultTlsContainerRef: &cloudCertID}
	if hadSNI {
		// 原 SNI 清单整单回写（剔除新证书）；原无 SNI 时传 nil 保持不修改
		option.SniContainerRefs = &preservedSNI
	}
	if _, err := client.UpdateListener(&elbmodel.UpdateListenerRequest{
		ListenerId: listenerID,
		Body:       &elbmodel.UpdateListenerRequestBody{Listener: option},
	}); err != nil {
		return wrapCertCloudErr(product, err)
	}
	a.logger.Info("华为云ELB证书绑定成功",
		elog.String("product", product),
		elog.String("listener", listenerID),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// findELBListener 跨地域定位监听器（返回监听器当前配置与所属地域客户端——
// 读当前证书配置以保留 SNI 扩展证书，写回同地域客户端）。
func (a *CertAdapter) findELBListener(ctx context.Context, creds *domain.CloudAccount, listenerID string) (*elbmodel.Listener, elbBindCertAPI, error) {
	for _, region := range certCredsRegions(creds) {
		client, err := a.newElbBindClient(creds, region)
		if err != nil {
			return nil, nil, err
		}
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, nil, err
		}
		response, err := client.ShowListener(&elbmodel.ShowListenerRequest{ListenerId: listenerID})
		if err != nil {
			if isCertDiscoveryNotFound(err) {
				continue // 未命中该地域，续查下一地域
			}
			return nil, nil, wrapCertCloudErr("elb", err)
		}
		if response != nil && response.Listener != nil {
			return response.Listener, client, nil
		}
	}
	return nil, nil, fmt.Errorf("huawei elb cert bind: listener %s not found in account regions", listenerID)
}

// parseELBListenerResourceID 解析 ELB 监听器资源 ID：发现侧复合形态
// "{LoadBalancerId}/{ListenerId}"，兼容存量纯监听 ID 形态（LoadBalancer ID
// 仅用于可读定位，监听器按地域+ID 寻址）。
func parseELBListenerResourceID(resourceID string) (string, string) {
	if loadBalancerID, listenerID, ok := strings.Cut(resourceID, "/"); ok && loadBalancerID != "" && listenerID != "" {
		return loadBalancerID, listenerID
	}
	return "", resourceID
}

// scmCertNameByID 经 SCM 详情解析证书名（CDN/WAF 绑定必填 certificatename
// 双字段；SCM 证书 ID 口径下名称即上传名，可观测对账用）。
func (a *CertAdapter) scmCertNameByID(ctx context.Context, creds *domain.CloudAccount, product, cloudCertID string) (string, error) {
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	client, err := a.newScmDeployClient(creds)
	if err != nil {
		return "", err
	}
	response, err := client.ShowCertificate(&scmmodel.ShowCertificateRequest{CertificateId: cloudCertID})
	if err != nil {
		if isCertDiscoveryNotFound(err) {
			return "", fmt.Errorf("huawei %s cert bind: scm certificate %s not found", product, cloudCertID)
		}
		return "", wrapCertCloudErr("scm", err)
	}
	if response == nil || strings.TrimSpace(derefString(response.Name)) == "" {
		return "", fmt.Errorf("huawei %s cert bind: scm certificate %s has empty name", product, cloudCertID)
	}
	return derefString(response.Name), nil
}

// ==================== GetCert：在库状态（回滚目标有效性校验） ====================

// GetCert 查询 SCM 证书在库状态（只读，回滚目标有效性校验依据）：
//   - Exists：ShowCertificate 非存在语义错误归一为 Exists=false 非错误
//     （云侧已删除 → 回滚三判定按无效目标处理）；
//   - Fingerprint：优先 SCM ExportCertificate 导出材料解析叶证书 SHA-256
//     （64 hex，对齐台账指纹口径——回滚前置"指纹被替换"硬等值比对依赖此口径；
//     ShowCertificate 自带指纹为 SHA-1 形态，永不匹配台账 64 hex）；导出失败
//     降级回 SHA-1 归一化指纹（上层按无法复核处理，回滚判定 fail-safe 阻断，
//     不误判有效）；
//   - NotAfter：优先导出叶证书有效期，回退 SCM 详情字段（多布局解析）。
//
// 安全口径：ExportCertificate 响应含私钥字段——本层永不读取该字段，证书
// 原始字节副本净化（仅 CERTIFICATE 块）后即刻归零，材料不留存不落日志。
func (a *CertAdapter) GetCert(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("huawei cert get: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return CloudCertInfo{}, fmt.Errorf("huawei cert get: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return CloudCertInfo{}, err
	}
	client, err := a.newScmDeployClient(creds)
	if err != nil {
		return CloudCertInfo{}, err
	}
	response, err := client.ShowCertificate(&scmmodel.ShowCertificateRequest{CertificateId: cloudCertID})
	if err != nil {
		if isCertDiscoveryNotFound(err) {
			return CloudCertInfo{Exists: false}, nil
		}
		return CloudCertInfo{}, wrapCertCloudErr("scm", err)
	}
	info := CloudCertInfo{Exists: true}
	if response != nil {
		if notAfter, ok := parseCloudCertTime(derefString(response.NotAfter)); ok {
			info.NotAfter = notAfter
		}
		info.Fingerprint = normalizeCloudCertFingerprint(derefString(response.Fingerprint))
	}
	a.enrichCertFingerprintFromExport(ctx, client, cloudCertID, &info)
	return info, nil
}

// enrichCertFingerprintFromExport 经 SCM 导出材料补齐 SHA-256 对齐指纹
// （尽力而为：失败仅告警并保留 ShowCertificate 的 SHA-1 指纹——在库存在性
// 判定不受影响，指纹对齐能力缺失由上层按无法复核降级处理）。
func (a *CertAdapter) enrichCertFingerprintFromExport(ctx context.Context, client scmDeployCertAPI, cloudCertID string, info *CloudCertInfo) {
	if err := a.waitRateLimit(ctx); err != nil {
		a.logger.Warn("华为云SCM证书导出等待限流令牌失败，指纹回退SHA-1口径",
			elog.String("cloud_cert_id", cloudCertID),
			elog.FieldErr(err))
		return
	}
	response, err := client.ExportCertificate(&scmmodel.ExportCertificateRequest{CertificateId: cloudCertID})
	if err != nil {
		a.logger.Warn("华为云SCM证书导出失败，GetCert指纹回退SHA-1口径（回滚指纹比对按无法复核处理）",
			elog.String("cloud_cert_id", cloudCertID),
			elog.FieldErr(err))
		return
	}
	if response == nil {
		return
	}
	// 优先不含证书链的证书内容，回退完整串（叶在前口径由解析取首块保证）
	raw := []byte(derefString(response.Certificate))
	if len(raw) == 0 {
		raw = []byte(derefString(response.EntireCertificate))
	}
	if len(raw) == 0 {
		return
	}
	sanitized := cloudx.SanitizeCertChainPEM(raw)
	cloudx.Zeroize(raw) // 原始材料（可能含私钥 bundle）用后即清
	block, _ := pem.Decode([]byte(sanitized))
	if block == nil {
		return
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	sum := sha256.Sum256(leaf.Raw)
	info.Fingerprint = hex.EncodeToString(sum[:])
	info.NotAfter = leaf.NotAfter
}

// ==================== CleanupOrphan：孤儿证书清理 ====================

// CleanupOrphan 删除 SCM 证书库中不再被引用的云证书（幂等：已不存在视为
// 成功，清理队列重放安全）。
func (a *CertAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("huawei cert cleanup: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return fmt.Errorf("huawei cert cleanup: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newScmDeployClient(creds)
	if err != nil {
		return err
	}
	if _, err := client.DeleteCertificate(&scmmodel.DeleteCertificateRequest{CertificateId: cloudCertID}); err != nil {
		if isCertDiscoveryNotFound(err) {
			// 已被删除 → 幂等成功（清理队列重放场景）
			a.logger.Info("华为云孤儿证书清理幂等成功（证书已不存在）",
				elog.String("cloud_cert_id", cloudCertID))
			return nil
		}
		return wrapCertCloudErr("scm", err)
	}
	a.logger.Info("华为云孤儿证书清理成功",
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}
