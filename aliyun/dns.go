package aliyun

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/alidns"
	"github.com/gotomicro/ego/core/elog"
)

const (
	aliyunDNSMaxRetries    = 3
	aliyunDNSBaseBackoffMs = 200
)

// DNSAdapter 阿里云 DNS 适配器
type DNSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDNSAdapter 创建阿里云 DNS 适配器
func NewDNSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DNSAdapter {
	return &DNSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 Alidns 客户端
func (a *DNSAdapter) createClient() (*alidns.Client, error) {
	return alidns.NewClientWithAccessKey(a.defaultRegion, a.accessKeyID, a.accessKeySecret)
}

// retryWithBackoff 指数退避重试
func (a *DNSAdapter) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= aliyunDNSMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < aliyunDNSMaxRetries {
			backoff := time.Duration(float64(aliyunDNSBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("aliyun: DNS API failed after %d retries: %w", aliyunDNSMaxRetries, lastErr)
}

// ListDomains 查询托管域名列表
func (a *DNSAdapter) ListDomains(ctx context.Context) ([]types.DNSDomain, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	var allDomains []types.DNSDomain
	pageNumber := 1
	pageSize := 100

	for {
		request := alidns.CreateDescribeDomainsRequest()
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		var response *alidns.DescribeDomainsResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.DescribeDomains(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("aliyun: list DNS domains failed: %w", err)
		}

		for _, d := range response.Domains.Domain {
			allDomains = append(allDomains, types.DNSDomain{
				DomainID:    d.DomainId,
				DomainName:  d.DomainName,
				RecordCount: d.RecordCount,
				Status:      "normal",
			})
		}

		if len(response.Domains.Domain) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云DNS域名列表成功", elog.Int("count", len(allDomains)))
	return allDomains, nil
}

// ListRecords 查询域名下解析记录列表
func (a *DNSAdapter) ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	var allRecords []types.DNSRecord
	pageNumber := 1
	pageSize := 500

	for {
		request := alidns.CreateDescribeDomainRecordsRequest()
		request.DomainName = domain.DomainName
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		var response *alidns.DescribeDomainRecordsResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.DescribeDomainRecords(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("aliyun: list DNS records for %s failed: %w", domain.DomainName, err)
		}

		for _, r := range response.DomainRecords.Record {
			allRecords = append(allRecords, types.DNSRecord{
				RecordID: r.RecordId,
				Domain:   domain.DomainName,
				RR:       r.RR,
				Type:     r.Type,
				Value:    r.Value,
				TTL:      int(r.TTL),
				Priority: int(r.Priority),
				Line:     r.Line,
				Status:   a.mapRecordStatus(r.Status),
				// 权重：DescribeDomainRecords 返回 Record.Weight（负载均衡解析）。
				// 无权重概念的记录云端返回 0 → NewDNSWeight 归一为 nil（上层展示 "-"）。
				Weight: types.NewDNSWeight(r.Weight),
			})
		}

		if len(response.DomainRecords.Record) < pageSize {
			break
		}
		pageNumber++
	}

	return allRecords, nil
}

// GetRecord 查询单条解析记录详情
func (a *DNSAdapter) GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	request := alidns.CreateDescribeDomainRecordInfoRequest()
	request.RecordId = recordID

	var response *alidns.DescribeDomainRecordInfoResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.DescribeDomainRecordInfo(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun: get DNS record %s failed: %w", recordID, err)
	}

	return &types.DNSRecord{
		RecordID: response.RecordId,
		Domain:   response.DomainName,
		RR:       response.RR,
		Type:     response.Type,
		Value:    response.Value,
		TTL:      int(response.TTL),
		Priority: int(response.Priority),
		Line:     response.Line,
		Status:   a.mapRecordStatus(response.Status),
	}, nil
}

// CreateRecord 创建解析记录
func (a *DNSAdapter) CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
	// 权重写保护：阿里云 AddDomainRecord 请求结构体没有 Weight 参数（权重只能读）。
	// 显式报错而非静默忽略，避免上层以为权重已生效。
	if req.Weight != nil {
		return nil, fmt.Errorf("aliyun: DNS 权重不支持写入（AddDomainRecord 无 Weight 参数，仅可读）")
	}
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	request := alidns.CreateAddDomainRecordRequest()
	request.DomainName = domain
	request.RR = req.RR
	request.Type = req.Type
	request.Value = req.Value
	if req.TTL > 0 {
		request.TTL = requests.NewInteger(req.TTL)
	}
	if req.Priority > 0 {
		request.Priority = requests.NewInteger(req.Priority)
	}
	if req.Line != "" {
		request.Line = req.Line
	}

	var response *alidns.AddDomainRecordResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.AddDomainRecord(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS record failed: %w", err)
	}

	return a.GetRecord(ctx, domain, response.RecordId)
}

// UpdateRecord 修改解析记录
func (a *DNSAdapter) UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
	// 权重写保护：阿里云 UpdateDomainRecord 请求结构体没有 Weight 参数（权重只能读）。
	if req.Weight != nil {
		return nil, fmt.Errorf("aliyun: DNS 权重不支持修改（UpdateDomainRecord 无 Weight 参数，仅可读）")
	}
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	request := alidns.CreateUpdateDomainRecordRequest()
	request.RecordId = recordID
	request.RR = req.RR
	request.Type = req.Type
	request.Value = req.Value
	if req.TTL > 0 {
		request.TTL = requests.NewInteger(req.TTL)
	}
	if req.Priority > 0 {
		request.Priority = requests.NewInteger(req.Priority)
	}
	if req.Line != "" {
		request.Line = req.Line
	}

	err = a.retryWithBackoff(func() error {
		_, e := client.UpdateDomainRecord(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun: update DNS record %s failed: %w", recordID, err)
	}

	return a.GetRecord(ctx, domain, recordID)
}

// DeleteRecord 删除解析记录
func (a *DNSAdapter) DeleteRecord(ctx context.Context, domain, recordID string) error {
	client, err := a.createClient()
	if err != nil {
		return fmt.Errorf("aliyun: create DNS client failed: %w", err)
	}

	request := alidns.CreateDeleteDomainRecordRequest()
	request.RecordId = recordID

	return a.retryWithBackoff(func() error {
		_, e := client.DeleteDomainRecord(request)
		return e
	})
}

// mapDomainStatus 映射阿里云域名 DNS 状态
func (a *DNSAdapter) mapDomainStatus(status string) string {
	// Alidns DnsStatus: NORMAL / DNS_ERROR
	switch status {
	case "NORMAL":
		return "normal"
	default:
		return "paused"
	}
}

// mapRecordStatus 映射阿里云记录状态
func (a *DNSAdapter) mapRecordStatus(status string) string {
	switch status {
	case "ENABLE":
		return "enable"
	case "DISABLE":
		return "disable"
	default:
		return status
	}
}

// ensure interface compliance
var _ = strconv.Itoa
