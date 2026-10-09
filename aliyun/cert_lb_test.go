package aliyun

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/alb"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/nlb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== ALB ====================

// fakeAlbClient ALB SDK fake（按地域分发）
type fakeAlbClient struct {
	listeners        []alb.Listener
	listenersByToken map[string][]alb.Listener // NextToken 分页模拟（简化：单页+token 翻转）
	listErr          error
	certPages        map[string][][]alb.CertificateModel // listenerId -> 证书分页
	certErr          map[string]error
	updateErr        error
	updateReqs       []*alb.UpdateListenerAttributeRequest
	listCalls        int
	rulesByListener  map[string][]alb.Rule // listenerId -> 转发规则（提取 served domain）
	rulesErr         error
	tagResources     []alb.TagResource     // ListTagResources 返回（托管标签提取）
	tagErr           error
}

func (f *fakeAlbClient) ListTagResources(request *alb.ListTagResourcesRequest) (*alb.ListTagResourcesResponse, error) {
	if f.tagErr != nil {
		return nil, f.tagErr
	}
	resp := alb.CreateListTagResourcesResponse()
	wanted := map[string]bool{}
	if request.ResourceId != nil {
		for _, id := range *request.ResourceId {
			wanted[id] = true
		}
	}
	for _, t := range f.tagResources {
		if !wanted[t.ResourceId] {
			continue
		}
		resp.TagResources = append(resp.TagResources, t)
	}
	return resp, nil
}

func (f *fakeAlbClient) ListListeners(request *alb.ListListenersRequest) (*alb.ListListenersResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	resp := alb.CreateListListenersResponse()
	for _, lsn := range f.listeners {
		if request.ListenerIds != nil && len(*request.ListenerIds) > 0 && !certContainsString(*request.ListenerIds, lsn.ListenerId) {
			continue
		}
		if request.LoadBalancerIds != nil && len(*request.LoadBalancerIds) > 0 && !certContainsString(*request.LoadBalancerIds, lsn.LoadBalancerId) {
			continue
		}
		resp.Listeners = append(resp.Listeners, lsn)
	}
	resp.TotalCount = len(resp.Listeners)
	return resp, nil
}

func (f *fakeAlbClient) ListListenerCertificates(request *alb.ListListenerCertificatesRequest) (*alb.ListListenerCertificatesResponse, error) {
	if err, ok := f.certErr[request.ListenerId]; ok && err != nil {
		return nil, err
	}
	pages := f.certPages[request.ListenerId]
	pageIdx := 0
	if request.NextToken != "" {
		pageIdx = 1
	}
	resp := alb.CreateListListenerCertificatesResponse()
	if pageIdx >= 0 && pageIdx < len(pages) {
		resp.Certificates = pages[pageIdx]
		resp.TotalCount = len(pages[pageIdx])
		if pageIdx+1 < len(pages) {
			resp.NextToken = "next"
		}
	}
	return resp, nil
}

func (f *fakeAlbClient) UpdateListenerAttribute(request *alb.UpdateListenerAttributeRequest) (*alb.UpdateListenerAttributeResponse, error) {
	f.updateReqs = append(f.updateReqs, request)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	resp := alb.CreateUpdateListenerAttributeResponse()
	resp.JobId = "job-1"
	return resp, nil
}

// ListRules 模拟 ALB 转发规则查询：返回该监听预置的规则（Host 条件提取 served domain）。
func (f *fakeAlbClient) ListRules(request *alb.ListRulesRequest) (*alb.ListRulesResponse, error) {
	if f.rulesErr != nil {
		return nil, f.rulesErr
	}
	resp := alb.CreateListRulesResponse()
	if request.ListenerIds != nil {
		for _, lsnID := range *request.ListenerIds {
			resp.Rules = append(resp.Rules, f.rulesByListener[lsnID]...)
		}
	}
	return resp, nil
}

func certContainsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func TestCertAdapterALBListReferences(t *testing.T) {
	t.Run("NextToken分页遍历监听与证书", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-1", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-1"},
				{ListenerId: "lsn-2", ListenerProtocol: "HTTP", LoadBalancerId: "alb-1"},
				{ListenerId: "lsn-3", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-2"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-1": {{
					{CertificateId: "111-cn-hangzhou", IsDefault: true, CertificateType: "Server"},
					{CertificateId: "222-cn-hangzhou", IsDefault: false, CertificateType: "Server"},
				}},
				"lsn-3": {{{CertificateId: "333-cn-hangzhou", IsDefault: true, CertificateType: "Server"}}},
			},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			if region == "cn-shanghai" {
				return &fakeAlbClient{}, nil
			}
			return fake, nil
		}

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductALB)
		require.NoError(t, err)
		require.Len(t, refs, 3)

		assert.Equal(t, "alb-1/lsn-1", refs[0].ResourceID)
		assert.Equal(t, "111-cn-hangzhou", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "222-cn-hangzhou", refs[1].ReferencedCloudCertID)
		assert.Equal(t, "aliyun", refs[1].Cloud)
		assert.Equal(t, CertProductALB, refs[1].Product)
		assert.Equal(t, "aliyun-main", refs[1].AccountKey)

		assert.Equal(t, "alb-2/lsn-3", refs[2].ResourceID)
		assert.Equal(t, "333-cn-hangzhou", refs[2].ReferencedCloudCertID)
	})

	t.Run("证书分页NextToken遍历", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-1", ListenerProtocol: "HTTPS"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-1": {
					{{CertificateId: "111-cn-hangzhou", IsDefault: true, CertificateType: "Server"}},
					{{CertificateId: "222-cn-hangzhou", IsDefault: false, CertificateType: "Server"}},
				},
			},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			if region == "cn-shanghai" {
				return &fakeAlbClient{}, nil
			}
			return fake, nil
		}

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductALB)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		assert.Equal(t, "222-cn-hangzhou", refs[1].ReferencedCloudCertID)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			return &fakeAlbClient{listErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductALB)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("单监听证书列举失败跳过继续", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-1", ListenerProtocol: "HTTPS"},
				{ListenerId: "lsn-2", ListenerProtocol: "HTTPS"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-2": {{{CertificateId: "222-cn-hangzhou", IsDefault: true, CertificateType: "Server"}}},
			},
			certErr: map[string]error{"lsn-1": errors.New("listener cert list boom")},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			if region == "cn-shanghai" {
				return &fakeAlbClient{}, nil
			}
			return fake, nil
		}

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductALB)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, "lsn-2", refs[0].ResourceID) // 云侧缺 LoadBalancerId 时回退纯监听形态
	})
}

func TestCertAdapterALBBindResource(t *testing.T) {
	t.Run("跨地域定位监听并替换默认证书保留扩展证书", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		hangzhou := &fakeAlbClient{}
		shanghai := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-target", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-9"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-target": {{
					{CertificateId: "old-cn-hangzhou", IsDefault: true, CertificateType: "Server"},
					{CertificateId: "ext-cn-hangzhou", IsDefault: false, CertificateType: "Server"},
				}},
			},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			if region == "cn-shanghai" {
				return shanghai, nil
			}
			return hangzhou, nil
		}

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductALB, "lsn-target", "new-cn-hangzhou")
		require.NoError(t, err)

		require.Len(t, shanghai.updateReqs, 1)
		assert.Empty(t, hangzhou.updateReqs)
		req := shanghai.updateReqs[0]
		assert.Equal(t, "lsn-target", req.ListenerId)
		require.NotNil(t, req.Certificates)
		require.Len(t, *req.Certificates, 2)
		assert.Equal(t, "new-cn-hangzhou", (*req.Certificates)[0].CertificateId) // 新证书为默认
		assert.Equal(t, "ext-cn-hangzhou", (*req.Certificates)[1].CertificateId) // 扩展证书保留
	})

	t.Run("复合资源ID解析出监听ID并附实例过滤", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-target", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-9"},
				{ListenerId: "lsn-other", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-8"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-target": {{{CertificateId: "old-cn-hangzhou", IsDefault: true, CertificateType: "Server"}}},
			},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductALB, "alb-9/lsn-target", "new-cn-hangzhou")
		require.NoError(t, err)

		require.Len(t, fake.updateReqs, 1)
		assert.Equal(t, "lsn-target", fake.updateReqs[0].ListenerId) // 复合形态拆出监听 ID
	})

	t.Run("HTTP监听显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-http", ListenerProtocol: "HTTP"},
			},
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductALB, "lsn-http", "new")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP")
	})

	t.Run("监听不存在显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
			return &fakeAlbClient{}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductALB, "lsn-missing", "new")
		require.Error(t, err)
	})

	t.Run("更新限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeAlbClient{
			listeners: []alb.Listener{
				{ListenerId: "lsn-1", ListenerProtocol: "HTTPS"},
			},
			certPages: map[string][][]alb.CertificateModel{
				"lsn-1": {{{CertificateId: "old-cn-hangzhou", IsDefault: true, CertificateType: "Server"}}},
			},
			updateErr: errFakeThrottling,
		}
		adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductALB, "lsn-1", "new-cn-hangzhou")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

// ==================== NLB ====================

// fakeNlbClient NLB SDK fake
type fakeNlbClient struct {
	listeners  []nlb.ListenerInfo
	listErr    error
	updateErr  error
	updateReqs []*nlb.UpdateListenerAttributeRequest
	listCalls  int
}

func (f *fakeNlbClient) ListListeners(request *nlb.ListListenersRequest) (*nlb.ListListenersResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	resp := nlb.CreateListListenersResponse()
	resp.Success = true
	for _, lsn := range f.listeners {
		if request.ListenerIds != nil && len(*request.ListenerIds) > 0 && !certContainsString(*request.ListenerIds, lsn.ListenerId) {
			continue
		}
		if request.LoadBalancerIds != nil && len(*request.LoadBalancerIds) > 0 && !certContainsString(*request.LoadBalancerIds, lsn.LoadBalancerId) {
			continue
		}
		resp.Listeners = append(resp.Listeners, lsn)
	}
	resp.TotalCount = len(resp.Listeners)
	return resp, nil
}

func (f *fakeNlbClient) UpdateListenerAttribute(request *nlb.UpdateListenerAttributeRequest) (*nlb.UpdateListenerAttributeResponse, error) {
	f.updateReqs = append(f.updateReqs, request)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	resp := nlb.CreateUpdateListenerAttributeResponse()
	resp.Success = true
	return resp, nil
}

func TestCertAdapterNLBListReferences(t *testing.T) {
	t.Run("遍历监听内联证书ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeNlbClient{
			listeners: []nlb.ListenerInfo{
				{ListenerId: "lsn-tls-1", ListenerProtocol: "TLS", LoadBalancerId: "nlb-1", CertificateIds: []string{"111-cn-hangzhou", "222-cn-hangzhou"}},
				{ListenerId: "lsn-tcp-1", ListenerProtocol: "TCP"},
			},
		}
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) {
			if region == "cn-shanghai" {
				return &fakeNlbClient{listCalls: 0}, nil
			}
			return fake, nil
		}

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductNLB)
		require.NoError(t, err)
		require.Len(t, refs, 2)

		assert.Equal(t, "nlb-1/lsn-tls-1", refs[0].ResourceID)
		assert.Equal(t, "111-cn-hangzhou", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "aliyun", refs[0].Cloud)
		assert.Equal(t, CertProductNLB, refs[0].Product)
		assert.Equal(t, "aliyun-main", refs[0].AccountKey)
		assert.Equal(t, "222-cn-hangzhou", refs[1].ReferencedCloudCertID)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) {
			return &fakeNlbClient{listErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductNLB)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

func TestParseLBScopedResourceID(t *testing.T) {
	lb, lsn := parseLBScopedResourceID("alb-9/lsn-target")
	assert.Equal(t, "alb-9", lb)
	assert.Equal(t, "lsn-target", lsn)

	// 存量纯监听形态：整串视为监听 ID（升级前创建的变更单兼容）
	lb, lsn = parseLBScopedResourceID("lsn-target")
	assert.Empty(t, lb)
	assert.Equal(t, "lsn-target", lsn)
}

func TestCertAdapterNLBBindResource(t *testing.T) {
	t.Run("复合资源ID解析出监听ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeNlbClient{
			listeners: []nlb.ListenerInfo{
				{ListenerId: "lsn-tls-1", ListenerProtocol: "TLS", LoadBalancerId: "nlb-1", CertificateIds: []string{"old-cn-hangzhou", "ext-cn-hangzhou"}},
			},
		}
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductNLB, "nlb-1/lsn-tls-1", "new-cn-hangzhou")
		require.NoError(t, err)

		require.Len(t, fake.updateReqs, 1)
		assert.Equal(t, "lsn-tls-1", fake.updateReqs[0].ListenerId)
	})

	t.Run("替换默认证书保留扩展证书", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeNlbClient{
			listeners: []nlb.ListenerInfo{
				{ListenerId: "lsn-tls-1", ListenerProtocol: "TLS", CertificateIds: []string{"old-cn-hangzhou", "ext-cn-hangzhou"}},
			},
		}
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) {
			if region == "cn-shanghai" {
				return &fakeNlbClient{}, nil
			}
			return fake, nil
		}

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductNLB, "lsn-tls-1", "new-cn-hangzhou")
		require.NoError(t, err)

		require.Len(t, fake.updateReqs, 1)
		req := fake.updateReqs[0]
		assert.Equal(t, "lsn-tls-1", req.ListenerId)
		require.NotNil(t, req.CertificateIds)
		require.Len(t, *req.CertificateIds, 2)
		assert.Equal(t, "new-cn-hangzhou", (*req.CertificateIds)[0]) // 默认位换新
		assert.Equal(t, "ext-cn-hangzhou", (*req.CertificateIds)[1]) // 扩展保留
	})

	t.Run("非TLS监听显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeNlbClient{
			listeners: []nlb.ListenerInfo{
				{ListenerId: "lsn-tcp-1", ListenerProtocol: "TCP"},
			},
		}
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductNLB, "lsn-tcp-1", "new")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS")
	})

	t.Run("更新限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeNlbClient{
			listeners: []nlb.ListenerInfo{
				{ListenerId: "lsn-tls-1", ListenerProtocol: "TLS", CertificateIds: []string{"old-cn-hangzhou"}},
			},
			updateErr: errFakeThrottling,
		}
		adapter.newNlbClient = func(creds *domain.CloudAccount, region string) (nlbCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductNLB, "lsn-tls-1", "new-cn-hangzhou")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

func TestCertAdapterALBListReferencesServedDomains(t *testing.T) {
	adapter := newTestCertAdapter(t)
	fake := &fakeAlbClient{
		listeners: []alb.Listener{
			{ListenerId: "lsn-1", ListenerProtocol: "HTTPS", LoadBalancerId: "alb-1"},
		},
		certPages: map[string][][]alb.CertificateModel{
			"lsn-1": {{{CertificateId: "111-cn-hangzhou", IsDefault: true, CertificateType: "Server"}}},
		},
		rulesByListener: map[string][]alb.Rule{
			"lsn-1": {
				{ListenerId: "lsn-1", RuleConditions: []alb.Condition{
					{Type: "Host", HostConfig: alb.HostConfig{Values: []string{"www.example.com", "api.example.com"}}},
					{Type: "Path", PathConfig: alb.PathConfig{Values: []string{"/*"}}}, // 非 Host 条件应忽略
				}},
			},
		},
	}
	adapter.newAlbClient = func(creds *domain.CloudAccount, region string) (albCertAPI, error) {
		if region == "cn-shanghai" {
			return &fakeAlbClient{}, nil
		}
		return fake, nil
	}

	refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductALB)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "alb-1/lsn-1", refs[0].ResourceID)
	assert.ElementsMatch(t, []string{"www.example.com", "api.example.com"}, refs[0].ServedDomains)
}

// ==================== 托管实例标签提取（cert-alb-ingress-managed） ====================

func TestManagedRefFromTags(t *testing.T) {
	// 托管实例：albconfig + 集群标签齐全
	ref, ok := managedRefFromTags(map[string]string{
		"ingress.k8s.alibaba/albconfig": "kube-system/jlc-uat-albconfig-internet",
		"ack.aliyun.com":                "cf9d2e9f8e146450a8fedcd7c9d9f972b",
		"business":                      "cpp",
	})
	assert.True(t, ok)
	assert.Equal(t, "cf9d2e9f8e146450a8fedcd7c9d9f972b", ref.ClusterID)
	assert.Equal(t, "cf9d2e9f8e146450a8fedcd7c9d9f972b/kube-system/jlc-uat-albconfig-internet", ref.Owner)

	// 无集群标签退化：owner 保留 ns/name
	ref, ok = managedRefFromTags(map[string]string{"ingress.k8s.alibaba/albconfig": "kube-system/x"})
	assert.True(t, ok)
	assert.Equal(t, "", ref.ClusterID)
	assert.Equal(t, "kube-system/x", ref.Owner)

	// 非托管实例：无 albconfig 标签
	_, ok = managedRefFromTags(map[string]string{"business": "cpp"})
	assert.False(t, ok)
}

func TestListALBManagedInstances(t *testing.T) {
	a := NewCertAdapter(nil)
	a.newAlbClient = func(_ *domain.CloudAccount, _ string) (albCertAPI, error) {
		return &fakeAlbClient{
			tagResources: []alb.TagResource{
				{ResourceId: "alb-main-1", TagKey: "ingress.k8s.alibaba/albconfig", TagValue: "kube-system/alb-conf-main"},
				{ResourceId: "alb-main-1", TagKey: "ack.aliyun.com", TagValue: "cluster-abc"},
				{ResourceId: "alb-other-2", TagKey: "business", TagValue: "cpp"}, // 非托管
			},
		}, nil
	}
	got, err := a.ListALBManagedInstances(context.Background(), &domain.CloudAccount{Regions: []string{"cn-shenzhen"}},
		[]string{"alb-main-1", "alb-other-2"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "cluster-abc/kube-system/alb-conf-main", got["alb-main-1"].Owner)
	_, present := got["alb-other-2"]
	assert.False(t, present, "非托管实例不入映射")
}
