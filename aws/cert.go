// cert.go AWS 完整证书适配器（cert-multicloud-deployers 任务 2）。
//
// 分层定位：与 3.1/3.2 aliyun/tencent CertAdapter 同构——五方法（UploadCert/
// BindResource/ListReferences/GetCert/CleanupOrphan）SDK 单次调用封装，供
// deployer.AwsDeployer 组装为 CloudDeployer 端口实例（任务 4 装配收敛：与
// 扫描适配共享实例，aliyun 模式）。
//
// 与既有 CertDiscoveryAdapter（任务 3.3，discovery-only）的关系：本类型内嵌
// 其指针复用只读发现（ListReferences/GetCert）与限流/工厂设施，并覆盖三个写
// 方法为真实云调用——只读哨兵约束（ErrDiscoveryOnly）仍由发现适配器类型自身
// 承载，本类型为部署能力形态，二者并存（任务 4 装配切换）。
//
// 云侧 ID 形态口径（与发现适配注释一致）：
//   - 证书库 = ACM（区域服务），云证书 ID = ACM 证书 ARN（自包含地域信息）；
//   - CloudFront（cdn）查看器自定义证书必须为 us-east-1 ACM 证书（AWS 硬约束，
//     certCloudFrontUploadRegion 常量锁定）——CloudFront 为全局服务，其余区域
//     ACM 证书不可作查看器证书；
//   - ALB/NLB（alb/nlb）监听证书经 AddListenerCertificates 追加（ALB HTTPS 与
//     NLB TLS 监听器同 API 形态）；ACM 为区域资源，证书 ARN 地域须与监听器
//     地域一致，跨地域目标显式失败不猜测（proposal 失败模式口径）；
//   - ACM 无证书名字段，上传名经 Name 标签承载（可观测对账用，C7 逐次唯一）。
//
// 安全口径：私钥明文仅内存透传（ImportCertificate 构参 []byte 为本层唯一副本，
// 调用完成后即刻归零，Hard Rule），禁入日志与错误文案；ACM 导出/导入接口不
// 回显私钥，GetCert 材料（叶+链）净化路径同发现适配。
package aws

import (
	"context"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk"
	cloudxaws "github.com/Havens-blog/e-cloudx-sdk/common/aws"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ACM/CloudFront/ELBv2 部署面 SDK 窄接口（真实客户端 *acm.Client 等
// 天然满足；测试注入 fake——与发现适配只读窄接口命名区分）。
type (
	// acmDeployCertAPI ACM 部署面窄接口：导入（上传）/描述（托管识别）/删除。
	acmDeployCertAPI interface {
		ImportCertificate(ctx context.Context, params *acm.ImportCertificateInput, optFns ...func(*acm.Options)) (*acm.ImportCertificateOutput, error)
		DescribeCertificate(ctx context.Context, params *acm.DescribeCertificateInput, optFns ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
		DeleteCertificate(ctx context.Context, params *acm.DeleteCertificateInput, optFns ...func(*acm.Options)) (*acm.DeleteCertificateOutput, error)
	}
	// cloudFrontBindCertAPI CloudFront 绑定面窄接口：分发配置读取（ETag）+ 更新。
	cloudFrontBindCertAPI interface {
		GetDistribution(ctx context.Context, params *cloudfront.GetDistributionInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetDistributionOutput, error)
		UpdateDistribution(ctx context.Context, params *cloudfront.UpdateDistributionInput, optFns ...func(*cloudfront.Options)) (*cloudfront.UpdateDistributionOutput, error)
	}
	// elbBindCertAPI ELBv2 绑定面窄接口：监听器证书集读取（幂等预检）+ 追加。
	// ALB（HTTPS）与 NLB（TLS）监听器共用该 API 形态——ELBv2 无独立
	// "UpdateListenerAttribute" 证书接口（ModifyListener 整单替换证书集风险高，
	// 不采用），AddListenerCertificates 即两产品的实际绑定 API 分支。
	elbBindCertAPI interface {
		DescribeListenerCertificates(ctx context.Context, params *elbv2.DescribeListenerCertificatesInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeListenerCertificatesOutput, error)
		AddListenerCertificates(ctx context.Context, params *elbv2.AddListenerCertificatesInput, optFns ...func(*elbv2.Options)) (*elbv2.AddListenerCertificatesOutput, error)
	}
)

const (
	// certCloudFrontUploadRegion CloudFront 证书上传地域（AWS 硬约束：CloudFront
	// 查看器自定义证书必须为 us-east-1 ACM 证书，Hard Rule 文档化+单测锁定）。
	// 与 certDefaultRegion 数值一致但语义独立——前者是产品约束常量，后者是账号
	// 缺省地域回退值，勿互相替代。
	certCloudFrontUploadRegion = "us-east-1"
	// certDefaultViewerTLSProtocol 自定义查看器证书的缺省最低 TLS 协议版本：
	// SSLv3/TLSv1 仅限 CloudFront 默认证书使用，自定义证书须 TLSv1_2016+；
	// 旧配置为默认证书形态时升到该缺省值，已是自定义形态则保留原值。
	certDefaultViewerTLSProtocol = cftypes.MinimumProtocolVersionTLSv122021
)

// CertAdapter AWS 完整证书适配器：五方法按产品分发（cdn/alb/nlb）。
// 凭证复用既有云账号体系（*domain.CloudAccount），逐调用传入，不在适配层
// 新建凭证存储；SDK 客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	*CertDiscoveryAdapter // ListReferences/GetCert/限流/产品枚举/公共辅助复用（引用形态同口径）

	newAcmDeployClient      func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error)
	newCloudFrontBindClient func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error)
	newElbBindClient        func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error)
}

// NewCertAdapter 创建 AWS 完整证书适配器（默认真实 SDK 客户端工厂，复用既有
// 云账号凭证体系与 20 QPS 限流口径；CloudFront 为全局服务固定 us-east-1 接入）。
func NewCertAdapter(logger *elog.Component) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertAdapter{
		CertDiscoveryAdapter: NewCertDiscoveryAdapter(logger),
		newAcmDeployClient: func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return newCertDeployACMClient(ctx, creds, region)
		},
		newCloudFrontBindClient: func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			return newCertDeployCloudFrontClient(ctx, creds)
		},
		newElbBindClient: func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			return newCertDeployELBClient(ctx, creds, region)
		},
	}
}

// ==================== 两段式第一段：上传 ====================

// UploadCert 导入证书束至 ACM（ImportCertificate），返回 ACM 证书 ARN（各产品
// 绑定引用的统一云证书 ID）。地域路由：
//   - cdn（CloudFront）：恒 us-east-1（AWS 硬约束，certCloudFrontUploadRegion）；
//   - alb/nlb：账号主地域（ACM 区域资源，证书须与监听器同地域可用）。
//
// certPEM 为叶在前证书束（可含证书链）：首个 CERTIFICATE 块入 Certificate 字段
// （ACM 仅接受单证书），其余 CERTIFICATE 块入 CertificateChain；keyPEM 为明文
// 私钥（仅内存透传，构参副本用后归零，不落日志）。name 经 Name 标签承载
// （ACM 无证书名字段；deployer 层逐次唯一生成，C7）。
func (a *CertAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	if !certSupportedProducts[product] {
		return "", certProductNotSupported(product)
	}
	if creds == nil {
		return "", fmt.Errorf("aws %s cert upload: nil creds", product)
	}
	if name == "" || certPEM == "" || keyPEM == "" {
		return "", fmt.Errorf("aws %s cert upload: name/cert/key required", product)
	}
	region := certCredsRegion(creds)
	if product == CertProductCDN {
		region = certCloudFrontUploadRegion
	}
	leafPEM, chainPEM, err := splitCertBundlePEM(certPEM)
	if err != nil {
		return "", fmt.Errorf("aws %s cert upload: %w", product, err)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	client, err := a.newAcmDeployClient(ctx, creds, region)
	if err != nil {
		return "", err
	}
	input := &acm.ImportCertificateInput{
		Certificate: []byte(leafPEM),
		PrivateKey:  []byte(keyPEM), // 明文副本（本层唯一可控归零形态）
		Tags:        []acmtypes.Tag{{Key: aws.String("Name"), Value: aws.String(name)}},
	}
	if chainPEM != "" {
		input.CertificateChain = []byte(chainPEM)
	}
	// 私钥明文用后即清（Hard Rule：仅内存传递；调用完成即归零，含失败路径）。
	defer cloudx.Zeroize(input.PrivateKey)
	output, err := client.ImportCertificate(ctx, input)
	if err != nil {
		return "", wrapCertCloudErr(product, err)
	}
	certArn := ""
	if output != nil {
		certArn = aws.ToString(output.CertificateArn)
	}
	if certArn == "" {
		return "", fmt.Errorf("aws %s cert upload: empty certificate arn", product)
	}
	a.logger.Info("AWSACM证书上传成功",
		elog.String("product", product),
		elog.String("region", region),
		elog.String("certificate_arn", certArn))
	return certArn, nil
}

// splitCertBundlePEM 拆分证书束（叶在前 fullchain 口径）：首个 CERTIFICATE 块
// 为叶证书（ACM Certificate 字段仅接受单证书），其余 CERTIFICATE 块为中间 CA/
// 根链（CertificateChain 字段）；非证书块（私钥等）剔除。无 CERTIFICATE 块即
// 显式失败（显式失败不猜测口径）。
func splitCertBundlePEM(bundle string) (leaf, chain string, err error) {
	rest := []byte(bundle)
	first := true
	var leafBuf, chainBuf []byte
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		encoded := pem.EncodeToMemory(block)
		if first {
			leafBuf = append(leafBuf, encoded...)
			first = false
			continue
		}
		chainBuf = append(chainBuf, encoded...)
	}
	if first {
		return "", "", fmt.Errorf("cert pem contains no CERTIFICATE block")
	}
	return string(leafBuf), string(chainBuf), nil
}

// ==================== 两段式第二段：绑定 ====================

// BindResource 将 ACM 证书绑定至产品资源（按产品分发）：
//   - cdn：CloudFront 分发级（GetDistribution 读配置与 ETag → UpdateDistribution
//     写 ViewerCertificate，IfMatch 并发控制；Aliases 属 CNAME 配置面不改动）；
//   - alb/nlb：监听器级（DescribeListenerCertificates 幂等预检 →
//     AddListenerCertificates 追加，ALB HTTPS 与 NLB TLS 同 API 形态）。
func (a *CertAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	switch product {
	case CertProductCDN:
		return a.bindCloudFront(ctx, creds, resourceID, cloudCertID)
	case CertProductALB, CertProductNLB:
		return a.bindELBListener(ctx, creds, product, resourceID, cloudCertID)
	default:
		return certProductNotSupported(product)
	}
}

// bindCloudFront 将 us-east-1 ACM 证书绑定为 CloudFront 分发查看器证书：
// 读分发配置（GetDistribution 返回完整 DistributionConfig 与 ETag）→ 仅替换
// ViewerCertificate 字段（ACMCertificateArn + acm 来源 + sni-only + 显式关闭
// 默认证书位）→ IfMatch 并发控制提交更新。查看器已引用目标 ARN 时幂等跳过
// （回滚重放/重复绑定安全，避免分发更新传播成本）。
func (a *CertAdapter) bindCloudFront(ctx context.Context, creds *domain.CloudAccount, distributionID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aws cdn cert bind: nil creds")
	}
	distributionID = strings.TrimSpace(distributionID)
	if distributionID == "" {
		return fmt.Errorf("aws cdn cert bind: empty distribution id")
	}
	certArn := strings.TrimSpace(cloudCertID)
	if certArn == "" {
		return fmt.Errorf("aws cdn cert bind: empty cloud cert id")
	}
	// CloudFront 查看器自定义证书仅支持 us-east-1 ACM ARN 形态：IAM 托管历史
	// 形态（非 ARN）不可经本路径绑定（GetCert 同口径为结构化降级标记，恢复须
	// 人工控制台操作）；跨地域 ACM 证书显式失败不猜测。
	if !strings.HasPrefix(certArn, arnPrefix) {
		return fmt.Errorf("aws cdn cert bind: cloud cert id %q is not an acm arn (IAM-hosted viewer certificates require manual restore)", certArn)
	}
	if region := arnRegion(certArn); region != certCloudFrontUploadRegion {
		return fmt.Errorf("aws cdn cert bind: acm certificate region %s is not %s (cloudfront viewer certificates require us-east-1)", region, certCloudFrontUploadRegion)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newCloudFrontBindClient(ctx, creds)
	if err != nil {
		return err
	}
	getOut, err := client.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(distributionID)})
	if err != nil {
		return wrapCertCloudErr(CertProductCDN, err)
	}
	if getOut == nil || getOut.Distribution == nil || getOut.Distribution.DistributionConfig == nil {
		return fmt.Errorf("aws cdn cert bind: distribution %s returned empty config", distributionID)
	}
	etag := aws.ToString(getOut.ETag)
	if etag == "" {
		return fmt.Errorf("aws cdn cert bind: distribution %s returned empty etag", distributionID)
	}
	config := getOut.Distribution.DistributionConfig
	// 幂等：查看器证书已引用目标 ARN → no-op 成功
	if vc := config.ViewerCertificate; vc != nil && aws.ToString(vc.ACMCertificateArn) == certArn {
		a.logger.Info("AWSCloudFront证书绑定幂等跳过（查看器证书已引用目标ARN）",
			elog.String("distribution", distributionID),
			elog.String("certificate_arn", certArn))
		return nil
	}
	config.ViewerCertificate = buildViewerCertificate(config.ViewerCertificate, certArn)
	// Aliases（CNAME）属分发域名配置面，证书绑定不改动（证书 SAN 覆盖校验由
	// 云侧执行）；DistributionConfig 整体回写为 UpdateDistribution 契约要求。
	if _, err := client.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
		DistributionConfig: config,
		Id:                 aws.String(distributionID),
		IfMatch:            aws.String(etag),
	}); err != nil {
		return wrapCertCloudErr(CertProductCDN, err)
	}
	a.logger.Info("AWSCloudFront证书绑定成功",
		elog.String("distribution", distributionID),
		elog.String("certificate_arn", certArn))
	return nil
}

// buildViewerCertificate 构造自定义 ACM 查看器证书配置：ACM ARN + acm 来源 +
// sni-only + 显式关闭默认证书位；最低协议版本保留原配置（已是自定义证书兼容
// 版本 TLSv1_2016+ 时），否则升到缺省值（SSLv3/TLSv1 仅限 CloudFront 默认证书）。
func buildViewerCertificate(old *cftypes.ViewerCertificate, certArn string) *cftypes.ViewerCertificate {
	vc := &cftypes.ViewerCertificate{
		ACMCertificateArn:            aws.String(certArn),
		CertificateSource:            cftypes.CertificateSourceAcm,
		CloudFrontDefaultCertificate: aws.Bool(false),
		SSLSupportMethod:             cftypes.SSLSupportMethodSniOnly,
		MinimumProtocolVersion:       certDefaultViewerTLSProtocol,
	}
	if old != nil && isCustomViewerProtocol(old.MinimumProtocolVersion) {
		vc.MinimumProtocolVersion = old.MinimumProtocolVersion
	}
	return vc
}

// isCustomViewerProtocol 原最低协议版本是否自定义证书兼容（SSLv3/TLSv1 仅限
// CloudFront 默认证书使用，自定义证书须 TLSv1_2016 及以上；空值视为未配置）。
func isCustomViewerProtocol(v cftypes.MinimumProtocolVersion) bool {
	switch v {
	case "", cftypes.MinimumProtocolVersionSSLv3, cftypes.MinimumProtocolVersionTLSv1:
		return false
	}
	return true
}

// bindELBListener 将 ACM 证书追加至 ALB（HTTPS）/NLB（TLS）监听器证书集
// （AddListenerCertificates 形态——ELBv2 两产品实际绑定 API；不设 IsDefault，
// 默认证书位不改动，新证书经 SNI 主机名匹配承接目标域名流量）。证书 ARN 地域
// 须与监听器 ARN 地域一致（ACM 区域资源），跨地域目标显式失败不猜测——需按
// 地域分别导入证书材料。监听器已含目标证书时幂等跳过（回滚重放安全）。
func (a *CertAdapter) bindELBListener(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aws %s cert bind: nil creds", product)
	}
	listenerArn := strings.TrimSpace(resourceID)
	if listenerArn == "" {
		return fmt.Errorf("aws %s cert bind: empty listener arn", product)
	}
	certArn := strings.TrimSpace(cloudCertID)
	if certArn == "" {
		return fmt.Errorf("aws %s cert bind: empty cloud cert id", product)
	}
	if !strings.HasPrefix(certArn, arnPrefix) {
		return fmt.Errorf("aws %s cert bind: cloud cert id %q is not an acm arn", product, certArn)
	}
	listenerRegion := arnRegion(listenerArn)
	certRegion := arnRegion(certArn)
	if listenerRegion != certRegion {
		return fmt.Errorf("aws %s cert bind: listener region %s differs from acm certificate region %s (import certificate per region)", listenerRegion, certRegion, product)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newElbBindClient(ctx, creds, listenerRegion)
	if err != nil {
		return err
	}
	attached, err := a.listenerHasCertificate(ctx, client, listenerArn, certArn)
	if err != nil {
		return err
	}
	if attached {
		a.logger.Info("AWSELB证书绑定幂等跳过（证书已挂载）",
			elog.String("product", product),
			elog.String("listener", listenerArn),
			elog.String("certificate_arn", certArn))
		return nil
	}
	if _, err := client.AddListenerCertificates(ctx, &elbv2.AddListenerCertificatesInput{
		ListenerArn: aws.String(listenerArn),
		// 单证书追加且不设 IsDefault（AddListenerCertificates 契约：默认位不改动）
		Certificates: []elbv2types.Certificate{{CertificateArn: aws.String(certArn)}},
	}); err != nil {
		return wrapCertCloudErr(product, err)
	}
	a.logger.Info("AWSELB证书绑定成功",
		elog.String("product", product),
		elog.String("listener", listenerArn),
		elog.String("certificate_arn", certArn))
	return nil
}

// listenerHasCertificate 监听器证书集是否已含目标证书（分页遍历；默认证书与
// SNI 扩展证书同集返回）。
func (a *CertAdapter) listenerHasCertificate(ctx context.Context, client elbBindCertAPI, listenerArn, certArn string) (bool, error) {
	var marker *string
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return false, err
		}
		output, err := client.DescribeListenerCertificates(ctx, &elbv2.DescribeListenerCertificatesInput{
			ListenerArn: aws.String(listenerArn),
			Marker:      marker,
			PageSize:    aws.Int32(a.certPageSize()),
		})
		if err != nil {
			return false, wrapCertCloudErr("elb", err)
		}
		if output == nil {
			return false, nil
		}
		for _, cert := range output.Certificates {
			if aws.ToString(cert.CertificateArn) == certArn {
				return true, nil
			}
		}
		if output.NextMarker == nil || *output.NextMarker == "" {
			return false, nil
		}
		marker = output.NextMarker
	}
}

// ==================== CleanupOrphan：孤儿证书清理 ====================

// CleanupOrphan 删除 ACM 导入证书（幂等：已不存在视为成功，清理队列重放安全）。
//   - 非 ARN 形态（IAM 托管证书 ID）不属 ACM 库资源，显式拒绝删除；
//   - AWS 托管证书（AMAZON_ISSUED/PRIVATE，ACM 签发/私有 CA 形态）非本链路
//     上传产物，显式识别拒绝——其生命周期由 ACM 签发流程管理，防误删在用
//     托管证书（DescribeCertificate.Type 判定）。
func (a *CertAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aws cert cleanup: nil creds")
	}
	certArn := strings.TrimSpace(cloudCertID)
	if certArn == "" {
		return fmt.Errorf("aws cert cleanup: empty cloud cert id")
	}
	if !strings.HasPrefix(certArn, arnPrefix) {
		return fmt.Errorf("aws cert cleanup: %q is not an acm certificate arn (refuse to delete)", certArn)
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	client, err := a.newAcmDeployClient(ctx, creds, arnRegion(certArn))
	if err != nil {
		return err
	}
	describe, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: aws.String(certArn)})
	if err != nil {
		if cloudxaws.IsNotFoundError(err) {
			// 已被删除 → 幂等成功（清理队列重放场景）
			a.logger.Info("AWS孤儿证书清理幂等成功（证书已不存在）",
				elog.String("certificate_arn", certArn))
			return nil
		}
		return wrapCertCloudErr("acm", err)
	}
	if describe != nil && describe.Certificate != nil && describe.Certificate.Type != acmtypes.CertificateTypeImported {
		// Type 为值类型枚举：非 IMPORTED（含未知空值）一律拒绝，fail-safe 不猜测
		return fmt.Errorf("aws cert cleanup: certificate %s is %s (AWS-managed certificates are not orphan cleanup targets)",
			certArn, describe.Certificate.Type)
	}
	if _, err := client.DeleteCertificate(ctx, &acm.DeleteCertificateInput{CertificateArn: aws.String(certArn)}); err != nil {
		if cloudxaws.IsNotFoundError(err) {
			// 描述与删除间隙被并发删除 → 幂等成功
			a.logger.Info("AWS孤儿证书清理幂等成功（删除时已不存在）",
				elog.String("certificate_arn", certArn))
			return nil
		}
		return wrapCertCloudErr("acm", err)
	}
	a.logger.Info("AWS孤儿证书清理成功",
		elog.String("certificate_arn", certArn))
	return nil
}

// ==================== 部署面客户端工厂（真实 SDK，构建不发起网络请求） ====================

// newCertDeployACMClient 构建 ACM 部署面客户端（区域服务，地域由调用方路由：
// CloudFront 固定 us-east-1 / 其他产品账号主地域 / 清理按证书 ARN 地域）
func newCertDeployACMClient(ctx context.Context, creds *domain.CloudAccount, region string) (*acm.Client, error) {
	if region == "" {
		region = certDefaultRegion
	}
	cfg, err := loadCertDiscoveryConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	return acm.NewFromConfig(cfg), nil
}

// newCertDeployCloudFrontClient 构建 CloudFront 绑定面客户端（全局服务，固定
// us-east-1 接入，与查看器证书地域约束一致）
func newCertDeployCloudFrontClient(ctx context.Context, creds *domain.CloudAccount) (*cloudfront.Client, error) {
	cfg, err := loadCertDiscoveryConfig(ctx, creds, certCloudFrontUploadRegion)
	if err != nil {
		return nil, err
	}
	return cloudfront.NewFromConfig(cfg), nil
}

// newCertDeployELBClient 构建 ELBv2 绑定面客户端（区域服务，按监听器 ARN 地域）
func newCertDeployELBClient(ctx context.Context, creds *domain.CloudAccount, region string) (*elbv2.Client, error) {
	if region == "" {
		region = certDefaultRegion
	}
	cfg, err := loadCertDiscoveryConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	return elbv2.NewFromConfig(cfg), nil
}
