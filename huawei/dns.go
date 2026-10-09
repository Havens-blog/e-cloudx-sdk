package huawei

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	dns "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2"
	dnsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/model"
	dnsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/region"
)

const (
	huaweiDNSMaxRetries    = 3
	huaweiDNSBaseBackoffMs = 200
)

// DNSAdapter 华为云 DNS 适配器
type DNSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDNSAdapter 创建华为云 DNS 适配器
func NewDNSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DNSAdapter {
	return &DNSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建华为云 DNS 客户端
func (a *DNSAdapter) createClient() (*dns.DnsClient, error) {
	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("huawei: build credentials failed: %w", err)
	}

	region, err := dnsregion.SafeValueOf(a.defaultRegion)
	if err != nil {
		return nil, fmt.Errorf("huawei: invalid DNS region %s: %w", a.defaultRegion, err)
	}

	builder := dns.DnsClientBuilder().
		WithRegion(region).
		WithCredential(auth)

	client, err := builder.SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("huawei: build DNS client failed: %w", err)
	}

	return dns.NewDnsClient(client), nil
}

// retryWithBackoff 指数退避重试
func (a *DNSAdapter) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= huaweiDNSMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < huaweiDNSMaxRetries {
			backoff := time.Duration(float64(huaweiDNSBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("huawei: DNS API failed after %d retries: %w", huaweiDNSMaxRetries, lastErr)
}

// ListDomains 查询托管域名列表（公网 Zone）
func (a *DNSAdapter) ListDomains(ctx context.Context) ([]types.DNSDomain, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	var allDomains []types.DNSDomain
	var marker *string
	zoneType := "public"

	for {
		request := &dnsmodel.ListPublicZonesRequest{
			Type:   &zoneType,
			Marker: marker,
		}

		var response *dnsmodel.ListPublicZonesResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.ListPublicZones(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("huawei: list public zones failed: %w", err)
		}

		if response.Zones == nil {
			break
		}

		for _, zone := range *response.Zones {
			domainName := strings.TrimSuffix(safeStr(zone.Name), ".")
			recordCount := int64(0)
			if zone.RecordNum != nil {
				recordCount = int64(*zone.RecordNum)
			}

			allDomains = append(allDomains, types.DNSDomain{
				DomainID:    safeStr(zone.Id),
				DomainName:  domainName,
				RecordCount: recordCount,
				Status:      a.mapDNSZoneStatus(safeStr(zone.Status)),
			})
		}

		if response.Links == nil || response.Links.Next == nil || *response.Links.Next == "" {
			break
		}
		zones := *response.Zones
		if len(zones) == 0 {
			break
		}
		lastID := safeStr(zones[len(zones)-1].Id)
		marker = &lastID
	}

	a.logger.Info("获取华为云DNS域名列表成功", elog.Int("count", len(allDomains)))
	return allDomains, nil
}

// ListRecords 查询域名下解析记录列表
func (a *DNSAdapter) ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	var allRecords []types.DNSRecord
	var marker *string

	for {
		request := &dnsmodel.ShowRecordSetByZoneRequest{
			ZoneId: domain.DomainID,
			Marker: marker,
		}

		var response *dnsmodel.ShowRecordSetByZoneResponse
		err = a.retryWithBackoff(func() error {
			var e error
			response, e = client.ShowRecordSetByZone(request)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("huawei: list record sets for zone %s failed: %w", domain.DomainID, err)
		}

		if response.Recordsets == nil {
			break
		}

		for _, rs := range *response.Recordsets {
			recordType := safeStr(rs.Type)
			rr := strings.TrimSuffix(safeStr(rs.Name), ".")
			ttl := 0
			if rs.Ttl != nil {
				ttl = int(*rs.Ttl)
			}

			if rs.Records != nil {
				for _, val := range *rs.Records {
					allRecords = append(allRecords, types.DNSRecord{
						RecordID: safeStr(rs.Id),
						Domain:   domain.DomainName,
						RR:       rr,
						Type:     recordType,
						Value:    val,
						TTL:      ttl,
						Line:     "default",
						Status:   a.mapDNSRecordStatus(safeStr(rs.Status)),
					})
				}
			}
		}

		if response.Links == nil || response.Links.Next == nil || *response.Links.Next == "" {
			break
		}
		recordsets := *response.Recordsets
		if len(recordsets) == 0 {
			break
		}
		lastID := safeStr(recordsets[len(recordsets)-1].Id)
		marker = &lastID
	}

	return allRecords, nil
}

// GetRecord 查询单条解析记录详情
func (a *DNSAdapter) GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	request := &dnsmodel.ShowRecordSetRequest{
		ZoneId:      domain,
		RecordsetId: recordID,
	}

	var response *dnsmodel.ShowRecordSetResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.ShowRecordSet(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("huawei: get record set %s failed: %w", recordID, err)
	}

	rr := strings.TrimSuffix(safeStr(response.Name), ".")
	ttl := 0
	if response.Ttl != nil {
		ttl = int(*response.Ttl)
	}

	value := ""
	if response.Records != nil && len(*response.Records) > 0 {
		value = (*response.Records)[0]
	}

	return &types.DNSRecord{
		RecordID: safeStr(response.Id),
		Domain:   domain,
		RR:       rr,
		Type:     safeStr(response.Type),
		Value:    value,
		TTL:      ttl,
		Line:     "default",
		Status:   a.mapDNSRecordStatus(safeStr(response.Status)),
	}, nil
}

// CreateRecord 创建解析记录
func (a *DNSAdapter) CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	fqdn := req.RR + "."
	ttl := int32(req.TTL)
	if ttl == 0 {
		ttl = 300
	}

	request := &dnsmodel.CreateRecordSetRequest{
		ZoneId: domain,
		Body: &dnsmodel.CreateRecordSetRequestBody{
			Name:    fqdn,
			Type:    req.Type,
			Records: []string{req.Value},
			Ttl:     &ttl,
		},
	}

	var response *dnsmodel.CreateRecordSetResponse
	err = a.retryWithBackoff(func() error {
		var e error
		response, e = client.CreateRecordSet(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("huawei: create record set failed: %w", err)
	}

	return a.GetRecord(ctx, domain, safeStr(response.Id))
}

// UpdateRecord 修改解析记录
func (a *DNSAdapter) UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	fqdn := req.RR + "."
	ttl := int32(req.TTL)
	if ttl == 0 {
		ttl = 300
	}

	request := &dnsmodel.UpdateRecordSetRequest{
		ZoneId:      domain,
		RecordsetId: recordID,
		Body: &dnsmodel.UpdateRecordSetReq{
			Name:    &fqdn,
			Type:    &req.Type,
			Records: &[]string{req.Value},
			Ttl:     &ttl,
		},
	}

	err = a.retryWithBackoff(func() error {
		_, e := client.UpdateRecordSet(request)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("huawei: update record set %s failed: %w", recordID, err)
	}

	return a.GetRecord(ctx, domain, recordID)
}

// DeleteRecord 删除解析记录
func (a *DNSAdapter) DeleteRecord(ctx context.Context, domain, recordID string) error {
	client, err := a.createClient()
	if err != nil {
		return fmt.Errorf("huawei: create DNS client failed: %w", err)
	}

	request := &dnsmodel.DeleteRecordSetRequest{
		ZoneId:      domain,
		RecordsetId: recordID,
	}

	return a.retryWithBackoff(func() error {
		_, e := client.DeleteRecordSet(request)
		return e
	})
}

// mapDNSZoneStatus 映射华为云 Zone 状态
func (a *DNSAdapter) mapDNSZoneStatus(status string) string {
	switch status {
	case "ACTIVE":
		return "normal"
	case "PENDING_CREATE", "PENDING_UPDATE", "PENDING_DELETE":
		return "paused"
	default:
		return status
	}
}

// mapDNSRecordStatus 映射华为云记录状态
func (a *DNSAdapter) mapDNSRecordStatus(status string) string {
	switch status {
	case "ACTIVE":
		return "enable"
	case "DISABLE":
		return "disable"
	default:
		return status
	}
}

// safeStr 安全获取字符串指针的值
func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
