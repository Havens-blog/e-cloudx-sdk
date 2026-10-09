package aws

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	cloudxaws "github.com/Havens-blog/e-cloudx-sdk/common/aws"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gotomicro/ego/core/elog"
)

// AWS 只读证书发现适配（任务 3.3，tech-design Interface 2: CloudDeployer 的
// discovery-only 实现）：仅实现 ListReferences / GetCert 两个只读方法，
// UploadCert / BindResource / CleanupOrphan 一律返回哨兵 ErrDiscoveryOnly
// （ERR_DISCOVERY_ONLY 语义），不产生任何云侧写操作（代码评审口径的只读硬约束）。
//
// 产品映射（枚举对齐 schema.sql cert_references.product，各云以实际产品映射）：
//   - cdn：CloudFront 分配（ViewerCertificate 的 ACM 证书 ARN / IAM 证书 ID）
//   - alb：ELBv2（LoadBalancerType=application）监听器证书
//   - nlb：ELBv2（LoadBalancerType=network）监听器 TLS 证书
//
// AWS WAF（wafv2）为七层检测服务、非 TLS 终结点，无证书引用面，不入支持集
// （首期盲区声明见 PRD 引用发现口径）。Classic ELB 已被 AWS 淘汰且无证书监听
// 产物 SDK 模块，同样不入支持集。

// 证书产品枚举（对齐 schema.sql cert_references.product AWS 引用面）
const (
	// CertProductCDN CloudFront 分配查看器证书
	CertProductCDN = "cdn"
	// CertProductALB ELBv2 应用型负载均衡监听器证书
	CertProductALB = "alb"
	// CertProductNLB ELBv2 网络型负载均衡监听器证书
	CertProductNLB = "nlb"
)

// certSupportedProducts 只读发现支持的产品集合（未实现产品显式报错而非静默）
var certSupportedProducts = map[string]bool{
	CertProductCDN: true,
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
	// IAM-hosted（非 ARN 形态）证书 ID 不在 ACM GetCertificate 覆盖范围，GetCert
	// 返回本哨兵供上层识别为降级标记（预览 parseable=false / 导入记因跳过）。
	ErrCertPEMUnsupported = cloudx.ErrCertPEMUnsupported
	// ErrCertProductNotSupported 未实现的证书产品/接口（显式报错而非静默）
	ErrCertProductNotSupported = errors.New("aws cert product not supported")
)

// CloudCertRef 云侧证书引用（对齐 CertReference 落库字段需求：
// cloud/product/resourceId/referencedCloudCertId/accountKey，见 schema.sql cert_references）
type CloudCertRef struct {
	Cloud                 string // 固定 "aws"
	Product               string // cdn|alb|nlb
	ResourceID            string // 云资源标识：CDN=分配 ID；ALB/NLB=监听器 ARN（自包含定位）
	ReferencedCloudCertID string // 云侧证书标识：ACM 证书 ARN 或 CloudFront IAM 证书 ID
	AccountKey            string // 云账号标识（取 CloudAccount.Name）
}

// CloudCertInfo GetCert 返回的云侧证书在库状态（只读）
type CloudCertInfo struct {
	Exists      bool      // ACM 证书库中该 cloudCertId 是否存在
	NotAfter    time.Time // 云侧证书有效期截止；Exists=false 时零值
	Fingerprint string    // PEM 解析的 SHA256 hex（对齐台账指纹 ^[0-9a-f]{64}$）
	// CertChainPEM 仅 CERTIFICATE 块的净化序列（叶在前 fullchain 口径）：
	// GetCertificate 的 Certificate（叶）+CertificateChain（中间 CA/根）拼接后
	// 块级过滤构造性保证（cloudx.SanitizeCertChainPEM），不含 PRIVATE KEY 等
	// 任何非证书内容；云侧未返回 PEM 时为空。发现导入材料通道（cert-cloud-discovery-import）。
	CertChainPEM string
}

// cloudFrontCertAPI CloudFront SDK 窄接口（只读：分配列表，*cloudfront.Client 天然满足）
type cloudFrontCertAPI interface {
	ListDistributions(ctx context.Context, input *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
}

// elbCertAPI ELBv2 SDK 窄接口（只读：负载均衡与监听器列表，*elbv2.Client 天然满足）
type elbCertAPI interface {
	DescribeLoadBalancers(ctx context.Context, input *elbv2.DescribeLoadBalancersInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error)
	DescribeListeners(ctx context.Context, input *elbv2.DescribeListenersInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeListenersOutput, error)
}

// acmCertAPI ACM SDK 窄接口（只读：证书内容导出，*acm.Client 天然满足）
type acmCertAPI interface {
	GetCertificate(ctx context.Context, input *acm.GetCertificateInput, optFns ...func(*acm.Options)) (*acm.GetCertificateOutput, error)
}

// CertDiscoveryAdapter AWS 只读证书发现适配器：按产品分发。
// 凭证复用既有云账号体系（*domain.CloudAccount，静态 AK/SK Provider，
// 与既有 AWS 适配器同风格），逐调用传入；SDK 客户端工厂字段可被测试注入 fake。
type CertDiscoveryAdapter struct {
	logger       *elog.Component
	rateLimiter  *ratelimit.RateLimiter
	listPageSize int32 // ListReferences 分页大小（默认 certDiscoveryPageSize，测试可缩小以覆盖翻页分支）

	newCloudFrontClient func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error)
	newElbClient        func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error)
	newAcmClient        func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error)
}

// certDiscoveryPageSize ListReferences 默认分页大小
const certDiscoveryPageSize = int32(50)

// NewCertDiscoveryAdapter 创建 AWS 只读证书发现适配器（默认真实 SDK 客户端工厂，
// 与既有 AWS 适配器 20 QPS 限流口径一致；CloudFront/ACM 按需指定区域）
func NewCertDiscoveryAdapter(logger *elog.Component) *CertDiscoveryAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertDiscoveryAdapter{
		logger:       logger,
		rateLimiter:  ratelimit.NewRateLimiter(20),
		listPageSize: certDiscoveryPageSize,
		newCloudFrontClient: func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error) {
			return newCertDiscoveryCloudFrontClient(ctx, creds)
		},
		newElbClient: func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return newCertDiscoveryELBClient(ctx, creds, region)
		},
		newAcmClient: func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return newCertDiscoveryACMClient(ctx, creds, region)
		},
	}
}

// ==================== 只读硬约束：三个写方法一律返回哨兵 ====================

// UploadCert 两段式第一段（discovery-only 云未实现）：AWS 首期无部署器，一律返回
// ErrDiscoveryOnly，不产生任何云侧写操作（PRD Out of Scope：三云部署器二期）
func (a *CertDiscoveryAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	_ = ctx
	_ = creds
	_ = name
	_ = certPEM
	_ = keyPEM
	return "", fmt.Errorf("%w: aws %s cert upload not implemented", ErrDiscoveryOnly, product)
}

// BindResource 两段式第二段（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = resourceID
	_ = cloudCertID
	return fmt.Errorf("%w: aws %s cert bind not implemented", ErrDiscoveryOnly, product)
}

// CleanupOrphan 孤儿清理（discovery-only 云未实现）：一律返回 ErrDiscoveryOnly
func (a *CertDiscoveryAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	_ = ctx
	_ = creds
	_ = cloudCertID
	return fmt.Errorf("%w: aws cert cleanup not implemented", ErrDiscoveryOnly)
}

// ==================== 只读发现 ====================

// ListReferences 只读发现：列出产品下全部证书引用（按产品分发）
func (a *CertDiscoveryAdapter) ListReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	switch product {
	case CertProductCDN:
		return a.listCDNReferences(ctx, creds)
	case CertProductALB, CertProductNLB:
		return a.listELBReferences(ctx, creds, product)
	default:
		return nil, certProductNotSupported(product)
	}
}

// listCDNReferences 遍历 CloudFront 分配的查看器证书（ListDistributions 分页；
// 默认 CloudFront 证书不构成自持引用，仅记录 ACM ARN / IAM 证书 ID 形态）
func (a *CertDiscoveryAdapter) listCDNReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aws cloudfront cert list: nil creds")
	}
	client, err := a.newCloudFrontClient(ctx, creds)
	if err != nil {
		return nil, err
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	var marker *string
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		output, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{
			Marker:   marker,
			MaxItems: aws.Int32(a.certPageSize()),
		})
		if err != nil {
			return nil, wrapCertCloudErr(CertProductCDN, err)
		}
		if output == nil || output.DistributionList == nil {
			break
		}
		for _, item := range output.DistributionList.Items {
			if item.Id == nil || *item.Id == "" {
				continue
			}
			certID := cloudFrontViewerCertID(item.ViewerCertificate)
			if certID == "" {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "aws",
				Product:               CertProductCDN,
				ResourceID:            *item.Id,
				ReferencedCloudCertID: certID,
				AccountKey:            accountKey,
			})
		}
		list := output.DistributionList
		if list.IsTruncated == nil || !*list.IsTruncated || list.NextMarker == nil || *list.NextMarker == "" {
			break
		}
		marker = list.NextMarker
	}
	a.logger.Info("获取AWSCloudFront证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// cloudFrontViewerCertID 提取查看器证书标识：优先 ACM 证书 ARN，
// 其次 IAM 证书 ID（IAM 托管形态 GetCert 不覆盖，见 GetCert 注释）
func cloudFrontViewerCertID(vc *cftypes.ViewerCertificate) string {
	if vc == nil {
		return ""
	}
	if vc.ACMCertificateArn != nil && *vc.ACMCertificateArn != "" {
		return *vc.ACMCertificateArn
	}
	if vc.IAMCertificateId != nil && *vc.IAMCertificateId != "" {
		return *vc.IAMCertificateId
	}
	return ""
}

// listELBReferences 按地域遍历 ELBv2 负载均衡与监听器证书：
// application → alb、network → nlb（与调用方 product 过滤），监听器 Certificates
// 数组内全部 ARN 均为引用（主证书 + AdditionalCertificates）
func (a *CertDiscoveryAdapter) listELBReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aws elb cert list: nil creds")
	}
	targetType := elbv2types.LoadBalancerTypeEnumNetwork
	if product == CertProductALB {
		targetType = elbv2types.LoadBalancerTypeEnumApplication
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range certCredsRegions(creds) {
		client, err := a.newElbClient(ctx, creds, region)
		if err != nil {
			return nil, err
		}
		var marker *string
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			output, err := client.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
				Marker:   marker,
				PageSize: aws.Int32(a.certPageSize()),
			})
			if err != nil {
				return nil, wrapCertCloudErr(product, err)
			}
			if output == nil {
				break
			}
			for _, lb := range output.LoadBalancers {
				if lb.Type != targetType || lb.LoadBalancerArn == nil || *lb.LoadBalancerArn == "" {
					continue
				}
				lbRefs, err := a.listELBListenerReferences(ctx, client, *lb.LoadBalancerArn, product, accountKey)
				if err != nil {
					// 单实例监听器列举失败跳过继续，避免整体扫描失败
					a.logger.Warn("获取ELB监听器证书失败，跳过",
						elog.String("load_balancer", *lb.LoadBalancerArn),
						elog.FieldErr(err))
					continue
				}
				refs = append(refs, lbRefs...)
			}
			if output.NextMarker == nil || *output.NextMarker == "" {
				break
			}
			marker = output.NextMarker
		}
	}
	a.logger.Info("获取AWS ELB证书引用成功",
		elog.String("account", accountKey),
		elog.String("product", product),
		elog.Int("count", len(refs)))
	return refs, nil
}

// listELBListenerReferences 收集单个负载均衡全部监听器的证书引用（监听器 ARN 自包含定位）
func (a *CertDiscoveryAdapter) listELBListenerReferences(ctx context.Context, client elbCertAPI, loadBalancerArn, product, accountKey string) ([]CloudCertRef, error) {
	var refs []CloudCertRef
	var marker *string
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		output, err := client.DescribeListeners(ctx, &elbv2.DescribeListenersInput{
			LoadBalancerArn: aws.String(loadBalancerArn),
			Marker:          marker,
			PageSize:        aws.Int32(a.certPageSize()),
		})
		if err != nil {
			return nil, wrapCertCloudErr(product, err)
		}
		if output == nil {
			break
		}
		for _, listener := range output.Listeners {
			if listener.ListenerArn == nil || *listener.ListenerArn == "" {
				continue
			}
			for _, cert := range listener.Certificates {
				if cert.CertificateArn == nil || *cert.CertificateArn == "" {
					continue
				}
				refs = append(refs, CloudCertRef{
					Cloud:                 "aws",
					Product:               product,
					ResourceID:            *listener.ListenerArn,
					ReferencedCloudCertID: *cert.CertificateArn,
					AccountKey:            accountKey,
				})
			}
		}
		if output.NextMarker == nil || *output.NextMarker == "" {
			break
		}
		marker = output.NextMarker
	}
	return refs, nil
}

// GetCert 查询 ACM 证书在库状态（只读）：GetCertificate 返回叶证书与证书链 PEM，
// 按"叶在前"口径拼接 Certificate+CertificateChain 后净化为仅 CERTIFICATE 块的
// 序列（fullchain 口径与手工导入 certtest 约定对齐），解析出 SHA256 指纹与
// 有效期；证书不存在 → Exists=false。
// IAM 托管证书（无 ARN 形态 ID，CloudFront 引用的历史形态）不在 ACM
// GetCertificate 覆盖范围：返回 ErrCertPEMUnsupported 结构化降级标记
// （可被上层识别为"暂不支持自动解析"，非通用失败），不发起云 API 调用。
func (a *CertDiscoveryAdapter) GetCert(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("aws cert get: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return CloudCertInfo{}, fmt.Errorf("aws cert get: empty cloud cert id")
	}
	if !strings.HasPrefix(cloudCertID, arnPrefix) {
		return CloudCertInfo{}, fmt.Errorf("%w: aws IAM-hosted certificate id has no acm pem export", ErrCertPEMUnsupported)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return CloudCertInfo{}, err
	}
	client, err := a.newAcmClient(ctx, creds, arnRegion(cloudCertID))
	if err != nil {
		return CloudCertInfo{}, err
	}
	output, err := client.GetCertificate(ctx, &acm.GetCertificateInput{
		CertificateArn: aws.String(cloudCertID),
	})
	if err != nil {
		if cloudxaws.IsNotFoundError(err) {
			// 云侧已删除 → Exists=false（非错误）
			return CloudCertInfo{Exists: false}, nil
		}
		return CloudCertInfo{}, wrapCertCloudErr("acm", err)
	}
	info := CloudCertInfo{Exists: true}
	if output.Certificate != nil {
		if leaf, ok := parseCertLeafPEM(*output.Certificate); ok {
			sum := sha256.Sum256(leaf.Raw)
			info.Fingerprint = hex.EncodeToString(sum[:])
			info.NotAfter = leaf.NotAfter
		}
	}
	// PEM 通道净化（构造性保证）：叶在前拼接 Certificate+CertificateChain，
	// 仅保留 CERTIFICATE 块；原始字节副本净化后即刻归零。
	rawChain := concatACMCertPEM(output.Certificate, output.CertificateChain)
	info.CertChainPEM = cloudx.SanitizeCertChainPEM(rawChain)
	cloudx.Zeroize(rawChain)
	return info, nil
}

// concatACMCertPEM 拼接 ACM GetCertificate 返回的证书材料（叶在前 fullchain
// 口径）：Certificate 为叶证书 PEM、CertificateChain 为中间 CA/自签根链 PEM
// 序列；两段间补换行，避免块尾与下一块头粘连导致 pem.Decode 漏块。
func concatACMCertPEM(certificate, chain *string) []byte {
	var out []byte
	if certificate != nil && *certificate != "" {
		out = append(out, strings.TrimSpace(*certificate)...)
		out = append(out, '\n')
	}
	if chain != nil && *chain != "" {
		out = append(out, strings.TrimSpace(*chain)...)
		out = append(out, '\n')
	}
	return out
}

// ==================== 客户端工厂（真实 SDK，构建不发起网络请求） ====================

// arnPrefix AWS 资源 ARN 前缀
const arnPrefix = "arn:"

// newCertDiscoveryCloudFrontClient 构建 CloudFront 只读客户端（全局服务，us-east-1）
func newCertDiscoveryCloudFrontClient(ctx context.Context, creds *domain.CloudAccount) (*cloudfront.Client, error) {
	cfg, err := loadCertDiscoveryConfig(ctx, creds, certDefaultRegion)
	if err != nil {
		return nil, err
	}
	return cloudfront.NewFromConfig(cfg), nil
}

// newCertDiscoveryELBClient 构建 ELBv2 只读客户端（区域服务）
func newCertDiscoveryELBClient(ctx context.Context, creds *domain.CloudAccount, region string) (*elbv2.Client, error) {
	if region == "" {
		region = certDefaultRegion
	}
	cfg, err := loadCertDiscoveryConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	return elbv2.NewFromConfig(cfg), nil
}

// newCertDiscoveryACMClient 构建 ACM 只读客户端（区域按证书 ARN 解析）
func newCertDiscoveryACMClient(ctx context.Context, creds *domain.CloudAccount, region string) (*acm.Client, error) {
	if region == "" {
		region = certDefaultRegion
	}
	cfg, err := loadCertDiscoveryConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	return acm.NewFromConfig(cfg), nil
}

// certDefaultRegion 账号缺省地域（与既有 AWS Adapter 同口径）
const certDefaultRegion = "us-east-1"

// loadCertDiscoveryConfig 加载静态 AK/SK 凭证的 SDK 配置（与既有 AWS 适配器同风格）
func loadCertDiscoveryConfig(ctx context.Context, creds *domain.CloudAccount, region string) (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.AccessKeySecret, "",
		)),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("加载AWS配置失败: %w", err)
	}
	return cfg, nil
}

// arnRegion 从 ARN 提取地域（arn:partition:service:region:account:resource；
// ACM 证书为区域资源，CloudFront 引用证书固定 us-east-1），解析失败回退默认地域
func arnRegion(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 4 && parts[3] != "" {
		return parts[3]
	}
	return certDefaultRegion
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
	if cloudxaws.IsThrottlingError(err) {
		return fmt.Errorf("%w: aws %s api throttled: %v", ErrCloudRateLimited, product, err)
	}
	return fmt.Errorf("aws %s api error: %w", product, err)
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

// certCredsRegion 账号默认地域（Regions[0]，缺省 us-east-1）
func certCredsRegion(creds *domain.CloudAccount) string {
	if creds == nil || len(creds.Regions) == 0 {
		return certDefaultRegion
	}
	return creds.Regions[0]
}

// certCredsRegions 账号地域清单（ELB 按地域遍历发现；缺省回退默认地域）
func certCredsRegions(creds *domain.CloudAccount) []string {
	if creds == nil || len(creds.Regions) == 0 {
		return []string{certCredsRegion(creds)}
	}
	return creds.Regions
}
