package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWafCaller WAF 3.0 RPC 调用 fake：按 action 分发
type fakeWafCaller struct {
	instanceErr error
	resListErr  error
	domainErr   error
	modifyErr   error

	resPages     []string // DescribeDefenseResources 各页原始 JSON
	resPageCalls int
	domainBodies map[string]string // domain -> DescribeDomainDetail 响应 JSON

	instanceCalls int
	detailCalls   []string
	modifyParams  []map[string]string
}

func (f *fakeWafCaller) call(action string, params map[string]string) (json.RawMessage, error) {
	switch action {
	case "DescribeInstance":
		f.instanceCalls++
		if f.instanceErr != nil {
			return nil, f.instanceErr
		}
		return json.RawMessage(`{"InstanceId":"waf-cn-test","RequestID":"r1"}`), nil
	case "DescribeDefenseResources":
		f.resPageCalls++
		if f.resListErr != nil {
			return nil, f.resListErr
		}
		page, _ := strconv.Atoi(params["PageNumber"])
		idx := page - 1
		if idx < 0 || idx >= len(f.resPages) {
			return json.RawMessage(`{"TotalCount":0,"Resources":[]}`), nil
		}
		return json.RawMessage(f.resPages[idx]), nil
	case "DescribeDomainDetail":
		f.detailCalls = append(f.detailCalls, params["Domain"])
		if f.domainErr != nil {
			return nil, f.domainErr
		}
		body, ok := f.domainBodies[params["Domain"]]
		if !ok {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(body), nil
	case "ModifyDomain":
		f.modifyParams = append(f.modifyParams, params)
		if f.modifyErr != nil {
			return nil, f.modifyErr
		}
		return json.RawMessage(`{"RequestId":"r2","DomainInfo":{"Domain":"ok"}}`), nil
	}
	return nil, errors.New("unexpected action: " + action)
}

func TestCertAdapterWAFListReferences(t *testing.T) {
	resPage1 := `{"TotalCount":3,"Resources":[
		{"Resource":"a.example.com-waf","Detail":"{\"product\":\"waf\",\"domain\":\"a.example.com\"}"},
		{"Resource":"b.example.com-waf","Detail":"{\"product\":\"waf\",\"domain\":\"b.example.com\"}"}
	]}`
	resPage2 := `{"TotalCount":3,"Resources":[
		{"Resource":"c.example.com-waf","Detail":"{\"product\":\"waf\",\"domain\":\"c.example.com\"}"}
	]}`
	domainBodies := map[string]string{
		"a.example.com": `{"Listen":{"HttpsPorts":[443],"CertId":"111"},"Redirect":{"Backends":["1.1.1.1"]}}`,
		"b.example.com": `{"Listen":{"HttpPorts":[80]}}`,
		"c.example.com": `{"Listen":{"HttpsPorts":[443],"CertId":"333"},"Redirect":{}}`,
	}

	t.Run("分页遍历防护对象并提取证书ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeWafCaller{
			resPages:     []string{resPage1, resPage2},
			domainBodies: domainBodies,
		}
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.NoError(t, err)
		require.Len(t, refs, 2)

		assert.Equal(t, "a.example.com", refs[0].ResourceID)
		assert.Equal(t, "111", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "aliyun", refs[0].Cloud)
		assert.Equal(t, CertProductWAF, refs[0].Product)
		assert.Equal(t, "aliyun-main", refs[0].AccountKey)

		assert.Equal(t, "c.example.com", refs[1].ResourceID)
		assert.Equal(t, "333", refs[1].ReferencedCloudCertID)

		// 两页遍历 + 3 个域名详情
		assert.Equal(t, 2, fake.resPageCalls)
		assert.Equal(t, []string{"a.example.com", "b.example.com", "c.example.com"}, fake.detailCalls)
	})

	t.Run("DescribeDomainDetail失败跳过该域名继续", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.listPageSize = 2
		fake := &fakeWafCaller{
			resPages:  []string{resPage1},
			domainErr: errors.New("waf domain detail unavailable"),
			// TotalCount 与页数据一致，仍翻页判定由页数据决定
		}
		fake.domainBodies = domainBodies
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) {
			return &fakeWafCaller{resListErr: errFakeThrottling}, nil
		}
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), CertProductWAF)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

func TestCertAdapterWAFBindResource(t *testing.T) {
	t.Run("读改写Listen.CertId并保留Redirect", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeWafCaller{
			domainBodies: map[string]string{
				"a.example.com": `{"Listen":{"HttpsPorts":[443],"Http2Enabled":true,"CertId":"111"},"Redirect":{"Backends":["1.1.1.1"],"Loadbalance":"iphash"}}`,
			},
		}
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "a.example.com", "8089870")
		require.NoError(t, err)

		require.Len(t, fake.modifyParams, 1)
		params := fake.modifyParams[0]
		assert.Equal(t, "a.example.com", params["Domain"])
		assert.Equal(t, "waf-cn-test", params["InstanceId"])

		var listen map[string]any
		require.NoError(t, json.Unmarshal([]byte(params["Listen"]), &listen))
		assert.Equal(t, "8089870", listen["CertId"])
		// 未知监听字段保留
		assert.Equal(t, true, listen["Http2Enabled"])
		assert.Equal(t, []any{443.0}, listen["HttpsPorts"])

		var redirect map[string]any
		require.NoError(t, json.Unmarshal([]byte(params["Redirect"]), &redirect))
		assert.Equal(t, "iphash", redirect["Loadbalance"])
	})

	t.Run("域名无Listen配置显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeWafCaller{
			domainBodies: map[string]string{
				"a.example.com": `{"Listen":{"HttpPorts":[80]}}`,
			},
		}
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "a.example.com", "8089870")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Listen")
	})

	t.Run("ModifyDomain限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeWafCaller{
			domainBodies: map[string]string{
				"a.example.com": `{"Listen":{"HttpsPorts":[443],"CertId":"111"},"Redirect":{}}`,
			},
			modifyErr: errFakeThrottling,
		}
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "a.example.com", "8089870")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("实例查询失败透传", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeWafCaller{instanceErr: errors.New("waf instance unavailable")}
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) { return fake, nil }

		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "a.example.com", "8089870")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
	})

	t.Run("空域名显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newWafCaller = func(creds *domain.CloudAccount, region string) (wafCertCaller, error) {
			return &fakeWafCaller{}, nil
		}
		err := adapter.BindResource(context.Background(), testCertCreds(), CertProductWAF, "", "8089870")
		require.Error(t, err)
	})
}
