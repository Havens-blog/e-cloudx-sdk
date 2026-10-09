package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/gotomicro/ego/core/elog"
)

// CDN 证书产品实现。
// 发现：DescribeCdnHttpsDomainList（PageNumber/PageSize 分页，CertInfo.CertId 为 CAS 证书 ID）；
// 绑定：SetCdnDomainSSLCertificate（CertType=cas 时必填 CertId，SSLProtocol=on，CertRegion 为 CAS 地域）。

// listCDNReferences 分页遍历 CDN 开启 HTTPS 的加速域名，收集带证书的引用
func (a *CertAdapter) listCDNReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun cdn cert list: nil creds")
	}
	client, err := a.newCdnClient(creds)
	if err != nil {
		return nil, err
	}

	accountKey := creds.Name
	var refs []CloudCertRef
	collected := 0
	pageNumber := 1
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := cdn.CreateDescribeCdnHttpsDomainListRequest()
		request.Scheme = "https"
		request.PageSize = requests.NewInteger(a.certPageSize())
		request.PageNumber = requests.NewInteger(pageNumber)

		response, err := client.DescribeCdnHttpsDomainList(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductCDN, err)
		}
		entries := response.CertInfos.CertInfo
		collected += len(entries)
		for _, entry := range entries {
			// 未绑定证书的域名不构成证书引用
			if entry.CertId == "" {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "aliyun",
				Product:               CertProductCDN,
				ResourceID:            entry.DomainName,
				ReferencedCloudCertID: entry.CertId,
				AccountKey:            accountKey,
			})
		}
		// 页未满或已达 TotalCount（TotalCount=0 视为未知，仅按页数据判定）均视为遍历完成
		if len(entries) < a.certPageSize() {
			break
		}
		if response.TotalCount > 0 && collected >= response.TotalCount {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云CDN证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// bindCDN 将 CAS 证书绑定到 CDN 加速域名（替换该域名证书并开启 HTTPS）
func (a *CertAdapter) bindCDN(ctx context.Context, creds *domain.CloudAccount, domainName, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aliyun cdn cert bind: nil creds")
	}
	if domainName == "" {
		return fmt.Errorf("aliyun cdn cert bind: empty domain")
	}
	certID, err := parseCASCertID(cloudCertID)
	if err != nil {
		return err
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}

	client, err := a.newCdnClient(creds)
	if err != nil {
		return err
	}
	request := cdn.CreateSetCdnDomainSSLCertificateRequest()
	request.Scheme = "https"
	request.DomainName = domainName
	request.CertType = "cas"
	request.CertId = requests.NewInteger64(certID)
	request.CertRegion = casRegion(credsRegion(creds))
	request.SSLProtocol = "on"

	if _, err := client.SetCdnDomainSSLCertificate(request); err != nil {
		return wrapCertCloudErr(CertProductCDN, err)
	}
	a.logger.Info("阿里云CDN证书绑定成功",
		elog.String("domain", domainName),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}
