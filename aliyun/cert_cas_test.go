package aliyun

import (
	"context"
	"strconv"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CAS 证书库清单（cert-cas-library-scan）测试。
// 发现：ListUserCertificateOrder(OrderType=Upload) 分页遍历；无绑定语义：
// cas 不进 certSupportedProducts/UploadCert/BindResource。

// fakeCasOrderClient CAS 证书库清单 fake：嵌入既有 fakeCasClient（上传/详情/删除
// 行为复用），覆写 ListUserCertificateOrder 返回分页数据。
// 另：fakeCasClient 的 ListUserCertificateOrder 兜底空页实现位于本文件——
// casCertAPI 扩容后该类型须补齐方法方可编译，而 cert_test.go 不在本任务可改
// 文件清单内（Hard Rule），故同包新文件承载。
type fakeCasOrderClient struct {
	*fakeCasClient
	pages    [][]cas.CertificateOrderListItem
	total    int64
	listErr  error
	listReqs []*cas.ListUserCertificateOrderRequest
}

func (f *fakeCasOrderClient) ListUserCertificateOrder(request *cas.ListUserCertificateOrderRequest) (*cas.ListUserCertificateOrderResponse, error) {
	f.listReqs = append(f.listReqs, request)
	if f.listErr != nil {
		return nil, f.listErr
	}
	page, _ := strconv.Atoi(string(request.CurrentPage))
	pageIdx := page - 1
	resp := cas.CreateListUserCertificateOrderResponse()
	resp.TotalCount = f.total
	if pageIdx >= 0 && pageIdx < len(f.pages) {
		resp.CertificateOrderList = f.pages[pageIdx]
	}
	return resp, nil
}

// casOrderItem 证书库条目夹具（Name=证书名称，CertificateId=CAS 数字证书 ID）。
func casOrderItem(id int64, name string) cas.CertificateOrderListItem {
	return cas.CertificateOrderListItem{CertificateId: id, Name: name, CommonName: "example.com"}
}

// casListAdapter 装配注入清单 fake 的适配器（页大小 2 覆盖翻页分支）。
func casListAdapter(t *testing.T, fake *fakeCasOrderClient) *CertAdapter {
	t.Helper()
	adapter := newTestCertAdapter(t)
	adapter.listPageSize = 2
	adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }
	return adapter
}

func TestCertAdapterCASListReferences(t *testing.T) {
	t.Run("显式OrderType=Upload分页遍历证书库", func(t *testing.T) {
		fake := &fakeCasOrderClient{
			total: 3,
			pages: [][]cas.CertificateOrderListItem{
				{casOrderItem(27029968, "jlccam.com-2026-09"), casOrderItem(20275346, "jlccam.com-2026-08")},
				{casOrderItem(27029002, "www.example.com-2026")},
			},
		}
		adapter := casListAdapter(t, fake)

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCAS)
		require.NoError(t, err)
		require.Len(t, refs, 3)
		require.Len(t, fake.listReqs, 2)
		for i, req := range fake.listReqs {
			// 实测（2026-09-03 活体验证）：空 OrderType 恒 TotalCount=0（默认不返回
			// Upload 类型）——该坑以测试固化，缺省即视为实现回退
			assert.Equal(t, casOrderTypeUpload, req.OrderType, "page %d 必须显式 OrderType=Upload", i+1)
			assert.Equal(t, requests.NewInteger(2), req.ShowSize)
			assert.Equal(t, requests.NewInteger(i+1), req.CurrentPage)
		}

		// 字段映射：resourceId=证书名称、referencedCloudCertId=数字证书 ID 串
		assert.Equal(t, "aliyun", refs[0].Cloud)
		assert.Equal(t, CertProductCAS, refs[0].Product)
		assert.Equal(t, "jlccam.com-2026-09", refs[0].ResourceID)
		assert.Equal(t, "27029968", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "aliyun-main", refs[0].AccountKey)
		assert.Equal(t, "20275346", refs[1].ReferencedCloudCertID)
		assert.Equal(t, "27029002", refs[2].ReferencedCloudCertID)
		assert.Equal(t, "www.example.com-2026", refs[2].ResourceID)
	})

	t.Run("TotalCount权威时整页即停", func(t *testing.T) {
		fake := &fakeCasOrderClient{
			total: 2,
			pages: [][]cas.CertificateOrderListItem{
				{casOrderItem(1, "a-2026"), casOrderItem(2, "b-2026")},
			},
		}
		adapter := casListAdapter(t, fake)

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCAS)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		// 页满且已达 TotalCount → 无需探测第二页
		assert.Len(t, fake.listReqs, 1)
	})

	t.Run("TotalCount缺失时翻页直至不足页", func(t *testing.T) {
		fake := &fakeCasOrderClient{
			total: 0, // TotalCount=0 视为未知
			pages: [][]cas.CertificateOrderListItem{
				{casOrderItem(1, "a-2026"), casOrderItem(2, "b-2026")},
			},
		}
		adapter := casListAdapter(t, fake)

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCAS)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		// 第一页填满后继续探测，第二页空数据（不足页）停止
		assert.Len(t, fake.listReqs, 2)
	})

	t.Run("跳过无数字证书ID的条目", func(t *testing.T) {
		fake := &fakeCasOrderClient{
			total: 2,
			pages: [][]cas.CertificateOrderListItem{
				{casOrderItem(27029968, "jlccam.com-2026-09"), casOrderItem(0, "order-without-cert")},
			},
		}
		adapter := casListAdapter(t, fake)

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCAS)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, "27029968", refs[0].ReferencedCloudCertID)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := casListAdapter(t, &fakeCasOrderClient{listErr: errFakeThrottling})
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductCAS)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

// TestCertProductCASNoDeploySemantics cas 无资源绑定语义（Hard Rule）：不入
// certSupportedProducts，UploadCert/BindResource 显式报错。
func TestCertProductCASNoDeploySemantics(t *testing.T) {
	assert.False(t, certSupportedProducts[CertProductCAS], "cas 不可进可部署产品集")

	adapter := newTestCertAdapter(t)
	adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
		return &fakeCasOrderClient{fakeCasClient: &fakeCasClient{}}, nil
	}
	_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCAS, "n", "cert", "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCertProductNotSupported)

	err = adapter.BindResource(context.Background(), testCertCreds(), CertProductCAS, "res", "27029968")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCertProductNotSupported)
}

// fakeCasClient 的 casCertAPI 扩容方法（兜底空页；证书库清单场景用 fakeCasOrderClient）。
// 实现置于本文件的原因见 fakeCasOrderClient 注释。
func (f *fakeCasClient) ListUserCertificateOrder(request *cas.ListUserCertificateOrderRequest) (*cas.ListUserCertificateOrderResponse, error) {
	resp := cas.CreateListUserCertificateOrderResponse()
	return resp, nil
}
