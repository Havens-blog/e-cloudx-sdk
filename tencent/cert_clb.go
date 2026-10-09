package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// CLB 证书产品实现（监听器维度）。
// 发现：按账号地域遍历 DescribeLoadBalancers（Offset/Limit 分页）→
//   逐实例 DescribeListeners（单实例一次性返回全部监听器，无分页参数），
//   监听器 Certificate.CertId（主证书）与 ExtCertIds（SNI 扩展证书）均为引用；
// 绑定：resourceID="{LoadBalancerId}/{ListenerId}" 自包含定位（监听器 ID 仅在
//   实例 + 地域内唯一，需两者联合寻址），读当前证书配置后 ModifyListener 回写——
//   新证书置主位，扩展证书与双向认证 CA 保留（替换主证书语义）。

// newCertCLBClient 构建 CLB 证书操作客户端（region 为 CLB 实例地域）
func newCertCLBClient(creds *domain.CloudAccount, region string) (*clb.Client, error) {
	credential := common.NewCredential(creds.AccessKeyID, creds.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "clb.tencentcloudapi.com"
	return clb.NewClient(credential, region, cpf)
}

// clbResourceID CLB 引用资源 ID 形态 "{LoadBalancerId}/{ListenerId}"
func clbResourceID(loadBalancerID, listenerID string) string {
	return loadBalancerID + "/" + listenerID
}

// listCLBReferences 按地域遍历 CLB 实例与监听器证书
func (a *CertAdapter) listCLBReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("tencent clb cert list: nil creds")
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range certCredsRegions(creds) {
		client, err := a.newClbClient(creds, region)
		if err != nil {
			return nil, err
		}
		lbOffset := int64(0)
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			request := clb.NewDescribeLoadBalancersRequest()
			request.Offset = common.Int64Ptr(lbOffset)
			request.Limit = common.Int64Ptr(int64(a.certPageSize()))

			response, err := client.DescribeLoadBalancers(request)
			if err != nil {
				return nil, wrapCertCloudErr(CertProductCLB, err)
			}
			if response == nil || response.Response == nil {
				break
			}
			balancers := response.Response.LoadBalancerSet
			for _, balancer := range balancers {
				if balancer == nil || balancer.LoadBalancerId == nil || *balancer.LoadBalancerId == "" {
					continue
				}
				lbRefs, err := a.listCLBBalancerReferences(ctx, client, *balancer.LoadBalancerId, accountKey)
				if err != nil {
					// 单实例监听器列举失败跳过继续，避免整体扫描失败
					a.logger.Warn("获取CLB监听器证书失败，跳过",
						elog.String("load_balancer", *balancer.LoadBalancerId),
						elog.FieldErr(err))
					continue
				}
				refs = append(refs, lbRefs...)
			}
			if len(balancers) < a.certPageSize() {
				break
			}
			lbOffset += int64(len(balancers))
		}
	}

	a.logger.Info("获取腾讯云CLB证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// listCLBBalancerReferences 收集单个 CLB 实例全部监听器的证书引用（主证书 + SNI 扩展证书）
func (a *CertAdapter) listCLBBalancerReferences(ctx context.Context, client clbCertAPI, loadBalancerID, accountKey string) ([]CloudCertRef, error) {
	if err := a.waitRateLimit(ctx); err != nil {
		return nil, err
	}
	request := clb.NewDescribeListenersRequest()
	request.LoadBalancerId = common.StringPtr(loadBalancerID)

	response, err := client.DescribeListeners(request)
	if err != nil {
		return nil, wrapCertCloudErr(CertProductCLB, err)
	}
	if response == nil || response.Response == nil {
		return nil, nil
	}

	var refs []CloudCertRef
	for _, listener := range response.Response.Listeners {
		if listener == nil || listener.ListenerId == nil || *listener.ListenerId == "" {
			continue
		}
		cert := listener.Certificate
		if cert == nil || cert.CertId == nil || *cert.CertId == "" {
			// 无服务端证书的监听器（HTTP/TCP 等）不构成证书引用
			continue
		}
		resourceID := clbResourceID(loadBalancerID, *listener.ListenerId)
		refs = append(refs, CloudCertRef{
			Cloud:                 "tencent",
			Product:               CertProductCLB,
			ResourceID:            resourceID,
			ReferencedCloudCertID: *cert.CertId,
			AccountKey:            accountKey,
		})
		for _, extCertID := range cert.ExtCertIds {
			if extCertID == nil || *extCertID == "" || *extCertID == *cert.CertId {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "tencent",
				Product:               CertProductCLB,
				ResourceID:            resourceID,
				ReferencedCloudCertID: *extCertID,
				AccountKey:            accountKey,
			})
		}
	}
	return refs, nil
}

// bindCLB 将 SSL 证书库证书绑定为 CLB 监听器服务端证书（扩展证书与双向认证保留）
func (a *CertAdapter) bindCLB(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("tencent clb cert bind: nil creds")
	}
	if cloudCertID == "" {
		return fmt.Errorf("tencent clb cert bind: empty cloud cert id")
	}
	loadBalancerID, listenerID, err := parseCertScopedResourceID(resourceID)
	if err != nil {
		return err
	}

	listener, client, err := a.findCLBListener(creds, loadBalancerID, listenerID)
	if err != nil {
		return err
	}
	cert := listener.Certificate
	if cert == nil || cert.SSLMode == nil || *cert.SSLMode == "" {
		return fmt.Errorf("tencent clb cert bind: listener %s has no certificate config", listenerID)
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	request := clb.NewModifyListenerRequest()
	request.LoadBalancerId = common.StringPtr(loadBalancerID)
	request.ListenerId = common.StringPtr(listenerID)

	extCertIDs := cert.ExtCertIds
	if len(extCertIDs) == 0 {
		// 单证书监听：直接替换服务端证书（认证模式与 CA 保留）
		request.Certificate = &clb.CertificateInput{
			SSLMode: cert.SSLMode,
			CertId:  common.StringPtr(cloudCertID),
		}
		if cert.CertCaId != nil {
			request.Certificate.CertCaId = cert.CertCaId
		}
		if cert.SSLVerifyClient != nil {
			request.Certificate.SSLVerifyClient = cert.SSLVerifyClient
		}
	} else {
		// SNI 多证书监听：MultiCertInfo 整单回写（新证书置主位，扩展证书与 CA 保留）
		certList := []*clb.CertInfo{{CertId: common.StringPtr(cloudCertID)}}
		for _, extCertID := range extCertIDs {
			if extCertID == nil || *extCertID == "" || *extCertID == cloudCertID {
				continue
			}
			certList = append(certList, &clb.CertInfo{CertId: extCertID})
		}
		if cert.CertCaId != nil && *cert.CertCaId != "" {
			certList = append(certList, &clb.CertInfo{CertId: cert.CertCaId})
		}
		request.MultiCertInfo = &clb.MultiCertInfo{
			SSLMode:  cert.SSLMode,
			CertList: certList,
		}
		if cert.SSLVerifyClient != nil {
			request.MultiCertInfo.SSLVerifyClient = cert.SSLVerifyClient
		}
	}

	if _, err := client.ModifyListener(request); err != nil {
		return wrapCertCloudErr(CertProductCLB, err)
	}
	a.logger.Info("腾讯云CLB证书绑定成功",
		elog.String("load_balancer", loadBalancerID),
		elog.String("listener", listenerID),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// findCLBListener 逐地域定位监听器当前配置（lbId 仅地域内唯一；返回监听器与所属客户端）
func (a *CertAdapter) findCLBListener(creds *domain.CloudAccount, loadBalancerID, listenerID string) (*clb.Listener, clbCertAPI, error) {
	for _, region := range certCredsRegions(creds) {
		client, err := a.newClbClient(creds, region)
		if err != nil {
			return nil, nil, err
		}
		request := clb.NewDescribeListenersRequest()
		request.LoadBalancerId = common.StringPtr(loadBalancerID)
		request.ListenerIds = common.StringPtrs([]string{listenerID})

		response, err := client.DescribeListeners(request)
		if err != nil {
			return nil, nil, wrapCertCloudErr(CertProductCLB, err)
		}
		if response == nil || response.Response == nil {
			continue
		}
		for _, listener := range response.Response.Listeners {
			if listener != nil && listener.ListenerId != nil && *listener.ListenerId == listenerID {
				return listener, client, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("tencent clb cert bind: listener %s not found in load balancer %s", listenerID, loadBalancerID)
}
