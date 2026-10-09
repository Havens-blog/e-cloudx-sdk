package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
)

// EdgeOne 证书产品实现（product=waf，见 schema.sql tencent 产品枚举 waf(EdgeOne)）。
// 注意：经典 WAF（waf.tencentcloudapi.com）域名证书首期未纳入本适配（任务 3.2 产品
// 集合为 cdn/waf(EdgeOne)/clb），经典 WAF 引用属扫描盲区，PoC 5.12 登记验证。
//
// 发现：DescribeZones（Offset/Limit 分页）遍历站点 → 逐站点 DescribeHostsSetting
//   （Offset/Limit 分页）取 Hosts.Https.CertInfo[].CertId；
// 绑定：读当前主机证书清单 → ModifyHostsCertificate（Mode=sslcert）整单回写。
//
// EdgeOne 证书绑定粒度说明（PoC 5.12 登记项）：单主机可绑定多本服务端证书
// （SNI 按域名匹配），API 无默认/主证书标记。本适配约定清单首位为主证书位：
// 绑定=新证书置首位、原首位移除、其余（扩展）证书保留。若待更换证书位于
// 非首位（多证书主机边缘场景），更换后旧证书仍保留绑定，需复扫核实并人工处理，
// 不做静默降级。

// newCertTEOClient 构建 EdgeOne 证书操作客户端（TEO 为全局服务，region 不参与寻址）
func newCertTEOClient(creds *domain.CloudAccount) (*teo.Client, error) {
	credential := common.NewCredential(creds.AccessKeyID, creds.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "teo.tencentcloudapi.com"
	return teo.NewClient(credential, "", cpf)
}

// edgeOneResourceID EdgeOne 引用资源 ID 形态 "{ZoneId}/{Host}"（自包含定位，绑定无需二次查站点）
func edgeOneResourceID(zoneID, host string) string {
	return zoneID + "/" + host
}

// listEdgeOneReferences 遍历 EdgeOne 站点与站点内域名，收集证书引用
func (a *CertAdapter) listEdgeOneReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("tencent edgeone cert list: nil creds")
	}
	client, err := a.newTeoClient(creds)
	if err != nil {
		return nil, err
	}

	accountKey := creds.Name
	var refs []CloudCertRef
	zoneOffset := int64(0)
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := teo.NewDescribeZonesRequest()
		request.Offset = common.Int64Ptr(zoneOffset)
		request.Limit = common.Int64Ptr(int64(a.certPageSize()))

		response, err := client.DescribeZones(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductWAF, err)
		}
		if response == nil || response.Response == nil {
			break
		}
		zones := response.Response.Zones
		for _, zone := range zones {
			if zone == nil || zone.ZoneId == nil || *zone.ZoneId == "" {
				continue
			}
			zoneRefs, err := a.listEdgeOneZoneReferences(ctx, client, *zone.ZoneId, accountKey)
			if err != nil {
				return nil, err
			}
			refs = append(refs, zoneRefs...)
		}
		if len(zones) < a.certPageSize() {
			break
		}
		zoneOffset += int64(len(zones))
	}

	a.logger.Info("获取腾讯云EdgeOne证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// listEdgeOneZoneReferences 分页遍历单个站点内域名的证书清单
func (a *CertAdapter) listEdgeOneZoneReferences(ctx context.Context, client teoCertAPI, zoneID, accountKey string) ([]CloudCertRef, error) {
	var refs []CloudCertRef
	offset := int64(0)
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := teo.NewDescribeHostsSettingRequest()
		request.ZoneId = common.StringPtr(zoneID)
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(int64(a.certPageSize()))

		response, err := client.DescribeHostsSetting(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductWAF, err)
		}
		if response == nil || response.Response == nil {
			break
		}
		hosts := response.Response.DetailHosts
		for _, host := range hosts {
			if host == nil || host.Host == nil || host.ZoneId == nil || *host.Host == "" {
				continue
			}
			if host.Https == nil {
				continue
			}
			for _, cert := range host.Https.CertInfo {
				if cert == nil || cert.CertId == nil || *cert.CertId == "" {
					continue
				}
				refs = append(refs, CloudCertRef{
					Cloud:                 "tencent",
					Product:               CertProductWAF,
					ResourceID:            edgeOneResourceID(*host.ZoneId, *host.Host),
					ReferencedCloudCertID: *cert.CertId,
					AccountKey:            accountKey,
				})
			}
		}
		if len(hosts) < a.certPageSize() {
			break
		}
		offset += int64(len(hosts))
	}
	return refs, nil
}

// edgeOneHostCerts 查询主机当前证书清单（返回按序 CertId 列表；found=false 表示站点内无此主机）
func (a *CertAdapter) edgeOneHostCerts(ctx context.Context, client teoCertAPI, zoneID, hostName string) (certIDs []string, found bool, err error) {
	offset := int64(0)
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, false, err
		}
		request := teo.NewDescribeHostsSettingRequest()
		request.ZoneId = common.StringPtr(zoneID)
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(int64(a.certPageSize()))

		response, err := client.DescribeHostsSetting(request)
		if err != nil {
			return nil, false, wrapCertCloudErr(CertProductWAF, err)
		}
		if response == nil || response.Response == nil {
			return nil, false, nil
		}
		hosts := response.Response.DetailHosts
		for _, host := range hosts {
			if host == nil || host.Host == nil || *host.Host != hostName {
				continue
			}
			var ids []string
			if host.Https != nil {
				for _, cert := range host.Https.CertInfo {
					if cert == nil || cert.CertId == nil || *cert.CertId == "" {
						continue
					}
					ids = append(ids, *cert.CertId)
				}
			}
			return ids, true, nil
		}
		if len(hosts) < a.certPageSize() {
			return nil, false, nil
		}
		offset += int64(len(hosts))
	}
}

// bindEdgeOne 将 SSL 证书库证书绑定到 EdgeOne 域名（resourceID="{ZoneId}/{Host}"）。
// 读改写：新证书置主位（首位），原主位移除，扩展证书保留（粒度约定见文件头注释）。
func (a *CertAdapter) bindEdgeOne(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("tencent edgeone cert bind: nil creds")
	}
	if cloudCertID == "" {
		return fmt.Errorf("tencent edgeone cert bind: empty cloud cert id")
	}
	zoneID, hostName, err := parseCertScopedResourceID(resourceID)
	if err != nil {
		return err
	}

	client, err := a.newTeoClient(creds)
	if err != nil {
		return err
	}
	current, found, err := a.edgeOneHostCerts(ctx, client, zoneID, hostName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("tencent edgeone cert bind: host %s not found in zone %s", hostName, zoneID)
	}
	if len(current) == 0 {
		return fmt.Errorf("tencent edgeone cert bind: host %s has no certificate config", hostName)
	}

	certs := []*teo.ServerCertInfo{{CertId: common.StringPtr(cloudCertID)}}
	for i, certID := range current {
		if i == 0 || certID == cloudCertID {
			// 原首位=被替换的主证书；新证书去重
			continue
		}
		certs = append(certs, &teo.ServerCertInfo{CertId: common.StringPtr(certID)})
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	request := teo.NewModifyHostsCertificateRequest()
	request.ZoneId = common.StringPtr(zoneID)
	request.Hosts = common.StringPtrs([]string{hostName})
	request.Mode = common.StringPtr("sslcert")
	request.ServerCertInfo = certs

	if _, err := client.ModifyHostsCertificate(request); err != nil {
		return wrapCertCloudErr(CertProductWAF, err)
	}
	a.logger.Info("腾讯云EdgeOne证书绑定成功",
		elog.String("zone", zoneID),
		elog.String("host", hostName),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}
