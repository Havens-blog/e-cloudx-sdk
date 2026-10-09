package aliyun

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCdnClient CDN SDK fake：listPages 依次返回分页数据
type fakeCdnClient struct {
	pages     [][]cdn.CertInfo
	total     int
	listErr   error
	bindErr   error
	listCalls int
	bindReq   *cdn.SetCdnDomainSSLCertificateRequest
}

func (f *fakeCdnClient) DescribeCdnHttpsDomainList(request *cdn.DescribeCdnHttpsDomainListRequest) (*cdn.DescribeCdnHttpsDomainListResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	page, _ := strconv.Atoi(string(request.PageNumber))
	pageIdx := page - 1
	resp := cdn.CreateDescribeCdnHttpsDomainListResponse()
	resp.TotalCount = f.total
	if pageIdx >= 0 && pageIdx < len(f.pages) {
		resp.CertInfos.CertInfo = f.pages[pageIdx]
	}
	return resp, nil
}

func (f *fakeCdnClient) SetCdnDomainSSLCertificate(request *cdn.SetCdnDomainSSLCertificateRequest) (*cdn.SetCdnDomainSSLCertificateResponse, error) {
	f.bindReq = request
	if f.bindErr != nil {
		return nil, f.bindErr
	}
	return cdn.CreateSetCdnDomainSSLCertificateResponse(), nil
}

func TestCertAdapterCDNListReferences(t *testing.T) {
	t.Run("分页遍历全部HTTPS域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeCdnClient{
			total: 3,
			pages: [][]cdn.CertInfo{
				{
					{DomainName: "a.example.com", CertId: "111"},
					{DomainName: "b.example.com", CertId: "222"},
				},
				{
					{DomainName: "c.example.com", CertId: "333"},
				},
			},
		}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 3)
		assert.Equal(t, 2, fake.listCalls)

		for i, expectDomain := range []string{"a.example.com", "b.example.com", "c.example.com"} {
			assert.Equal(t, expectDomain, refs[i].ResourceID)
			assert.Equal(t, "aliyun", refs[i].Cloud)
			assert.Equal(t, CertProductCDN, refs[i].Product)
			assert.Equal(t, "aliyun-main", refs[i].AccountKey)
		}
		assert.Equal(t, "111", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "333", refs[2].ReferencedCloudCertID)
	})

	t.Run("跳过未绑定证书的域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCdnClient{
			total: 2,
			pages: [][]cdn.CertInfo{
				{
					{DomainName: "a.example.com", CertId: "111"},
					{DomainName: "plain.example.com"},
				},
			},
		}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, "a.example.com", refs[0].ResourceID)
	})

	t.Run("TotalCount权威时整页即停", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		page := []cdn.CertInfo{
			{DomainName: "a.example.com", CertId: "1"},
			{DomainName: "b.example.com", CertId: "2"},
		}
		fake := &fakeCdnClient{total: 2, pages: [][]cdn.CertInfo{page}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		// 页满且已达 TotalCount → 无需探测第二页
		assert.Equal(t, 1, fake.listCalls)
	})

	t.Run("TotalCount缺失时翻页直至不足页", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		page := []cdn.CertInfo{
			{DomainName: "a.example.com", CertId: "1"},
			{DomainName: "b.example.com", CertId: "2"},
		}
		fake := &fakeCdnClient{total: 0, pages: [][]cdn.CertInfo{page}}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		// TotalCount=0（未知）→ 第一页填满后继续探测，第二页空数据（不足页）停止
		assert.Equal(t, 2, fake.listCalls)
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
}

func TestCertAdapterCDNBindResource(t *testing.T) {
	t.Run("cas证书按CertId绑定域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCdnClient{}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "8089870")
		require.NoError(t, err)

		require.NotNil(t, fake.bindReq)
		assert.Equal(t, "a.example.com", fake.bindReq.DomainName)
		assert.Equal(t, "cas", fake.bindReq.CertType)
		assert.Equal(t, requests.Integer("8089870"), fake.bindReq.CertId)
		assert.Equal(t, "on", fake.bindReq.SSLProtocol)
		assert.Equal(t, "cn-hangzhou", fake.bindReq.CertRegion)
	})

	t.Run("绑定失败透传云错误", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return &fakeCdnClient{bindErr: errors.New("SDK.ServerError Message: Certificate.NotFind")}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "8089870")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Certificate.NotFind")
	})

	t.Run("空域名与非法证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCdnClient{}
		adapter.newCdnClient = func(creds *domain.CloudAccount) (cdnCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "", "8089870")
		require.Error(t, err)
		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductCDN, "a.example.com", "oops")
		require.Error(t, err)
		assert.Nil(t, fake.bindReq)
	})
}
