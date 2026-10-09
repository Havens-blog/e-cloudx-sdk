package tencent

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
)

// fakeTeoClient EdgeOne SDK fake（zones/hosts 各自按页弹出模拟分页）
type fakeTeoClient struct {
	zonesErr    error
	hostsErr    error
	modifyErr   error
	zonesPages  [][]*teo.Zone
	hostsByZone map[string][][]*teo.DetailHost

	zonesReqs  []*teo.DescribeZonesRequest
	hostsReqs  []*teo.DescribeHostsSettingRequest
	modifyReqs []*teo.ModifyHostsCertificateRequest
}

func (f *fakeTeoClient) DescribeZones(request *teo.DescribeZonesRequest) (*teo.DescribeZonesResponse, error) {
	f.zonesReqs = append(f.zonesReqs, request)
	if f.zonesErr != nil {
		return nil, f.zonesErr
	}
	response := teo.NewDescribeZonesResponse()
	response.Response = &teo.DescribeZonesResponseParams{}
	if len(f.zonesPages) > 0 {
		response.Response.Zones = f.zonesPages[0]
		f.zonesPages = f.zonesPages[1:]
	}
	return response, nil
}

func (f *fakeTeoClient) DescribeHostsSetting(request *teo.DescribeHostsSettingRequest) (*teo.DescribeHostsSettingResponse, error) {
	f.hostsReqs = append(f.hostsReqs, request)
	if f.hostsErr != nil {
		return nil, f.hostsErr
	}
	response := teo.NewDescribeHostsSettingResponse()
	response.Response = &teo.DescribeHostsSettingResponseParams{}
	if pages := f.hostsByZone[*request.ZoneId]; len(pages) > 0 {
		response.Response.DetailHosts = pages[0]
		f.hostsByZone[*request.ZoneId] = pages[1:]
	}
	return response, nil
}

func (f *fakeTeoClient) ModifyHostsCertificate(request *teo.ModifyHostsCertificateRequest) (*teo.ModifyHostsCertificateResponse, error) {
	f.modifyReqs = append(f.modifyReqs, request)
	if f.modifyErr != nil {
		return nil, f.modifyErr
	}
	return teo.NewModifyHostsCertificateResponse(), nil
}

// teoCertZone 构造站点
func teoCertZone(zoneID string) *teo.Zone {
	return &teo.Zone{ZoneId: common.StringPtr(zoneID), ZoneName: common.StringPtr(zoneID + ".example.com")}
}

// teoCertHost 构造带证书清单的站点域名（certIDs 为空表示无 HTTPS 配置）
func teoCertHost(zoneID, host string, certIDs []string) *teo.DetailHost {
	detailHost := &teo.DetailHost{
		ZoneId: common.StringPtr(zoneID),
		Host:   common.StringPtr(host),
	}
	if certIDs != nil {
		certs := make([]*teo.ServerCertInfo, 0, len(certIDs))
		for _, certID := range certIDs {
			certs = append(certs, &teo.ServerCertInfo{CertId: common.StringPtr(certID)})
		}
		detailHost.Https = &teo.Https{CertInfo: certs}
	}
	return detailHost
}

// ==================== ListReferences ====================

func TestCertAdapterListEdgeOneReferences(t *testing.T) {
	t.Run("遍历站点与域名并收集多证书引用", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeTeoClient{
			zonesPages: [][]*teo.Zone{
				{teoCertZone("zone-1"), teoCertZone("zone-2")},
			},
			hostsByZone: map[string][][]*teo.DetailHost{
				// zone-1 两页：多证书主机 + 无 HTTPS 主机 + 普通主机
				"zone-1": {
					{teoCertHost("zone-1", "www.a.com", []string{"111", "222"}), teoCertHost("zone-1", "api.a.com", nil)},
					{teoCertHost("zone-1", "img.a.com", []string{"333"})},
				},
				"zone-2": {
					{teoCertHost("zone-2", "www.b.com", []string{"444"})},
				},
			},
		}
		adapter.newTeoClient = func(creds *domain.CloudAccount) (teoCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.NoError(t, err)
		require.Len(t, refs, 4)

		assert.Equal(t, "tencent", refs[0].Cloud)
		assert.Equal(t, CertProductWAF, refs[0].Product)
		assert.Equal(t, "zone-1/www.a.com", refs[0].ResourceID)
		assert.Equal(t, "111", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "tencent-main", refs[0].AccountKey)
		// 同一主机的第二本证书独立成引用
		assert.Equal(t, "zone-1/www.a.com", refs[1].ResourceID)
		assert.Equal(t, "222", refs[1].ReferencedCloudCertID)
		assert.Equal(t, "zone-1/img.a.com", refs[2].ResourceID)
		assert.Equal(t, "zone-2/www.b.com", refs[3].ResourceID)

		// 站点页满触发 Offset 递增；zone-1 主机两页 + zone-2 一页
		require.Len(t, fake.hostsReqs, 3)
		assert.Equal(t, int64(0), *fake.hostsReqs[0].Offset)
		assert.Equal(t, int64(2), *fake.hostsReqs[1].Offset)
	})

	t.Run("站点列举限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newTeoClient = func(creds *domain.CloudAccount) (teoCertAPI, error) {
			return &fakeTeoClient{zonesErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("主机配置列举限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newTeoClient = func(creds *domain.CloudAccount) (teoCertAPI, error) {
			return &fakeTeoClient{
				zonesPages:  [][]*teo.Zone{{teoCertZone("zone-1")}},
				hostsByZone: map[string][][]*teo.DetailHost{"zone-1": nil},
				hostsErr:    errFakeThrottling,
			}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductWAF)
		require.Error(t, err)
	})
}

// ==================== BindResource ====================

func TestCertAdapterBindEdgeOne(t *testing.T) {
	newBindAdapter := func(t *testing.T, fake *fakeTeoClient) *CertAdapter {
		t.Helper()
		adapter := newTestCertAdapter(t)
		adapter.newTeoClient = func(creds *domain.CloudAccount) (teoCertAPI, error) { return fake, nil }
		return adapter
	}

	t.Run("单证书主机整单替换", func(t *testing.T) {
		fake := &fakeTeoClient{hostsByZone: map[string][][]*teo.DetailHost{
			"zone-1": {{teoCertHost("zone-1", "www.a.com", []string{"111"})}},
		}}
		adapter := newBindAdapter(t, fake)

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/www.a.com", "999")
		require.NoError(t, err)

		require.Len(t, fake.modifyReqs, 1)
		request := fake.modifyReqs[0]
		assert.Equal(t, "zone-1", *request.ZoneId)
		require.NotNil(t, request.Hosts)
		require.Len(t, request.Hosts, 1)
		assert.Equal(t, "www.a.com", *request.Hosts[0])
		assert.Equal(t, "sslcert", *request.Mode)
		require.Len(t, request.ServerCertInfo, 1)
		assert.Equal(t, "999", *request.ServerCertInfo[0].CertId)
	})

	t.Run("多证书主机保留扩展证书", func(t *testing.T) {
		fake := &fakeTeoClient{hostsByZone: map[string][][]*teo.DetailHost{
			"zone-1": {{teoCertHost("zone-1", "www.a.com", []string{"111", "222", "999"})}},
		}}
		adapter := newBindAdapter(t, fake)

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/www.a.com", "999")
		require.NoError(t, err)

		require.Len(t, fake.modifyReqs, 1)
		certs := fake.modifyReqs[0].ServerCertInfo
		// 新证书置首位、原主位移除、扩展保留、与新证书重复的旧项去重
		require.Len(t, certs, 2)
		assert.Equal(t, "999", *certs[0].CertId)
		assert.Equal(t, "222", *certs[1].CertId)
	})

	t.Run("主机不在站点内显式报错", func(t *testing.T) {
		fake := &fakeTeoClient{hostsByZone: map[string][][]*teo.DetailHost{
			"zone-1": {{teoCertHost("zone-1", "www.a.com", []string{"111"})}},
		}}
		adapter := newBindAdapter(t, fake)

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/missing.com", "999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("主机无证书配置显式报错", func(t *testing.T) {
		fake := &fakeTeoClient{hostsByZone: map[string][][]*teo.DetailHost{
			"zone-1": {{teoCertHost("zone-1", "www.a.com", nil)}},
		}}
		adapter := newBindAdapter(t, fake)

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/www.a.com", "999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no certificate config")
	})

	t.Run("非法资源ID与限流显式报错", func(t *testing.T) {
		fake := &fakeTeoClient{hostsByZone: map[string][][]*teo.DetailHost{
			"zone-1": {{teoCertHost("zone-1", "www.a.com", []string{"111"})}},
		}}
		fake.modifyErr = errFakeThrottling
		adapter := newBindAdapter(t, fake)

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "no-slash", "999")
		require.Error(t, err)

		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/www.a.com", "999")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)

		err = adapter.BindResource(context.Background(), nil, CertProductWAF, "zone-1/www.a.com", "999")
		require.Error(t, err)

		err = adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "zone-1/www.a.com", "")
		require.Error(t, err)
	})
}
