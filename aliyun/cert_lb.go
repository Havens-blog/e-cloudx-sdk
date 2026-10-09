package aliyun

import (
	"context"
	"fmt"

	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/alb"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/nlb"
	"github.com/gotomicro/ego/core/elog"
)

// ALB/NLB 证书产品实现（负载均衡监听维度）。
// 发现：ListListeners（NextToken 分页）遍历账号各地域——
//   ALB 需逐监听 ListListenerCertificates（CertificateType=Server）取证书清单；
//   NLB 监听内联 CertificateIds，无需二次查询。
// 绑定：监听 ID（lsn-*）全局唯一但需定位地域（逐地域 ListListeners 按 ListenerIds 过滤），
//   读当前证书清单后 UpdateListenerAttribute 回写——新证书置默认位，扩展证书保留（替换默认证书语义）。

// lbScopedResourceID 构造复合资源 ID "{loadBalancerId}/{listenerId}"
//（对齐腾讯 CLB/华为 ELB 口径：实例 ID 供控制台对账，监听 ID 供绑定定位）。
// loadBalancerID 为空（云侧响应异常缺字段）时回退纯监听形态，不产生 "/lsn-*" 脏值。
func lbScopedResourceID(loadBalancerID, listenerID string) string {
	if loadBalancerID == "" {
		return listenerID
	}
	return loadBalancerID + "/" + listenerID
}

// parseLBScopedResourceID 解析复合资源 ID："{loadBalancerId}/{listenerId}"；
// 无 "/" 时整串视为监听 ID（存量变更单/旧快照引用的纯监听形态，绑定兼容）。
func parseLBScopedResourceID(resourceID string) (loadBalancerID, listenerID string) {
	if idx := strings.IndexByte(resourceID, '/'); idx >= 0 {
		return resourceID[:idx], resourceID[idx+1:]
	}
	return "", resourceID
}

// listALBReferences 按地域遍历 ALB 监听及其服务器证书
func (a *CertAdapter) listALBReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun alb cert list: nil creds")
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range credsRegions(creds) {
		client, err := a.newAlbClient(creds, region)
		if err != nil {
			return nil, err
		}
		nextToken := ""
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			request := alb.CreateListListenersRequest()
			request.Scheme = "https"
			request.MaxResults = requests.NewInteger(a.certPageSize())
			request.NextToken = nextToken
			response, err := client.ListListeners(request)
			if err != nil {
				return nil, wrapCertCloudErr(CertProductALB, err)
			}
			for _, listener := range response.Listeners {
				// HTTP 监听无服务器证书
				if listener.ListenerProtocol == "HTTP" {
					continue
				}
				certs, err := a.listALBListenerCerts(ctx, client, listener.ListenerId)
				if err != nil {
					// 单监听证书列举失败跳过继续，避免整体扫描失败
					a.logger.Warn("获取ALB监听证书失败，跳过",
						elog.String("listener", listener.ListenerId),
						elog.FieldErr(err))
					continue
				}
				// ALB 监听规则提取 served hostname（Host 条件）——external DNS 记录→ALB
				// 资源级 expected 对齐依据。失败不阻塞（served 置空，回退 coverage）。
				served := a.listALBServedDomains(ctx, client, listener.ListenerId)
				for _, cert := range certs {
					refs = append(refs, CloudCertRef{
						Cloud:                 "aliyun",
						Product:               CertProductALB,
						ResourceID:            lbScopedResourceID(listener.LoadBalancerId, listener.ListenerId),
						ReferencedCloudCertID: cert.CertificateId,
						AccountKey:            accountKey,
						ServedDomains:         served,
					})
				}
			}
			if response.NextToken == "" {
				break
			}
			nextToken = response.NextToken
		}
	}

	a.logger.Info("获取阿里云ALB证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// listALBListenerCerts 遍历单个 ALB 监听的服务器证书（NextToken 分页）
func (a *CertAdapter) listALBListenerCerts(ctx context.Context, client albCertAPI, listenerID string) ([]alb.CertificateModel, error) {
	var certs []alb.CertificateModel
	nextToken := ""
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := alb.CreateListListenerCertificatesRequest()
		request.Scheme = "https"
		request.ListenerId = listenerID
		request.CertificateType = "Server"
		request.MaxResults = requests.NewInteger(a.certPageSize())
		request.NextToken = nextToken
		response, err := client.ListListenerCertificates(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductALB, err)
		}
		certs = append(certs, response.Certificates...)
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return certs, nil
}

// listALBServedDomains 遍历监听的转发规则，提取 Host 条件值（served hostname）。
// 用于 external DNS 记录（A→ALB IP）与 ALB 资源级 expected 的对齐：Phase 3
// buildReferenceIndex 按 served domain 展开 refIndex[alb|hostname]。失败返回空。
func (a *CertAdapter) listALBServedDomains(ctx context.Context, client albCertAPI, listenerID string) []string {
	seen := make(map[string]struct{})
	var out []string
	nextToken := ""
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return out
		}
		request := alb.CreateListRulesRequest()
		request.Scheme = "https"
		request.ListenerIds = &[]string{listenerID}
		request.MaxResults = requests.NewInteger(a.certPageSize())
		request.NextToken = nextToken
		response, err := client.ListRules(request)
		if err != nil {
			a.logger.Warn("获取ALB监听规则失败，跳过 served domain 提取",
				elog.String("listener", listenerID),
				elog.FieldErr(err))
			return out
		}
		for _, rule := range response.Rules {
			for _, cond := range rule.RuleConditions {
				if cond.Type != "Host" {
					continue
				}
				for _, h := range cond.HostConfig.Values {
					name := strings.TrimSpace(h)
					if name == "" {
						continue
					}
					if _, ok := seen[name]; !ok {
						seen[name] = struct{}{}
						out = append(out, name)
					}
				}
			}
		}
		if response.NextToken == "" {
			break
		}
		nextToken = response.NextToken
	}
	return out
}

// bindALB 将云证书绑定为 ALB 监听默认服务器证书（扩展证书保留）。
// resourceID 接受复合形态 "{loadBalancerId}/{listenerId}" 与存量纯监听 ID。
func (a *CertAdapter) bindALB(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aliyun alb cert bind: nil creds")
	}
	loadBalancerID, listenerID := parseLBScopedResourceID(resourceID)
	region, listener, client, err := a.findALBListener(creds, loadBalancerID, listenerID)
	if err != nil {
		return err
	}
	if listener.ListenerProtocol == "HTTP" {
		return fmt.Errorf("aliyun alb cert bind: listener %s is HTTP (no server certificate)", listenerID)
	}

	currentCerts, err := a.listALBListenerCerts(ctx, client, listenerID)
	if err != nil {
		return err
	}
	// 新证书置默认位；原默认证书移除，扩展（非默认）证书保留
	newCerts := []alb.UpdateListenerAttributeCertificates{{CertificateId: cloudCertID}}
	for _, cert := range currentCerts {
		if !cert.IsDefault && cert.CertificateId != cloudCertID {
			newCerts = append(newCerts, alb.UpdateListenerAttributeCertificates{CertificateId: cert.CertificateId})
		}
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	request := alb.CreateUpdateListenerAttributeRequest()
	request.Scheme = "https"
	request.ListenerId = listenerID
	request.Certificates = &newCerts
	if _, err := client.UpdateListenerAttribute(request); err != nil {
		return wrapCertCloudErr(CertProductALB, err)
	}
	a.logger.Info("阿里云ALB证书绑定成功",
		elog.String("region", region),
		elog.String("listener", listenerID),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// findALBListener 逐地域定位监听所在地域与实例（listenerId 全局唯一；
// 复合形态时附 LoadBalancerIds 过滤缩小范围，纯监听形态仅按 ListenerIds）
func (a *CertAdapter) findALBListener(creds *domain.CloudAccount, loadBalancerID, listenerID string) (string, alb.Listener, albCertAPI, error) {
	for _, region := range credsRegions(creds) {
		client, err := a.newAlbClient(creds, region)
		if err != nil {
			return "", alb.Listener{}, nil, err
		}
		request := alb.CreateListListenersRequest()
		request.Scheme = "https"
		request.ListenerIds = &[]string{listenerID}
		if loadBalancerID != "" {
			request.LoadBalancerIds = &[]string{loadBalancerID}
		}
		request.MaxResults = requests.NewInteger(1)
		response, err := client.ListListeners(request)
		if err != nil {
			return "", alb.Listener{}, nil, wrapCertCloudErr(CertProductALB, err)
		}
		for _, listener := range response.Listeners {
			if listener.ListenerId == listenerID {
				return region, listener, client, nil
			}
		}
	}
	return "", alb.Listener{}, nil, fmt.Errorf("aliyun alb cert bind: listener %s not found in account regions", listenerID)
}

// listNLBReferences 按地域遍历 NLB 监听及其证书 ID 清单
func (a *CertAdapter) listNLBReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun nlb cert list: nil creds")
	}
	accountKey := creds.Name
	var refs []CloudCertRef
	for _, region := range credsRegions(creds) {
		client, err := a.newNlbClient(creds, region)
		if err != nil {
			return nil, err
		}
		nextToken := ""
		for {
			if err := a.waitRateLimit(ctx); err != nil {
				return nil, err
			}
			request := nlb.CreateListListenersRequest()
			request.Scheme = "https"
			request.MaxResults = requests.NewInteger(a.certPageSize())
			request.NextToken = nextToken
			response, err := client.ListListeners(request)
			if err != nil {
				return nil, wrapCertCloudErr(CertProductNLB, err)
			}
			// NLB 以响应体 Success 表达业务成败（HTTP 200 也可能携带失败）
			if !response.Success {
				return nil, fmt.Errorf("aliyun nlb list listeners failed: code=%s message=%s", response.Code, response.Message)
			}
			for _, listener := range response.Listeners {
				for _, certID := range listener.CertificateIds {
					refs = append(refs, CloudCertRef{
						Cloud:                 "aliyun",
						Product:               CertProductNLB,
						ResourceID:            lbScopedResourceID(listener.LoadBalancerId, listener.ListenerId),
						ReferencedCloudCertID: certID,
						AccountKey:            accountKey,
					})
				}
			}
			if response.NextToken == "" {
				break
			}
			nextToken = response.NextToken
		}
	}

	a.logger.Info("获取阿里云NLB证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// bindNLB 将云证书绑定为 NLB TLS 监听默认证书（扩展证书保留）。
// resourceID 接受复合形态 "{loadBalancerId}/{listenerId}" 与存量纯监听 ID。
func (a *CertAdapter) bindNLB(ctx context.Context, creds *domain.CloudAccount, resourceID, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aliyun nlb cert bind: nil creds")
	}
	loadBalancerID, listenerID := parseLBScopedResourceID(resourceID)
	region, listener, client, err := a.findNlbListener(creds, loadBalancerID, listenerID)
	if err != nil {
		return err
	}
	if listener.ListenerProtocol != "TLS" {
		return fmt.Errorf("aliyun nlb cert bind: listener %s is %s (only TLS listeners hold certificates)", listenerID, listener.ListenerProtocol)
	}

	// 新证书置默认位（首位），原默认移除，扩展证书保留
	newCertIDs := []string{cloudCertID}
	current := listener.CertificateIds
	for i, certID := range current {
		if i == 0 || certID == cloudCertID {
			continue
		}
		newCertIDs = append(newCertIDs, certID)
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	request := nlb.CreateUpdateListenerAttributeRequest()
	request.Scheme = "https"
	request.ListenerId = listenerID
	request.CertificateIds = &newCertIDs
	response, err := client.UpdateListenerAttribute(request)
	if err != nil {
		return wrapCertCloudErr(CertProductNLB, err)
	}
	if !response.Success {
		return fmt.Errorf("aliyun nlb update listener failed: code=%s message=%s", response.Code, response.Message)
	}
	a.logger.Info("阿里云NLB证书绑定成功",
		elog.String("region", region),
		elog.String("listener", listenerID),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// findNlbListener 逐地域定位 NLB 监听所在地域与实例（复合形态附 LoadBalancerIds 过滤）
func (a *CertAdapter) findNlbListener(creds *domain.CloudAccount, loadBalancerID, listenerID string) (string, nlb.ListenerInfo, nlbCertAPI, error) {
	for _, region := range credsRegions(creds) {
		client, err := a.newNlbClient(creds, region)
		if err != nil {
			return "", nlb.ListenerInfo{}, nil, err
		}
		request := nlb.CreateListListenersRequest()
		request.Scheme = "https"
		request.ListenerIds = &[]string{listenerID}
		if loadBalancerID != "" {
			request.LoadBalancerIds = &[]string{loadBalancerID}
		}
		request.MaxResults = requests.NewInteger(1)
		response, err := client.ListListeners(request)
		if err != nil {
			return "", nlb.ListenerInfo{}, nil, wrapCertCloudErr(CertProductNLB, err)
		}
		if !response.Success {
			return "", nlb.ListenerInfo{}, nil, fmt.Errorf("aliyun nlb list listeners failed: code=%s message=%s", response.Code, response.Message)
		}
		for _, listener := range response.Listeners {
			if listener.ListenerId == listenerID {
				return region, listener, client, nil
			}
		}
	}
	return "", nlb.ListenerInfo{}, nil, fmt.Errorf("aliyun nlb cert bind: listener %s not found in account regions", listenerID)
}

// ---------------------------------------------------------------------
// 托管实例标签提取（cert-alb-ingress-managed：无 kubeconfig 的托管识别）。
// ALB Ingress Controller 在管理的 ALB 实例上打标（实测 2026-09）：
//   ingress.k8s.alibaba/albconfig = {namespace}/{albconfig-name}
//   ack.aliyun.com                = {cluster-id}
// 据此构建"控制器托管 ALB 实例 → 托管方"映射，证书变更须经 CRD 管理管道。
// ---------------------------------------------------------------------

// ALB 标签键（ALB Ingress Controller 托管标记）。
const (
	tagKeyAlbConfig  = "ingress.k8s.alibaba/albconfig"
	tagKeyAckCluster = "ack.aliyun.com"
)

// AlbManagedRef 单实例托管关系（标签派生）。
type AlbManagedRef struct {
	ClusterID string // ack.aliyun.com 标签值（ACK 集群 ID）；缺省空
	Owner     string // 托管定位 "{clusterId}/{ns}/{albconfigName}"（无集群标签时退化为 "{ns}/{name}"）
}

// managedRefFromTags 标签 → 托管关系解析（无 albconfig 标签 = 非控制器托管）。
func managedRefFromTags(tags map[string]string) (AlbManagedRef, bool) {
	cfg := tags[tagKeyAlbConfig]
	if cfg == "" {
		return AlbManagedRef{}, false
	}
	cluster := tags[tagKeyAckCluster]
	owner := cfg
	if cluster != "" {
		owner = cluster + "/" + cfg
	}
	return AlbManagedRef{ClusterID: cluster, Owner: owner}, true
}

// albTagBatchSize ListTagResources 单请求资源 ID 上限（API 约束 20）。
const albTagBatchSize = 20

// ListALBManagedInstances 批量查询 ALB 实例标签中的托管关系。分批（每批
// albTagBatchSize 个 ID）遍历，按地域执行（标签 API 为地域性端点；单地域
// 失败容忍跳过——标签缺失仅导致该实例不标注，不影响引用扫描主干）。
func (a *CertAdapter) ListALBManagedInstances(ctx context.Context, creds *domain.CloudAccount, lbIDs []string) (map[string]AlbManagedRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun alb tag list: nil creds")
	}
	out := map[string]AlbManagedRef{}
	for _, region := range credsRegions(creds) {
		client, err := a.newAlbClient(creds, region)
		if err != nil {
			return nil, err
		}
		for start := 0; start < len(lbIDs); start += albTagBatchSize {
			end := start + albTagBatchSize
			if end > len(lbIDs) {
				end = len(lbIDs)
			}
			batch := lbIDs[start:end]
			nextToken := ""
			for {
				if err := a.waitRateLimit(ctx); err != nil {
					return nil, err
				}
				request := alb.CreateListTagResourcesRequest()
				request.Scheme = "https"
				request.ResourceType = "loadbalancer"
				request.ResourceId = &batch
				request.NextToken = nextToken
				response, err := client.ListTagResources(request)
				if err != nil {
					// 单地域/单批失败容忍（辅助信号不阻塞扫描主干），记日志继续
					a.logger.Warn("aliyun alb tag list failed",
						elog.String("region", region), elog.Any("err", err))
					break
				}
				// 按 resourceId 聚合本页标签
				byID := map[string]map[string]string{}
				for _, t := range response.TagResources {
					if byID[t.ResourceId] == nil {
						byID[t.ResourceId] = map[string]string{}
					}
					byID[t.ResourceId][t.TagKey] = t.TagValue
				}
				for id, tags := range byID {
					if ref, ok := managedRefFromTags(tags); ok {
						out[id] = ref
					}
				}
				nextToken = response.NextToken
				if nextToken == "" {
					break
				}
			}
		}
	}
	return out, nil
}
