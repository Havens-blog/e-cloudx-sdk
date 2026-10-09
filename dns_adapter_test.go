package cloudx_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDNSAdapter 模拟 DNS 适配器，用于测试接口契约
type mockDNSAdapter struct {
	listDomainsFunc func(ctx context.Context) ([]types.DNSDomain, error)
	listRecordsFunc func(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error)
	getRecordFunc   func(ctx context.Context, domain, recordID string) (*types.DNSRecord, error)
	createRecordFn  func(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error)
	updateRecordFn  func(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error)
	deleteRecordFn  func(ctx context.Context, domain, recordID string) error
}

func (m *mockDNSAdapter) ListDomains(ctx context.Context) ([]types.DNSDomain, error) {
	if m.listDomainsFunc != nil {
		return m.listDomainsFunc(ctx)
	}
	return nil, nil
}

func (m *mockDNSAdapter) ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
	if m.listRecordsFunc != nil {
		return m.listRecordsFunc(ctx, domain)
	}
	return nil, nil
}

func (m *mockDNSAdapter) GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
	if m.getRecordFunc != nil {
		return m.getRecordFunc(ctx, domain, recordID)
	}
	return nil, nil
}

func (m *mockDNSAdapter) CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
	if m.createRecordFn != nil {
		return m.createRecordFn(ctx, domain, req)
	}
	return nil, nil
}

func (m *mockDNSAdapter) UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
	if m.updateRecordFn != nil {
		return m.updateRecordFn(ctx, domain, recordID, req)
	}
	return nil, nil
}

func (m *mockDNSAdapter) DeleteRecord(ctx context.Context, domain, recordID string) error {
	if m.deleteRecordFn != nil {
		return m.deleteRecordFn(ctx, domain, recordID)
	}
	return nil
}

// 确保 mockDNSAdapter 实现 DNSAdapter 接口
var _ cloudx.DNSAdapter = (*mockDNSAdapter)(nil)

// ============================================================================
// 正常返回场景测试
// ============================================================================

func TestDNSAdapter_ListDomains_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		listDomainsFunc: func(ctx context.Context) ([]types.DNSDomain, error) {
			return []types.DNSDomain{
				{DomainID: "d1", DomainName: "example.com", RecordCount: 10, Status: "normal"},
				{DomainID: "d2", DomainName: "test.org", RecordCount: 5, Status: "normal"},
			}, nil
		},
	}

	domains, err := adapter.ListDomains(context.Background())
	require.NoError(t, err)
	assert.Len(t, domains, 2)
	assert.Equal(t, "example.com", domains[0].DomainName)
	assert.Equal(t, int64(10), domains[0].RecordCount)
	assert.Equal(t, "normal", domains[0].Status)
}

func TestDNSAdapter_ListRecords_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		listRecordsFunc: func(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
			assert.Equal(t, "example.com", domain.DomainName)
			return []types.DNSRecord{
				{RecordID: "r1", Domain: domain.DomainName, RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600, Status: "enable"},
				{RecordID: "r2", Domain: domain.DomainName, RR: "mail", Type: "MX", Value: "mail.example.com", TTL: 300, Priority: 10, Status: "enable"},
			}, nil
		},
	}

	records, err := adapter.ListRecords(context.Background(), types.DNSDomain{DomainName: "example.com"})
	require.NoError(t, err)
	assert.Len(t, records, 2)
	assert.Equal(t, "www", records[0].RR)
	assert.Equal(t, "A", records[0].Type)
	assert.Equal(t, "1.2.3.4", records[0].Value)
	assert.Equal(t, 10, records[1].Priority)
}

func TestDNSAdapter_GetRecord_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		getRecordFunc: func(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
			return &types.DNSRecord{
				RecordID: recordID,
				Domain:   domain,
				RR:       "www",
				Type:     "CNAME",
				Value:    "cdn.example.com",
				TTL:      600,
				Status:   "enable",
			}, nil
		},
	}

	record, err := adapter.GetRecord(context.Background(), "example.com", "r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", record.RecordID)
	assert.Equal(t, "CNAME", record.Type)
	assert.Equal(t, "cdn.example.com", record.Value)
}

func TestDNSAdapter_CreateRecord_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		createRecordFn: func(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
			return &types.DNSRecord{
				RecordID: "new-r1",
				Domain:   domain,
				RR:       req.RR,
				Type:     req.Type,
				Value:    req.Value,
				TTL:      req.TTL,
				Status:   "enable",
			}, nil
		},
	}

	record, err := adapter.CreateRecord(context.Background(), "example.com", types.CreateDNSRecordRequest{
		RR:    "api",
		Type:  "A",
		Value: "10.0.0.1",
		TTL:   300,
	})
	require.NoError(t, err)
	assert.Equal(t, "new-r1", record.RecordID)
	assert.Equal(t, "api", record.RR)
	assert.Equal(t, "10.0.0.1", record.Value)
}

func TestDNSAdapter_UpdateRecord_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		updateRecordFn: func(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
			return &types.DNSRecord{
				RecordID: recordID,
				Domain:   domain,
				RR:       req.RR,
				Type:     req.Type,
				Value:    req.Value,
				TTL:      req.TTL,
				Status:   "enable",
			}, nil
		},
	}

	record, err := adapter.UpdateRecord(context.Background(), "example.com", "r1", types.UpdateDNSRecordRequest{
		RR:    "www",
		Type:  "A",
		Value: "10.0.0.2",
		TTL:   600,
	})
	require.NoError(t, err)
	assert.Equal(t, "r1", record.RecordID)
	assert.Equal(t, "10.0.0.2", record.Value)
}

func TestDNSAdapter_DeleteRecord_Normal(t *testing.T) {
	adapter := &mockDNSAdapter{
		deleteRecordFn: func(ctx context.Context, domain, recordID string) error {
			assert.Equal(t, "example.com", domain)
			assert.Equal(t, "r1", recordID)
			return nil
		},
	}

	err := adapter.DeleteRecord(context.Background(), "example.com", "r1")
	require.NoError(t, err)
}

// ============================================================================
// 认证失败场景测试
// ============================================================================

func TestDNSAdapter_ListDomains_AuthFailure(t *testing.T) {
	adapter := &mockDNSAdapter{
		listDomainsFunc: func(ctx context.Context) ([]types.DNSDomain, error) {
			return nil, fmt.Errorf("aliyun: DNS API failed after 3 retries: InvalidAccessKeyId.NotFound")
		},
	}

	domains, err := adapter.ListDomains(context.Background())
	assert.Nil(t, domains)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aliyun")
	assert.Contains(t, err.Error(), "InvalidAccessKeyId")
}

func TestDNSAdapter_CreateRecord_AuthFailure(t *testing.T) {
	adapter := &mockDNSAdapter{
		createRecordFn: func(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
			return nil, fmt.Errorf("aws: DNS API failed after 3 retries: AccessDeniedException: User is not authorized")
		},
	}

	record, err := adapter.CreateRecord(context.Background(), "example.com", types.CreateDNSRecordRequest{
		RR: "www", Type: "A", Value: "1.2.3.4",
	})
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws")
	assert.Contains(t, err.Error(), "AccessDeniedException")
}

func TestDNSAdapter_UpdateRecord_AuthFailure(t *testing.T) {
	adapter := &mockDNSAdapter{
		updateRecordFn: func(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
			return nil, fmt.Errorf("huawei: DNS API failed after 3 retries: APIGW.0301: Incorrect IAM authentication information")
		},
	}

	record, err := adapter.UpdateRecord(context.Background(), "example.com", "r1", types.UpdateDNSRecordRequest{
		RR: "www", Type: "A", Value: "1.2.3.4",
	})
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "huawei")
}

func TestDNSAdapter_DeleteRecord_AuthFailure(t *testing.T) {
	adapter := &mockDNSAdapter{
		deleteRecordFn: func(ctx context.Context, domain, recordID string) error {
			return fmt.Errorf("tencent: DNS API failed after 3 retries: AuthFailure.SecretIdNotFound")
		},
	}

	err := adapter.DeleteRecord(context.Background(), "example.com", "r1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tencent")
	assert.Contains(t, err.Error(), "AuthFailure")
}

// ============================================================================
// 超时重试场景测试
// ============================================================================

// retryWithBackoff 模拟指数退避重试逻辑（与各适配器实现一致）
func retryWithBackoff(maxRetries int, baseBackoffMs int, provider string, operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < maxRetries {
			backoff := time.Duration(float64(baseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("%s: DNS API failed after %d retries: %w", provider, maxRetries, lastErr)
}

func TestRetryWithBackoff_SucceedsAfterRetries(t *testing.T) {
	var callCount int32

	err := retryWithBackoff(3, 10, "test", func() error {
		count := atomic.AddInt32(&callCount, 1)
		if count < 3 {
			return errors.New("timeout")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&callCount))
}

func TestRetryWithBackoff_ExhaustsRetries(t *testing.T) {
	var callCount int32

	err := retryWithBackoff(3, 10, "aliyun", func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("connection timeout")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aliyun: DNS API failed after 3 retries")
	assert.Contains(t, err.Error(), "connection timeout")
	assert.Equal(t, int32(4), atomic.LoadInt32(&callCount)) // 1 initial + 3 retries
}

func TestRetryWithBackoff_SucceedsFirstTry(t *testing.T) {
	var callCount int32

	err := retryWithBackoff(3, 10, "aws", func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

func TestRetryWithBackoff_ErrorWrapping(t *testing.T) {
	providers := []string{"aliyun", "aws", "huawei", "tencent", "volcano"}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			err := retryWithBackoff(3, 10, provider, func() error {
				return errors.New("network error")
			})

			assert.Error(t, err)
			assert.Contains(t, err.Error(), provider)
			assert.Contains(t, err.Error(), "DNS API failed after 3 retries")
			assert.Contains(t, err.Error(), "network error")
		})
	}
}

// ============================================================================
// 空结果场景测试
// ============================================================================

func TestDNSAdapter_ListDomains_Empty(t *testing.T) {
	adapter := &mockDNSAdapter{
		listDomainsFunc: func(ctx context.Context) ([]types.DNSDomain, error) {
			return nil, nil
		},
	}

	domains, err := adapter.ListDomains(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, domains)
}

func TestDNSAdapter_ListRecords_Empty(t *testing.T) {
	adapter := &mockDNSAdapter{
		listRecordsFunc: func(ctx context.Context, _ types.DNSDomain) ([]types.DNSRecord, error) {
			return []types.DNSRecord{}, nil
		},
	}

	records, err := adapter.ListRecords(context.Background(), types.DNSDomain{DomainName: "example.com"})
	assert.NoError(t, err)
	assert.Empty(t, records)
}

func TestDNSAdapter_GetRecord_NotFound(t *testing.T) {
	adapter := &mockDNSAdapter{
		getRecordFunc: func(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
			return nil, fmt.Errorf("aliyun: DNS record %s not found", recordID)
		},
	}

	record, err := adapter.GetRecord(context.Background(), "example.com", "nonexistent")
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ============================================================================
// 数据类型完整性测试
// ============================================================================

func TestDNSRecord_AllFields(t *testing.T) {
	record := types.DNSRecord{
		RecordID: "r1",
		Domain:   "example.com",
		RR:       "mail",
		Type:     "MX",
		Value:    "mail.example.com",
		TTL:      600,
		Priority: 10,
		Line:     "default",
		Status:   "enable",
	}

	assert.Equal(t, "r1", record.RecordID)
	assert.Equal(t, "example.com", record.Domain)
	assert.Equal(t, "mail", record.RR)
	assert.Equal(t, "MX", record.Type)
	assert.Equal(t, "mail.example.com", record.Value)
	assert.Equal(t, 600, record.TTL)
	assert.Equal(t, 10, record.Priority)
	assert.Equal(t, "default", record.Line)
	assert.Equal(t, "enable", record.Status)
}

func TestDNSDomain_AllFields(t *testing.T) {
	domain := types.DNSDomain{
		DomainID:    "d1",
		DomainName:  "example.com",
		RecordCount: 42,
		Status:      "normal",
	}

	assert.Equal(t, "d1", domain.DomainID)
	assert.Equal(t, "example.com", domain.DomainName)
	assert.Equal(t, int64(42), domain.RecordCount)
	assert.Equal(t, "normal", domain.Status)
}

func TestCreateDNSRecordRequest_Fields(t *testing.T) {
	req := types.CreateDNSRecordRequest{
		RR:       "www",
		Type:     "A",
		Value:    "1.2.3.4",
		TTL:      300,
		Priority: 0,
		Line:     "default",
	}

	assert.Equal(t, "www", req.RR)
	assert.Equal(t, "A", req.Type)
	assert.Equal(t, "1.2.3.4", req.Value)
	assert.Equal(t, 300, req.TTL)
}

func TestUpdateDNSRecordRequest_Fields(t *testing.T) {
	req := types.UpdateDNSRecordRequest{
		RR:    "api",
		Type:  "CNAME",
		Value: "api.cdn.example.com",
		TTL:   3600,
	}

	assert.Equal(t, "api", req.RR)
	assert.Equal(t, "CNAME", req.Type)
	assert.Equal(t, "api.cdn.example.com", req.Value)
	assert.Equal(t, 3600, req.TTL)
}

// ============================================================================
// 集成测试：验证各云厂商 DNS() 方法返回非 nil
// ============================================================================

func TestAllProviders_DNS_NotNil(t *testing.T) {
	factory := cloudx.NewAdapterFactory(nil)

	providers := []struct {
		name     string
		provider string
		region   string
	}{
		{"阿里云", "aliyun", "cn-hangzhou"},
		{"AWS", "aws", "us-east-1"},
		{"华为云", "huawei", "cn-north-4"},
		{"腾讯云", "tencent", "ap-guangzhou"},
		{"火山引擎", "volcano", "cn-beijing"},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			account := &domain.CloudAccount{
				ID:              1,
				Name:            "test-dns-" + p.provider,
				Provider:        domain.CloudProvider(p.provider),
				AccessKeyID:     "test-key-id-12345",
				AccessKeySecret: "test-key-secret-12345",
				Regions:         []string{p.region},
				Status:          domain.CloudAccountStatusActive,
				TenantID:        6,
			}

			adapter, err := factory.CreateAdapter(account)
			require.NoError(t, err)
			require.NotNil(t, adapter)

			dnsAdapter := adapter.DNS()
			assert.NotNil(t, dnsAdapter, "%s DNS() should return non-nil adapter", p.name)
		})
	}
}
