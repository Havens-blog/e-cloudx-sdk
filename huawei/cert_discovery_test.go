package huawei

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	elbmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	wafmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 测试公共设施 ====================

// newTestCertDiscoveryAdapter 构造可注入 fake SDK 客户端的发现适配器（限流器放宽避免测试排队）
func newTestCertDiscoveryAdapter(t *testing.T) *CertDiscoveryAdapter {
	t.Helper()
	adapter := NewCertDiscoveryAdapter(elog.DefaultLogger)
	adapter.rateLimiter = ratelimit.NewRateLimiter(5000)
	adapter.listPageSize = 2
	return adapter
}

// certTestInt32 int32 指针构造（避免与既有测试辅助重名）
func certTestInt32(v int32) *int32 { return &v }

func certTestCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "huawei-main",
		Provider:        domain.CloudProviderHuawei,
		AccessKeyID:     "test-ak",
		AccessKeySecret: "test-sk",
		Regions:         []string{"cn-north-4", "cn-east-3"},
	}
}

var (
	errCertThrottled = errors.New("APIGW.0301: 请求过于频繁, throttled")
	errCertNotFound  = errors.New("SCM.0021: certificate not exist / 证书不存在")
)

// fakeCDNCertClient CDN fake（分页返回预置 HTTPS 详情）
type fakeCDNCertClient struct {
	pages [][]cdnmodel.HttpsDetail
	calls int
	err   error
}

func (f *fakeCDNCertClient) ShowCertificatesHttpsInfo(request *cdnmodel.ShowCertificatesHttpsInfoRequest) (*cdnmodel.ShowCertificatesHttpsInfoResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	page := 0
	if request.PageNumber != nil {
		page = int(*request.PageNumber) - 1
	}
	if page < 0 || page >= len(f.pages) {
		return &cdnmodel.ShowCertificatesHttpsInfoResponse{}, nil
	}
	details := f.pages[page]
	return &cdnmodel.ShowCertificatesHttpsInfoResponse{Https: &details}, nil
}

// fakeWAFClient WAF fake（列表 + 详情按 host_id 分发）
type fakeWAFClient struct {
	hosts     []wafmodel.CloudWafHostItem
	shows     map[string]*wafmodel.ShowHostResponse
	showErrs  map[string]error
	listErr   error
	listCalls int
	showCalls int
}

func (f *fakeWAFClient) ListHost(request *wafmodel.ListHostRequest) (*wafmodel.ListHostResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	size := 50
	if request.Pagesize != nil {
		size = int(*request.Pagesize)
	}
	offset := 0
	if request.Page != nil {
		offset = (int(*request.Page) - 1) * size
	}
	if offset >= len(f.hosts) {
		return &wafmodel.ListHostResponse{}, nil
	}
	end := offset + size
	if end > len(f.hosts) {
		end = len(f.hosts)
	}
	items := make([]wafmodel.CloudWafHostItem, end-offset)
	copy(items, f.hosts[offset:end])
	return &wafmodel.ListHostResponse{Items: &items}, nil
}

func (f *fakeWAFClient) ShowHost(request *wafmodel.ShowHostRequest) (*wafmodel.ShowHostResponse, error) {
	f.showCalls++
	if err, ok := f.showErrs[request.InstanceId]; ok {
		return nil, err
	}
	if show, ok := f.shows[request.InstanceId]; ok {
		return show, nil
	}
	return &wafmodel.ShowHostResponse{}, nil
}

// fakeELBPage ELB 预置页（marker 链式分页，跨地域可重放）
type fakeELBPage struct {
	marker    string // 请求该页携带的 marker（"" 为首页）
	listeners []elbmodel.Listener
	next      string // 下一页 marker（"" = 结束）
}

// fakeELBClient ELB fake（按请求 marker 匹配预置页；地域无关可重放）
type fakeELBClient struct {
	pages []fakeELBPage
	got   []string
	err   error
}

func (f *fakeELBClient) ListListeners(request *elbmodel.ListListenersRequest) (*elbmodel.ListListenersResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	marker := ""
	if request.Marker != nil {
		marker = *request.Marker
	}
	f.got = append(f.got, marker)
	for _, page := range f.pages {
		if page.marker != marker {
			continue
		}
		listeners := page.listeners
		var next *string
		if page.next != "" {
			next = stringPtr(page.next)
		}
		return &elbmodel.ListListenersResponse{
			Listeners: &listeners,
			PageInfo:  &elbmodel.PageInfo{NextMarker: next},
		}, nil
	}
	return &elbmodel.ListListenersResponse{}, nil
}

// ==================== 只读硬约束：三写方法返回哨兵且不触达云侧 ====================

func TestCertDiscoveryWritesReturnSentinel(t *testing.T) {
	t.Run("三写方法一律返回ErrDiscoveryOnly且不构造云客户端", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		// 工厂注入即失败哨兵：写方法若触达客户端构造会被捕获
		failFactory := func(name string) {
			t.Helper()
			t.Fatalf("discovery-only 写方法不得构造 %s 云客户端", name)
		}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			failFactory("CDN")
			return nil, nil
		}
		adapter.newWafClient = func(creds *domain.CloudAccount, region string) (wafCertAPI, error) {
			failFactory("WAF")
			return nil, nil
		}
		adapter.newElbClient = func(creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			failFactory("ELB")
			return nil, nil
		}
		adapter.newScmClient = func(creds *domain.CloudAccount) (scmCertAPI, error) {
			failFactory("SCM")
			return nil, nil
		}

		ctx := context.Background()
		creds := certTestCreds()
		for _, product := range []string{CertProductCDN, CertProductWAF, CertProductALB, CertProductNLB} {
			_, err := adapter.UploadCert(ctx, creds, product, "n", "pem", "key")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
			assert.ErrorIs(t, err, cloudx.ErrDiscoveryOnly, product)
			assert.NotErrorIs(t, err, cloudx.ErrCloudRateLimited, product)

			err = adapter.BindResource(ctx, creds, product, "res-1", "scs-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)

			err = adapter.CleanupOrphan(ctx, creds, "scs-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
		}
	})
}

// ==================== ListReferences: CDN ====================

func TestCertDiscoveryListCDNReferences(t *testing.T) {
	t.Run("分页遍历并产出证书名形态引用", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t) // pageSize=2
		fake := &fakeCDNCertClient{pages: [][]cdnmodel.HttpsDetail{
			{
				{DomainName: stringPtr("a.example.com"), CertName: stringPtr("cert-a"), HttpsStatus: certTestInt32(2)},
				{DomainName: stringPtr("b.example.com"), CertName: stringPtr("cert-b"), HttpsStatus: certTestInt32(1)},
			},
			{
				{DomainName: stringPtr("c.example.com"), CertName: stringPtr("cert-c"), HttpsStatus: certTestInt32(2)},
			},
		}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 3)
		assert.Equal(t, 2, fake.calls) // 翻页两次
		for _, ref := range refs {
			assert.Equal(t, "huawei", ref.Cloud)
			assert.Equal(t, CertProductCDN, ref.Product)
			assert.Equal(t, "huawei-main", ref.AccountKey)
			assert.NotEmpty(t, ref.ReferencedCloudCertID)
		}
		assert.Equal(t, "a.example.com", refs[0].ResourceID)
		assert.Equal(t, "cert-a", refs[0].ReferencedCloudCertID)
	})

	t.Run("HTTPS未启用与空证书名跳过", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeCDNCertClient{pages: [][]cdnmodel.HttpsDetail{
			{
				{DomainName: stringPtr("off.example.com"), CertName: stringPtr("cert-off"), HttpsStatus: certTestInt32(0)},
				{DomainName: stringPtr("nocert.example.com"), CertName: nil, HttpsStatus: certTestInt32(2)},
			},
		}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return &fakeCDNCertClient{err: errCertThrottled}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductCDN)
		require.Error(t, err)
	})
}

// ==================== ListReferences: WAF ====================

func TestCertDiscoveryListWAFReferences(t *testing.T) {
	t.Run("按地域遍历防护域名并经详情展开证书引用", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t) // pageSize=2，hosts 3 个 → 每地域翻页两次
		fake := &fakeWAFClient{
			hosts: []wafmodel.CloudWafHostItem{
				{Id: stringPtr("host-1"), Hostname: stringPtr("waf-a.example.com")},
				{Id: stringPtr("host-2"), Hostname: stringPtr("waf-b.example.com")},
				{Id: stringPtr("host-3"), Hostname: stringPtr("waf-c.example.com")},
			},
			shows: map[string]*wafmodel.ShowHostResponse{
				"host-1": {Hostname: stringPtr("waf-a.example.com"), Certificateid: stringPtr("scs-1631")},
				"host-2": {Hostname: stringPtr("waf-b.example.com"), Certificateid: nil},
			},
			showErrs: map[string]error{
				"host-3": errors.New("WAF.2001: internal error"),
			},
		}
		adapter.newWafClient = func(creds *domain.CloudAccount, region string) (wafCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductWAF)
		require.NoError(t, err)
		// 两个地域 × 每地域 1 个带证书域名 = 2 条引用；host-2 无证书、host-3 详情失败跳过
		require.Len(t, refs, 2)
		ref := refs[0]
		assert.Equal(t, "huawei", ref.Cloud)
		assert.Equal(t, CertProductWAF, ref.Product)
		assert.Equal(t, "waf-a.example.com", ref.ResourceID)
		assert.Equal(t, "scs-1631", ref.ReferencedCloudCertID)
		assert.Equal(t, "huawei-main", ref.AccountKey)
		// 分页断言：3 hosts / pageSize=2 → 每地域 2 次 ListHost
		assert.Equal(t, 4, fake.listCalls)
		assert.Equal(t, 6, fake.showCalls)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newWafClient = func(creds *domain.CloudAccount, region string) (wafCertAPI, error) {
			return &fakeWAFClient{listErr: errCertThrottled}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductWAF)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

// ==================== ListReferences: ELB ====================

func TestCertDiscoveryListELBReferences(t *testing.T) {
	httpsListener := elbmodel.Listener{
		Id:                     "listener-https",
		Protocol:               "HTTPS",
		ProtocolPort:           443,
		DefaultTlsContainerRef: "cert-main",
		SniContainerRefs:       []string{"cert-sni-1", "cert-main"}, // 重复主证书应去重
		Loadbalancers:          []elbmodel.LoadBalancerRef{{Id: stringPtr("lb-1")}},
	}
	terminatedListener := elbmodel.Listener{
		Id:                     "listener-term",
		Protocol:               "TERMINATED_HTTPS",
		DefaultTlsContainerRef: "cert-shared",
		Loadbalancers:          []elbmodel.LoadBalancerRef{{Id: stringPtr("lb-1")}},
	}
	tlsListener := elbmodel.Listener{
		Id:                     "listener-tls",
		Protocol:               "TLS",
		DefaultTlsContainerRef: "cert-l4",
		Loadbalancers:          []elbmodel.LoadBalancerRef{{Id: stringPtr("lb-2")}},
	}
	plainListener := elbmodel.Listener{
		Id:            "listener-http",
		Protocol:      "HTTP",
		Loadbalancers: []elbmodel.LoadBalancerRef{{Id: stringPtr("lb-1")}},
	}

	t.Run("alb产品返回L7监听证书并按协议过滤", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeELBClient{pages: []fakeELBPage{
			{marker: "", listeners: []elbmodel.Listener{httpsListener, terminatedListener}, next: "m1"},
			{marker: "m1", listeners: []elbmodel.Listener{tlsListener, plainListener}},
		}}
		adapter.newElbClient = func(creds *domain.CloudAccount, region string) (elbCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err)
		// 每地域：https(主证书+sni去重后1条=2条) + terminated(1条) = 3 条；两地域共 6 条
		require.Len(t, refs, 6)
		first := refs[0]
		assert.Equal(t, "huawei", first.Cloud)
		assert.Equal(t, CertProductALB, first.Product)
		assert.Equal(t, "lb-1/listener-https", first.ResourceID)
		assert.Equal(t, "cert-main", first.ReferencedCloudCertID)
		assert.Equal(t, "huawei-main", first.AccountKey)
	})

	t.Run("nlb产品返回TLS监听证书", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeELBClient{pages: []fakeELBPage{
			{marker: "", listeners: []elbmodel.Listener{tlsListener}},
		}}
		adapter.newElbClient = func(creds *domain.CloudAccount, region string) (elbCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductNLB)
		require.NoError(t, err)
		require.Len(t, refs, 2) // 两地域各 1 条
		assert.Equal(t, CertProductNLB, refs[0].Product)
		assert.Equal(t, "lb-2/listener-tls", refs[0].ResourceID)
		assert.Equal(t, "cert-l4", refs[0].ReferencedCloudCertID)
	})

	t.Run("无证书协议与空负载均衡引用跳过", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeELBClient{pages: []fakeELBPage{
			{marker: "", listeners: []elbmodel.Listener{plainListener, {Id: "listener-orphan", Protocol: "HTTPS", DefaultTlsContainerRef: "cert-x"}}},
		}}
		adapter.newElbClient = func(creds *domain.CloudAccount, region string) (elbCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err)
		assert.Empty(t, refs) // HTTP 无证书；HTTPS 无所属 LB 无法定位资源
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newElbClient = func(creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return &fakeELBClient{err: errCertThrottled}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductALB)
		require.Error(t, err)
	})
}

// ==================== GetCert（PEM 通道不支持降级标记） ====================

func TestCertDiscoveryGetCert(t *testing.T) {
	t.Run("返回不支持PEM降级标记哨兵且不触达云侧", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		// PEM 通道不支持判定先于客户端构造：注入失败工厂证明不发起 SCM 调用
		adapter.newScmClient = func(creds *domain.CloudAccount) (scmCertAPI, error) {
			t.Fatalf("PEM 不支持分支不得构造 SCM 云客户端")
			return nil, nil
		}
		info, err := adapter.GetCert(context.Background(), certTestCreds(), "scs-1631")
		require.Error(t, err)
		require.Empty(t, info.Fingerprint)
		assert.False(t, info.Exists)
		// 可被上层识别为降级标记（非通用失败）
		assert.ErrorIs(t, err, ErrCertPEMUnsupported)
		assert.ErrorIs(t, err, cloudx.ErrCertPEMUnsupported)
		assert.NotErrorIs(t, err, ErrCloudRateLimited)
		assert.NotErrorIs(t, err, ErrDiscoveryOnly)
		// 错误文案为静态文案，不携带云响应片段
		assert.Contains(t, err.Error(), "no pem export")
	})

	t.Run("空证书ID与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.GetCert(context.Background(), certTestCreds(), " ")
		require.Error(t, err)
		_, err = adapter.GetCert(context.Background(), nil, "scs-1631")
		require.Error(t, err)
	})
}

// ==================== 分发与产品集合 ====================

func TestCertDiscoveryDispatch(t *testing.T) {
	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		for _, product := range []string{"dcdn", "clb", "crd", ""} {
			_, err := adapter.ListReferences(context.Background(), certTestCreds(), product)
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrCertProductNotSupported, product)
		}
	})

	t.Run("产品集合覆盖华为云四产品且枚举对齐schema", func(t *testing.T) {
		for _, product := range []string{CertProductCDN, CertProductWAF, CertProductALB, CertProductNLB} {
			assert.True(t, certSupportedProducts[product], product)
		}
		// schema.sql cert_references.product 枚举约束
		for product := range certSupportedProducts {
			assert.Contains(t, []string{"cdn", "dcdn", "waf", "alb", "clb", "nlb", "crd"}, product)
		}
		assert.False(t, certSupportedProducts["dcdn"])
	})
}

// ==================== 辅助函数 ====================

func TestElbListenerProduct(t *testing.T) {
	assert.Equal(t, CertProductALB, elbListenerProduct("HTTPS"))
	assert.Equal(t, CertProductALB, elbListenerProduct("TERMINATED_HTTPS"))
	assert.Equal(t, CertProductNLB, elbListenerProduct("TLS"))
	assert.Empty(t, elbListenerProduct("HTTP"))
	assert.Empty(t, elbListenerProduct("TCP"))
	assert.Empty(t, elbListenerProduct(""))
}

func TestNextListMarker(t *testing.T) {
	marker := "m-1"
	// 未满页：终止翻页
	assert.Nil(t, nextListMarker(&elbmodel.PageInfo{NextMarker: &marker}, 1, 2))
	// 满页且有 NextMarker：继续
	assert.Equal(t, &marker, nextListMarker(&elbmodel.PageInfo{NextMarker: &marker}, 2, 2))
	// PageInfo 缺失 / NextMarker 为空：终止
	empty := ""
	assert.Nil(t, nextListMarker(nil, 2, 2))
	assert.Nil(t, nextListMarker(&elbmodel.PageInfo{}, 2, 2))
	assert.Nil(t, nextListMarker(&elbmodel.PageInfo{NextMarker: &empty}, 2, 2))
}

func TestCertDiscoveryHelpers(t *testing.T) {
	// 指纹归一化
	assert.Equal(t, "abcdef01", normalizeCloudCertFingerprint("AB:CD:EF:01"))
	assert.Equal(t, "abcdef01", normalizeCloudCertFingerprint("ab:cd:ef:01"))
	assert.Empty(t, normalizeCloudCertFingerprint(""))
	assert.Equal(t, "plain", normalizeCloudCertFingerprint("PLAIN"))

	// 时间解析
	got, ok := parseCloudCertTime("2027-01-02T08:00:00Z")
	require.True(t, ok)
	assert.Equal(t, 2027, got.Year())
	got, ok = parseCloudCertTime("2027-01-02 15:04:05")
	require.True(t, ok)
	assert.Equal(t, 2027, got.Year())
	_, ok = parseCloudCertTime("")
	assert.False(t, ok)
	_, ok = parseCloudCertTime("not-a-date")
	assert.False(t, ok)

	// 地域回退
	creds := certTestCreds()
	assert.Equal(t, []string{"cn-north-4", "cn-east-3"}, certCredsRegions(creds))
	creds.Regions = nil
	assert.Equal(t, []string{"cn-north-4"}, certCredsRegions(creds))
	assert.Equal(t, "cn-north-4", certCredsRegion(nil))
}

func TestIsCertDiscoveryNotFound(t *testing.T) {
	assert.True(t, isCertDiscoveryNotFound(errCertNotFound))
	sdkErr := &sdkerr.ServiceResponseError{StatusCode: 404, ErrorCode: "APIG.1001", ErrorMessage: "boom"}
	assert.True(t, isCertDiscoveryNotFound(sdkErr))
	sdkNotFound := &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "SCM.NotFound", ErrorMessage: "x"}
	assert.True(t, isCertDiscoveryNotFound(sdkNotFound))
	assert.False(t, isCertDiscoveryNotFound(errCertThrottled))
	assert.False(t, isCertDiscoveryNotFound(nil))
}

// ==================== 真实客户端工厂边界 ====================
// 注意：华为云 SDK 的 SafeBuild 会经 IAM 自动解析 domain/project id（网络调用），
// 与既有华为云适配器（cdn.go/waf.go/lb.go）同行为，无法用假凭证离线构建真实客户端；
// 此处仅验证不触网的地域校验失败路径与适配器边界分支。

func TestCertDiscoveryRealClientFactories(t *testing.T) {
	creds := certTestCreds()
	adapter := NewCertDiscoveryAdapter(elog.DefaultLogger)

	// 无效地域在 IAM 解析前快速失败（不触网）
	_, err := adapter.newWafClient(creds, "not-a-region")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WAF地域")
	_, err = adapter.newElbClient(creds, "not-a-region")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ELB地域")

	// 边界：空限流器/零分页回退
	adapter.rateLimiter = nil
	assert.NoError(t, adapter.waitRateLimit(context.Background()))
	adapter.listPageSize = 0
	assert.Equal(t, certDiscoveryPageSize, adapter.certPageSize())
	assert.NotNil(t, NewCertDiscoveryAdapter(nil))
}
