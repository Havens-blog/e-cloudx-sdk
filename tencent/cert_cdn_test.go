package tencent

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

// fakeCdnClient CDN SDK fake（pages 依次弹出模拟分页）
type fakeCdnClient struct {
	listErr    error
	updateErr  error
	pages      [][]*tencentcdn.DetailDomain
	listReqs   []*tencentcdn.DescribeDomainsConfigRequest
	updateReqs []*tencentcdn.UpdateDomainConfigRequest
}

func (f *fakeCdnClient) DescribeDomainsConfig(request *tencentcdn.DescribeDomainsConfigRequest) (*tencentcdn.DescribeDomainsConfigResponse, error) {
	f.listReqs = append(f.listReqs, request)
	if f.listErr != nil {
		return nil, f.listErr
	}
	response := tencentcdn.NewDescribeDomainsConfigResponse()
	response.Response = &tencentcdn.DescribeDomainsConfigResponseParams{}
	if len(f.pages) > 0 {
		response.Response.Domains = f.pages[0]
		f.pages = f.pages[1:]
	}
	return response, nil
}

func (f *fakeCdnClient) UpdateDomainConfig(request *tencentcdn.UpdateDomainConfigRequest) (*tencentcdn.UpdateDomainConfigResponse, error) {
	f.updateReqs = append(f.updateReqs, request)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return tencentcdn.NewUpdateDomainConfigResponse(), nil
}

// cdnCertDomain 构造带/不带证书的 CDN 域名配置（certID 为空表示未配置 HTTPS 证书）
func cdnCertDomain(name, certID string) *tencentcdn.DetailDomain {
	detail := &tencentcdn.DetailDomain{Domain: common.StringPtr(name)}
	if certID != "" {
		detail.Https = &tencentcdn.Https{
			CertInfo: &tencentcdn.ServerCert{CertId: common.StringPtr(certID)},
		}
	}
	return detail
}

// ==================== ListReferences ====================

func TestCertAdapterListCDNReferences(t *testing.T) {
	t.Run("分页遍历并跳过无证书域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeCdnClient{pages: [][]*tencentcdn.DetailDomain{
			{cdnCertDomain("a.example.com", "111"), cdnCertDomain("b.example.com", "222")},
			{cdnCertDomain("c.example.com", "333"), cdnCertDomain("d.example.com", "")},
		}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 3)

		assert.Equal(t, "tencent", refs[0].Cloud)
		assert.Equal(t, CertProductCDN, refs[0].Product)
		assert.Equal(t, "a.example.com", refs[0].ResourceID)
		assert.Equal(t, "111", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "tencent-main", refs[0].AccountKey)
		assert.Equal(t, "c.example.com", refs[2].ResourceID)

		// 页满连续翻页：第二页 Offset=2，第二页仍满则第三页（空页）Offset=4 后终止
		require.Len(t, fake.listReqs, 3)
		assert.Equal(t, int64(0), *fake.listReqs[0].Offset)
		assert.Equal(t, int64(2), *fake.listReqs[1].Offset)
		assert.Equal(t, int64(4), *fake.listReqs[2].Offset)
		assert.Equal(t, int64(2), *fake.listReqs[1].Limit)
	})

	t.Run("单页不足页大小即止", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCdnClient{pages: [][]*tencentcdn.DetailDomain{
			{cdnCertDomain("a.example.com", "111")},
		}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Len(t, fake.listReqs, 1)
	})

	t.Run("Https配置存在但CertInfo缺失不构成引用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		detail := &tencentcdn.DetailDomain{
			Domain: common.StringPtr("e.example.com"),
			Https:  &tencentcdn.Https{Switch: common.StringPtr("off")},
		}
		fake := &fakeCdnClient{pages: [][]*tencentcdn.DetailDomain{{detail}}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return &fakeCdnClient{listErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductCDN)
		require.Error(t, err)
	})
}

// ==================== BindResource ====================

func TestCertAdapterBindCDN(t *testing.T) {
	t.Run("成功绑定并开启HTTPS", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCdnClient{}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "987654321")
		require.NoError(t, err)

		require.Len(t, fake.updateReqs, 1)
		request := fake.updateReqs[0]
		assert.Equal(t, "a.example.com", *request.Domain)
		require.NotNil(t, request.Https)
		assert.Equal(t, "on", *request.Https.Switch)
		require.NotNil(t, request.Https.CertInfo)
		assert.Equal(t, "987654321", *request.Https.CertInfo.CertId)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return &fakeCdnClient{updateErr: errFakeThrottling}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "987654321")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空域名/空证书ID/空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return &fakeCdnClient{}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "", "987654321")
		require.Error(t, err)
		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "")
		require.Error(t, err)
		err = adapter.BindResource(context.Background(), nil, CertProductCDN, "a.example.com", "987654321")
		require.Error(t, err)
	})
}
