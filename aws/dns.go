package aws

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/gotomicro/ego/core/elog"
)

const (
	awsDNSMaxRetries    = 3
	awsDNSBaseBackoffMs = 200
)

// DNSAdapter AWS Route53 DNS 适配器
type DNSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewDNSAdapter 创建 AWS DNS 适配器
func NewDNSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *DNSAdapter {
	return &DNSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 Route53 客户端
func (a *DNSAdapter) createClient(ctx context.Context) (*route53.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"), // Route53 是全局服务
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("aws: load config failed: %w", err)
	}
	return route53.NewFromConfig(cfg), nil
}

// retryWithBackoff 指数退避重试
func (a *DNSAdapter) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= awsDNSMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < awsDNSMaxRetries {
			backoff := time.Duration(float64(awsDNSBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("aws: DNS API failed after %d retries: %w", awsDNSMaxRetries, lastErr)
}

// ListDomains 查询托管域名列表（Route53 HostedZones）
func (a *DNSAdapter) ListDomains(ctx context.Context) ([]types.DNSDomain, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: create Route53 client failed: %w", err)
	}

	var allDomains []types.DNSDomain
	var marker *string

	for {
		input := &route53.ListHostedZonesInput{
			Marker: marker,
		}

		var output *route53.ListHostedZonesOutput
		err = a.retryWithBackoff(func() error {
			var e error
			output, e = client.ListHostedZones(ctx, input)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("aws: list hosted zones failed: %w", err)
		}

		for _, zone := range output.HostedZones {
			zoneID := cleanHostedZoneID(awssdk.ToString(zone.Id))
			domainName := strings.TrimSuffix(awssdk.ToString(zone.Name), ".")

			allDomains = append(allDomains, types.DNSDomain{
				DomainID:    zoneID,
				DomainName:  domainName,
				RecordCount: int64(awssdk.ToInt64(zone.ResourceRecordSetCount)),
				Status:      "normal",
			})
		}

		if !output.IsTruncated {
			break
		}
		marker = output.NextMarker
	}

	a.logger.Info("获取AWS Route53域名列表成功", elog.Int("count", len(allDomains)))
	return allDomains, nil
}

// ListRecords 查询域名下解析记录列表
func (a *DNSAdapter) ListRecords(ctx context.Context, domain types.DNSDomain) ([]types.DNSRecord, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: create Route53 client failed: %w", err)
	}

	// ListRecords 的 domain 携带 DomainName(域名) 与 DomainID(HostedZoneId);
	// Route53 查询走 HostedZoneId,记录归属写回域名名。
	hostedZoneID := domain.DomainID

	var allRecords []types.DNSRecord
	var nextName *string
	var nextType r53types.RRType
	hasNextType := false

	for {
		input := &route53.ListResourceRecordSetsInput{
			HostedZoneId: awssdk.String(hostedZoneID),
		}
		if nextName != nil {
			input.StartRecordName = nextName
		}
		if hasNextType {
			input.StartRecordType = nextType
		}

		var output *route53.ListResourceRecordSetsOutput
		err = a.retryWithBackoff(func() error {
			var e error
			output, e = client.ListResourceRecordSets(ctx, input)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("aws: list record sets for zone %s failed: %w", hostedZoneID, err)
		}

		for _, rrs := range output.ResourceRecordSets {
			recordType := string(rrs.Type)
			name := strings.TrimSuffix(awssdk.ToString(rrs.Name), ".")

			// 提取主机记录（去掉域名后缀部分）
			rr := name

			ttl := 0
			if rrs.TTL != nil {
				ttl = int(*rrs.TTL)
			}

			// Route53 一条 ResourceRecordSet 可能包含多个值
			if rrs.ResourceRecords != nil {
				for i, record := range rrs.ResourceRecords {
					allRecords = append(allRecords, types.DNSRecord{
						RecordID: fmt.Sprintf("%s_%s_%d", name, recordType, i),
						Domain:   domain.DomainName,
						RR:       rr,
						Type:     recordType,
						Value:    awssdk.ToString(record.Value),
						TTL:      ttl,
						Line:     "default",
						Status:   "enable",
					})
				}
			}

			// Alias 记录
			if rrs.AliasTarget != nil {
				allRecords = append(allRecords, types.DNSRecord{
					RecordID: fmt.Sprintf("%s_%s_alias", name, recordType),
					Domain:   domain.DomainName,
					RR:       rr,
					Type:     recordType,
					Value:    awssdk.ToString(rrs.AliasTarget.DNSName),
					TTL:      ttl,
					Line:     "default",
					Status:   "enable",
				})
			}
		}

		if !output.IsTruncated {
			break
		}
		nextName = output.NextRecordName
		if output.NextRecordType != "" {
			nextType = output.NextRecordType
			hasNextType = true
		}
	}

	return allRecords, nil
}

// GetRecord 查询单条解析记录详情
func (a *DNSAdapter) GetRecord(ctx context.Context, domain, recordID string) (*types.DNSRecord, error) {
	records, err := a.ListRecords(ctx, types.DNSDomain{DomainID: domain})
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.RecordID == recordID {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("aws: DNS record %s not found in zone %s", recordID, domain)
}

// CreateRecord 创建解析记录
func (a *DNSAdapter) CreateRecord(ctx context.Context, domain string, req types.CreateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: create Route53 client failed: %w", err)
	}

	fqdn := req.RR + "."
	ttl := int64(req.TTL)
	if ttl == 0 {
		ttl = 300
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awssdk.String(domain),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionCreate,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: awssdk.String(fqdn),
						Type: r53types.RRType(req.Type),
						TTL:  awssdk.Int64(ttl),
						ResourceRecords: []r53types.ResourceRecord{
							{Value: awssdk.String(req.Value)},
						},
					},
				},
			},
		},
	}

	err = a.retryWithBackoff(func() error {
		_, e := client.ChangeResourceRecordSets(ctx, input)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("aws: create DNS record failed: %w", err)
	}

	recordID := fmt.Sprintf("%s_%s_0", strings.TrimSuffix(fqdn, "."), req.Type)
	return &types.DNSRecord{
		RecordID: recordID,
		Domain:   domain,
		RR:       req.RR,
		Type:     req.Type,
		Value:    req.Value,
		TTL:      int(ttl),
		Line:     "default",
		Status:   "enable",
	}, nil
}

// UpdateRecord 修改解析记录
func (a *DNSAdapter) UpdateRecord(ctx context.Context, domain, recordID string, req types.UpdateDNSRecordRequest) (*types.DNSRecord, error) {
	client, err := a.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: create Route53 client failed: %w", err)
	}

	fqdn := req.RR + "."
	ttl := int64(req.TTL)
	if ttl == 0 {
		ttl = 300
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awssdk.String(domain),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: awssdk.String(fqdn),
						Type: r53types.RRType(req.Type),
						TTL:  awssdk.Int64(ttl),
						ResourceRecords: []r53types.ResourceRecord{
							{Value: awssdk.String(req.Value)},
						},
					},
				},
			},
		},
	}

	err = a.retryWithBackoff(func() error {
		_, e := client.ChangeResourceRecordSets(ctx, input)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("aws: update DNS record %s failed: %w", recordID, err)
	}

	return &types.DNSRecord{
		RecordID: recordID,
		Domain:   domain,
		RR:       req.RR,
		Type:     req.Type,
		Value:    req.Value,
		TTL:      int(ttl),
		Line:     "default",
		Status:   "enable",
	}, nil
}

// DeleteRecord 删除解析记录
func (a *DNSAdapter) DeleteRecord(ctx context.Context, domain, recordID string) error {
	// 先获取记录详情
	record, err := a.GetRecord(ctx, domain, recordID)
	if err != nil {
		return fmt.Errorf("aws: get record before delete failed: %w", err)
	}

	client, err := a.createClient(ctx)
	if err != nil {
		return fmt.Errorf("aws: create Route53 client failed: %w", err)
	}

	fqdn := record.RR + "."
	ttl := int64(record.TTL)
	if ttl == 0 {
		ttl = 300
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awssdk.String(domain),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionDelete,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: awssdk.String(fqdn),
						Type: r53types.RRType(record.Type),
						TTL:  awssdk.Int64(ttl),
						ResourceRecords: []r53types.ResourceRecord{
							{Value: awssdk.String(record.Value)},
						},
					},
				},
			},
		},
	}

	return a.retryWithBackoff(func() error {
		_, e := client.ChangeResourceRecordSets(ctx, input)
		return e
	})
}

// cleanHostedZoneID 清理 HostedZone ID（去掉 /hostedzone/ 前缀）
func cleanHostedZoneID(id string) string {
	return strings.TrimPrefix(id, "/hostedzone/")
}
