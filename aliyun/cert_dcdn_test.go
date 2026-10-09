package aliyun

import (
	"context"
	"strconv"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dcdn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDcdnClient DCDN SDK fake
type fakeDcdnClient struct {
	pages     [][]dcdn.CertInfo
	total     int
	listErr   error
	bindErr   error
	listCalls int
	bindReq   *dcdn.SetDcdnDomainSSLCertificateRequest
}

func (f *fakeDcdnClient) DescribeDcdnHttpsDomainList(request *dcdn.DescribeDcdnHttpsDomainListRequest) (*dcdn.DescribeDcdnHttpsDomainListResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	page, _ := strconv.Atoi(string(request.PageNumber))
	pageIdx := page - 1
	resp := dcdn.CreateDescribeDcdnHttpsDomainListResponse()
	resp.TotalCount = f.total
	if pageIdx >= 0 && pageIdx < len(f.pages) {
		resp.CertInfos.CertInfo = f.pages[pageIdx]
	}
	return resp, nil
}

func (f *fakeDcdnClient) SetDcdnDomainSSLCertificate(request *dcdn.SetDcdnDomainSSLCertificateRequest) (*dcdn.SetDcdnDomainSSLCertificateResponse, error) {
	f.bindReq = request
	if f.bindErr != nil {
		return nil, f.bindErr
	}
	return dcdn.CreateSetDcdnDomainSSLCertificateResponse(), nil
}

func TestCertAdapterDCDNListReferences(t *testing.T) {
	t.Run("分页遍历全部HTTPS域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeDcdnClient{
			total: 3,
			pages: [][]dcdn.CertInfo{
				{
					{DomainName: "a.dcdn.example.com", CertId: "111"},
					{DomainName: "b.dcdn.example.com", CertId: "222"},
				},
				{
					{DomainName: "c.dcdn.example.com", CertId: "333"},
				},
			},
		}
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductDCDN)
		require.NoError(t, err)
		require.Len(t, refs, 3)
		assert.Equal(t, 2, fake.listCalls)

		for i, expectDomain := range []string{"a.dcdn.example.com", "b.dcdn.example.com", "c.dcdn.example.com"} {
			assert.Equal(t, expectDomain, refs[i].ResourceID)
			assert.Equal(t, "aliyun", refs[i].Cloud)
			assert.Equal(t, CertProductDCDN, refs[i].Product)
			assert.Equal(t, "aliyun-main", refs[i].AccountKey)
		}
		assert.Equal(t, "222", refs[1].ReferencedCloudCertID)
	})

	t.Run("跳过未绑定证书的域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeDcdnClient{
			total: 2,
			pages: [][]dcdn.CertInfo{
				{
					{DomainName: "plain.dcdn.example.com"},
					{DomainName: "a.dcdn.example.com", CertId: "111"},
				},
			},
		}
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductDCDN)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, "a.dcdn.example.com", refs[0].ResourceID)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) {
			return &fakeDcdnClient{listErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductDCDN)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

func TestCertAdapterDCDNBindResource(t *testing.T) {
	t.Run("cas证书按CertId绑定域名", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeDcdnClient{}
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductDCDN, "a.dcdn.example.com", "8089870")
		require.NoError(t, err)

		require.NotNil(t, fake.bindReq)
		assert.Equal(t, "a.dcdn.example.com", fake.bindReq.DomainName)
		assert.Equal(t, "cas", fake.bindReq.CertType)
		assert.Equal(t, requests.Integer("8089870"), fake.bindReq.CertId)
		assert.Equal(t, "on", fake.bindReq.SSLProtocol)
		assert.Equal(t, "cn-hangzhou", fake.bindReq.CertRegion)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) {
			return &fakeDcdnClient{bindErr: errFakeThrottling}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductDCDN, "a.dcdn.example.com", "8089870")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("非法证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newDcdnClient = func(creds *domain.CloudAccount) (dcdnCertAPI, error) {
			return &fakeDcdnClient{}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductDCDN, "a.dcdn.example.com", "oops")
		require.Error(t, err)
	})
}
