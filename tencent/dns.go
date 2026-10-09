package tencent

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	dnspod "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dnspod/v20210323"
)

const (
	tencentDNSMaxRetries    = 3
	tencentDNSBaseBackoffMs = 200
)

// DNSAdapter 腾讯云 DNSPod 适配器
type DNSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDNSAdapter 创建腾讯云 DNS 适配器
func NewDNSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DNSAdapter {
	return &DNSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 DNSPod 客户端
func (a *DNSAdapter) createClient() (*dnspod.Client, error) {
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	return dnspod.NewClient(credential, "", cpf)
}

// retryWithBackoff 指数退避重试
func (a *DNSAdapter) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= tencentDNSMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < tencentDNSMaxRetries {
			backoff := time.Duration(float64(tencentDNSBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("tencent: DNS API failed after %d retries: %w", tencentDNSMaxRetries, lastErr)
}

// ListDomains 查询托管域名列表
func (a *DNSAdapter) ListDomains(ctx context.Context) ([]types.DNSDomain, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	var allDomains []types.DNSDomain
	offset := int64(0)
	limit := int64(100)

	for {
		request := dnspod.NewDescribeDomainListRequest()
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(limit)
		request.Type = common.StringPtr("ALL")

		var response *dnspod.DescribeDomainListResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.DescribeDomainList(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("tencent: list domains failed: %w", err)
		}

		if response.Response.DomainList == nil || len(response.Response.DomainList) == 0 {
			break
		}

		for _, d := range response.Response.DomainList {
			domainID := ""
			if d.DomainId != nil {
				domainID = strconv.FormatUint(*d.DomainId, 10)
			}
			recordCount := int64(0)
			if d.RecordCount != nil {
				recordCount = int64(*d.RecordCount)
			}

			allDomains = append(allDomains, types.DNSDomain{
				DomainID:    domainID,
				DomainName:  ptrStr(d.Name),
				RecordCount: recordCount,
				Status:      a.mapDomainStatus(ptrStr(d.Status)),
			})
		}

		if len(response.Response.DomainList) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云DNS域名列表成功", elog.Int("count", len(allDomains)))
	return allDomains, nil
}

// ListRecords 查询域名下解析记录列表
func (a *DNSAdapter) ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	var allRecords []types.DNSRecord
	offset := uint64(0)
	limit := uint64(500)

	for {
		request := dnspod.NewDescribeRecordListRequest()
		request.Domain = common.StringPtr(domain.DomainName)
		request.Offset = common.Uint64Ptr(offset)
		request.Limit = common.Uint64Ptr(limit)

		var response *dnspod.DescribeRecordListResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.DescribeRecordList(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("tencent: list records for %s failed: %w", domain.DomainName, err)
		}

		if response.Response.RecordList == nil || len(response.Response.RecordList) == 0 {
			break
		}

		for _, r := range response.Response.RecordList {
			recordID := ""
			if r.RecordId != nil {
				recordID = strconv.FormatUint(*r.RecordId, 10)
			}
			ttl := 0
			if r.TTL != nil {
				ttl = int(*r.TTL)
			}
			mx := 0
			if r.MX != nil {
				mx = int(*r.MX)
			}

			allRecords = append(allRecords, types.DNSRecord{
				RecordID: recordID,
				Domain:   domain.DomainName,
				RR:       ptrStr(r.Name),
				Type:     ptrStr(r.Type),
				Value:    ptrStr(r.Value),
				TTL:      ttl,
				Priority: mx,
				Line:     ptrStr(r.Line),
				Status:   a.mapRecordStatus(ptrStr(r.Status)),
			})
		}

		if len(response.Response.RecordList) < int(limit) {
			break
		}
		offset += limit
	}

	return allRecords, nil
}

// GetRecord 查询单条解析记录详情
func (a *DNSAdapter) GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	rid, err := strconv.ParseUint(recordID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("tencent: invalid record ID %s: %w", recordID, err)
	}

	request := dnspod.NewDescribeRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordId = common.Uint64Ptr(rid)

	var response *dnspod.DescribeRecordResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.DescribeRecord(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("tencent: get record %s failed: %w", recordID, err)
	}

	info := response.Response.RecordInfo
	ttl := 0
	if info.TTL != nil {
		ttl = int(*info.TTL)
	}
	mx := 0
	if info.MX != nil {
		mx = int(*info.MX)
	}

	return &types.DNSRecord{
		RecordID: recordID,
		Domain:   domain,
		RR:       ptrStr(info.SubDomain),
		Type:     ptrStr(info.RecordType),
		Value:    ptrStr(info.Value),
		TTL:      ttl,
		Priority: mx,
		Line:     ptrStr(info.RecordLine),
		Status:   a.mapRecordStatusFromUint(info.Enabled),
	}, nil
}

// CreateRecord 创建解析记录
func (a *DNSAdapter) CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	request := dnspod.NewCreateRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.SubDomain = common.StringPtr(req.RR)
	request.RecordType = common.StringPtr(req.Type)
	request.Value = common.StringPtr(req.Value)
	request.RecordLine = common.StringPtr("默认")
	if req.Line != "" {
		request.RecordLine = common.StringPtr(req.Line)
	}
	if req.TTL > 0 {
		request.TTL = common.Uint64Ptr(uint64(req.TTL))
	}
	if req.Priority > 0 {
		request.MX = common.Uint64Ptr(uint64(req.Priority))
	}

	var response *dnspod.CreateRecordResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.CreateRecord(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("tencent: create record failed: %w", err)
	}

	newID := ""
	if response.Response.RecordId != nil {
		newID = strconv.FormatUint(*response.Response.RecordId, 10)
	}

	return a.GetRecord(ctx, domain, newID)
}

// UpdateRecord 修改解析记录
func (a *DNSAdapter) UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	rid, err := strconv.ParseUint(recordID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("tencent: invalid record ID %s: %w", recordID, err)
	}

	request := dnspod.NewModifyRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordId = common.Uint64Ptr(rid)
	request.SubDomain = common.StringPtr(req.RR)
	request.RecordType = common.StringPtr(req.Type)
	request.Value = common.StringPtr(req.Value)
	request.RecordLine = common.StringPtr("默认")
	if req.Line != "" {
		request.RecordLine = common.StringPtr(req.Line)
	}
	if req.TTL > 0 {
		request.TTL = common.Uint64Ptr(uint64(req.TTL))
	}
	if req.Priority > 0 {
		request.MX = common.Uint64Ptr(uint64(req.Priority))
	}

	err = a.retryWithBackoff(func() error {
		_, e := client.ModifyRecord(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("tencent: update record %s failed: %w", recordID, err)
	}

	return a.GetRecord(ctx, domain, recordID)
}

// DeleteRecord 删除解析记录
func (a *DNSAdapter) DeleteRecord(ctx context.Context, domain, recordID string) error {
	client, err := a.createClient()
	if err != nil {
		return fmt.Errorf("tencent: create DNSPod client failed: %w", err)
	}

	rid, err := strconv.ParseUint(recordID, 10, 64)
	if err != nil {
		return fmt.Errorf("tencent: invalid record ID %s: %w", recordID, err)
	}

	request := dnspod.NewDeleteRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordId = common.Uint64Ptr(rid)

	return a.retryWithBackoff(func() error {
		_, e := client.DeleteRecord(request)
		return e
	})
}

// mapDomainStatus 映射腾讯云域名状态
func (a *DNSAdapter) mapDomainStatus(status string) string {
	switch status {
	case "ENABLE":
		return "normal"
	case "PAUSE":
		return "paused"
	case "SPAM":
		return "locked"
	default:
		return status
	}
}

// mapRecordStatus 映射腾讯云记录状态
func (a *DNSAdapter) mapRecordStatus(status string) string {
	switch status {
	case "ENABLE", "1":
		return "enable"
	case "DISABLE", "0":
		return "disable"
	default:
		return status
	}
}

// mapRecordStatusFromUint 从 uint64 指针映射记录状态
func (a *DNSAdapter) mapRecordStatusFromUint(enabled *uint64) string {
	if enabled == nil {
		return "enable"
	}
	if *enabled == 1 {
		return "enable"
	}
	return "disable"
}
