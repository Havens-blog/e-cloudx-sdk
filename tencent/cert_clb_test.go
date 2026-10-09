package tencent

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// fakeClbClient CLB SDK fake（lbs 按页弹出模拟分页；监听器按实例一次性返回）
type fakeClbClient struct {
	lbsErr        error
	listenersErr  error
	modifyErr     error
	lbsPages      [][]*clb.LoadBalancer
	listenersByLB map[string][]*clb.Listener

	lbsReqs       []*clb.DescribeLoadBalancersRequest
	listenersReqs []*clb.DescribeListenersRequest
	modifyReqs    []*clb.ModifyListenerRequest
}

func (f *fakeClbClient) DescribeLoadBalancers(request *clb.DescribeLoadBalancersRequest) (*clb.DescribeLoadBalancersResponse, error) {
	f.lbsReqs = append(f.lbsReqs, request)
	if f.lbsErr != nil {
		return nil, f.lbsErr
	}
	response := clb.NewDescribeLoadBalancersResponse()
	response.Response = &clb.DescribeLoadBalancersResponseParams{}
	if len(f.lbsPages) > 0 {
		response.Response.LoadBalancerSet = f.lbsPages[0]
		f.lbsPages = f.lbsPages[1:]
	}
	return response, nil
}

func (f *fakeClbClient) DescribeListeners(request *clb.DescribeListenersRequest) (*clb.DescribeListenersResponse, error) {
	f.listenersReqs = append(f.listenersReqs, request)
	if f.listenersErr != nil {
		return nil, f.listenersErr
	}
	response := clb.NewDescribeListenersResponse()
	response.Response = &clb.DescribeListenersResponseParams{}
	if listeners := f.listenersByLB[*request.LoadBalancerId]; listeners != nil {
		filtered := make([]*clb.Listener, 0, len(listeners))
		for _, listener := range listeners {
			// 模拟按 ListenerIds 过滤语义
			if len(request.ListenerIds) == 0 {
				filtered = append(filtered, listener)
				continue
			}
			for _, id := range request.ListenerIds {
				if listener.ListenerId != nil && *listener.ListenerId == *id {
					filtered = append(filtered, listener)
				}
			}
		}
		response.Response.Listeners = filtered
	}
	return response, nil
}

func (f *fakeClbClient) ModifyListener(request *clb.ModifyListenerRequest) (*clb.ModifyListenerResponse, error) {
	f.modifyReqs = append(f.modifyReqs, request)
	if f.modifyErr != nil {
		return nil, f.modifyErr
	}
	return clb.NewModifyListenerResponse(), nil
}

// clbCertLoadBalancer 构造负载均衡实例
func clbCertLoadBalancer(lbID string) *clb.LoadBalancer {
	return &clb.LoadBalancer{LoadBalancerId: common.StringPtr(lbID)}
}

// clbCertListener 构造监听器（certID 为空表示无证书监听，如 HTTP）
func clbCertListener(listenerID, protocol, certID string, extCertIDs ...string) *clb.Listener {
	listener := &clb.Listener{
		ListenerId: common.StringPtr(listenerID),
		Protocol:   common.StringPtr(protocol),
	}
	if certID != "" {
		listener.Certificate = &clb.CertificateOutput{
			SSLMode: common.StringPtr("UNIDIRECTIONAL"),
			CertId:  common.StringPtr(certID),
		}
		if len(extCertIDs) > 0 {
			listener.Certificate.ExtCertIds = common.StringPtrs(extCertIDs)
		}
	}
	return listener
}

// injectClbFakes 按地域注入 fake 客户端工厂
func injectClbFakes(adapter *CertAdapter, fakes map[string]*fakeClbClient) {
	adapter.newClbClient = func(creds *domain.CloudAccount, region string) (clbCertAPI, error) {
		fake, ok := fakes[region]
		if !ok {
			return &fakeClbClient{}, nil
		}
		return fake, nil
	}
}

// ==================== ListReferences ====================

func TestCertAdapterListCLBReferences(t *testing.T) {
	t.Run("按地域遍历实例与监听器证书引用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		gz := &fakeClbClient{
			lbsPages: [][]*clb.LoadBalancer{
				{clbCertLoadBalancer("lb-1"), clbCertLoadBalancer("lb-2")},
				{clbCertLoadBalancer("lb-3")},
			},
			listenersByLB: map[string][]*clb.Listener{
				// 主证书 + SNI 扩展证书 + 无证书 HTTP 监听
				"lb-1": {
					clbCertListener("lbl-1", "HTTPS", "111", "222"),
					clbCertListener("lbl-2", "HTTP", ""),
				},
				"lb-2": {clbCertListener("lbl-3", "QUIC", "333")},
				"lb-3": {clbCertListener("lbl-4", "HTTPS", "444")},
			},
		}
		sh := &fakeClbClient{
			lbsPages: [][]*clb.LoadBalancer{{clbCertLoadBalancer("lb-sh-1")}},
			listenersByLB: map[string][]*clb.Listener{
				"lb-sh-1": {clbCertListener("lbl-sh-1", "HTTPS", "555")},
			},
		}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz, "ap-shanghai": sh})

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCLB)
		require.NoError(t, err)
		require.Len(t, refs, 5)

		assert.Equal(t, "tencent", refs[0].Cloud)
		assert.Equal(t, CertProductCLB, refs[0].Product)
		assert.Equal(t, "lb-1/lbl-1", refs[0].ResourceID)
		assert.Equal(t, "111", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "tencent-main", refs[0].AccountKey)
		// 扩展证书独立成引用（同资源 ID）
		assert.Equal(t, "lb-1/lbl-1", refs[1].ResourceID)
		assert.Equal(t, "222", refs[1].ReferencedCloudCertID)
		assert.Equal(t, "lb-2/lbl-3", refs[2].ResourceID)
		assert.Equal(t, "lb-3/lbl-4", refs[3].ResourceID)
		assert.Equal(t, "lb-sh-1/lbl-sh-1", refs[4].ResourceID)

		// 实例页满触发 Offset 递增
		require.Len(t, gz.lbsReqs, 2)
		assert.Equal(t, int64(0), *gz.lbsReqs[0].Offset)
		assert.Equal(t, int64(2), *gz.lbsReqs[1].Offset)
	})

	t.Run("单实例监听器列举失败跳过不中断", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		gz := &fakeClbClient{
			lbsPages: [][]*clb.LoadBalancer{{clbCertLoadBalancer("lb-1"), clbCertLoadBalancer("lb-2")}},
			listenersByLB: map[string][]*clb.Listener{
				"lb-2": {clbCertListener("lbl-3", "HTTPS", "333")},
			},
		}
		gz.listenersErr = errFakeDenied
		// lb-1 配置了监听器但列举报错（fake 全局报错，仅校验 lb-2 空监听 + 报错路径跳过）
		gz.listenersByLB["lb-1"] = []*clb.Listener{clbCertListener("lbl-1", "HTTPS", "111")}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCLB)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("实例列举限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		injectClbFakes(adapter, map[string]*fakeClbClient{
			"ap-guangzhou": {lbsErr: errFakeThrottling},
		})
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCLB)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductCLB)
		require.Error(t, err)
	})
}

// ==================== BindResource ====================

func TestCertAdapterBindCLB(t *testing.T) {
	t.Run("单证书监听器替换主证书并保留认证模式", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		listener := clbCertListener("lbl-1", "HTTPS", "111")
		listener.Certificate.CertCaId = common.StringPtr("ca-1")
		listener.Certificate.SSLVerifyClient = common.StringPtr("on")
		gz := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{
			"lb-1": {listener},
		}}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-1", "999")
		require.NoError(t, err)

		require.Len(t, gz.modifyReqs, 1)
		request := gz.modifyReqs[0]
		assert.Equal(t, "lb-1", *request.LoadBalancerId)
		assert.Equal(t, "lbl-1", *request.ListenerId)
		require.NotNil(t, request.Certificate)
		assert.Equal(t, "UNIDIRECTIONAL", *request.Certificate.SSLMode)
		assert.Equal(t, "999", *request.Certificate.CertId)
		assert.Equal(t, "ca-1", *request.Certificate.CertCaId)
		assert.Equal(t, "on", *request.Certificate.SSLVerifyClient)
		assert.Nil(t, request.MultiCertInfo)
	})

	t.Run("SNI多证书监听器整单回写保留扩展与CA", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		listener := clbCertListener("lbl-1", "HTTPS", "111", "222", "333")
		listener.Certificate.CertCaId = common.StringPtr("ca-1")
		gz := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{
			"lb-1": {listener},
		}}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-1", "999")
		require.NoError(t, err)

		require.Len(t, gz.modifyReqs, 1)
		request := gz.modifyReqs[0]
		assert.Nil(t, request.Certificate)
		require.NotNil(t, request.MultiCertInfo)
		assert.Equal(t, "UNIDIRECTIONAL", *request.MultiCertInfo.SSLMode)
		certList := request.MultiCertInfo.CertList
		require.Len(t, certList, 4)
		assert.Equal(t, "999", *certList[0].CertId)
		assert.Equal(t, "222", *certList[1].CertId)
		assert.Equal(t, "333", *certList[2].CertId)
		assert.Equal(t, "ca-1", *certList[3].CertId)
	})

	t.Run("非默认地域监听器按地域定位", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		sh := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{
			"lb-sh-1": {clbCertListener("lbl-sh-1", "HTTPS", "555")},
		}}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-shanghai": sh})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-sh-1/lbl-sh-1", "999")
		require.NoError(t, err)
		require.Len(t, sh.modifyReqs, 1)
	})

	t.Run("监听器无证书配置显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		gz := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{
			"lb-1": {clbCertListener("lbl-1", "HTTP", "")},
		}}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-1", "999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no certificate config")
	})

	t.Run("监听器不存在显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		gz := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{}}
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-404", "999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("非法资源ID/空证书ID/限流显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		gz := &fakeClbClient{listenersByLB: map[string][]*clb.Listener{
			"lb-1": {clbCertListener("lbl-1", "HTTPS", "111")},
		}}
		gz.modifyErr = errFakeThrottling
		injectClbFakes(adapter, map[string]*fakeClbClient{"ap-guangzhou": gz})

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "no-slash", "999")
		require.Error(t, err)

		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-1", "")
		require.Error(t, err)

		err = adapter.BindResource(context.Background(), nil, CertProductCLB, "lb-1/lbl-1", "999")
		require.Error(t, err)

		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductCLB, "lb-1/lbl-1", "999")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}
