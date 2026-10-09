package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// CDN 证书产品实现。
// 发现：DescribeDomainsConfig（Offset/Limit 分页）遍历域名配置，
//   Https.CertInfo.CertId 为 SSL 证书库 CertificateId；
// 绑定：UpdateDomainConfig 回写 Https.CertInfo（域名原有 Https 开关/HTTP2/OCSP 等
//   配置不在请求中携带，云侧按字段级部分更新语义保持不变）。

// newCertCDNClient 构建 CDN 证书操作客户端（CDN 为全局服务，region 不参与寻址）
func newCertCDNClient(creds *domain.CloudAccount) (*tencentcdn.Client, error) {
	credential := common.NewCredential(creds.AccessKeyID, creds.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cdn.tencentcloudapi.com"
	return tencentcdn.NewClient(credential, "", cpf)
}

// listCDNReferences 分页遍历 CDN 域名配置，收集开启 HTTPS 且绑定证书的引用
func (a *CertAdapter) listCDNReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("tencent cdn cert list: nil creds")
	}
	client, err := a.newCdnClient(creds)
	if err != nil {
		return nil, err
	}

	accountKey := creds.Name
	var refs []CloudCertRef
	offset := int64(0)
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := tencentcdn.NewDescribeDomainsConfigRequest()
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(int64(a.certPageSize()))

		response, err := client.DescribeDomainsConfig(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductCDN, err)
		}
		if response == nil || response.Response == nil {
			break
		}
		domains := response.Response.Domains
		for _, domain := range domains {
			if domain == nil || domain.Domain == nil {
				continue
			}
			if domain.Https == nil || domain.Https.CertInfo == nil ||
				domain.Https.CertInfo.CertId == nil || *domain.Https.CertInfo.CertId == "" {
				// 未绑定证书的域名不构成证书引用
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "tencent",
				Product:               CertProductCDN,
				ResourceID:            *domain.Domain,
				ReferencedCloudCertID: *domain.Https.CertInfo.CertId,
				AccountKey:            accountKey,
			})
		}
		if len(domains) < a.certPageSize() {
			break
		}
		offset += int64(len(domains))
	}

	a.logger.Info("获取腾讯云CDN证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// bindCDN 将 SSL 证书库证书绑定到 CDN 加速域名（替换该域名服务端证书并确保 HTTPS 开启）
func (a *CertAdapter) bindCDN(ctx context.Context, creds *domain.CloudAccount, domainName, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("tencent cdn cert bind: nil creds")
	}
	if domainName == "" {
		return fmt.Errorf("tencent cdn cert bind: empty domain")
	}
	if cloudCertID == "" {
		return fmt.Errorf("tencent cdn cert bind: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}

	client, err := a.newCdnClient(creds)
	if err != nil {
		return err
	}
	request := tencentcdn.NewUpdateDomainConfigRequest()
	request.Domain = common.StringPtr(domainName)
	request.Https = &tencentcdn.Https{
		Switch: common.StringPtr("on"),
		CertInfo: &tencentcdn.ServerCert{
			CertId: common.StringPtr(cloudCertID),
		},
	}

	if _, err := client.UpdateDomainConfig(request); err != nil {
		return wrapCertCloudErr(CertProductCDN, err)
	}
	a.logger.Info("腾讯云CDN证书绑定成功",
		elog.String("domain", domainName),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}
