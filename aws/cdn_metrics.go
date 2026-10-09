package aws

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// GetDomainMetrics AWS CloudFront 未实现按域名/分发的日级指标查询。
// CloudFront 自身 API 不提供指标端点,逐日带宽/流量指标需走 CloudWatch
// (Namespaced "AWS/CloudFront",按 DistributionId 维度),而仓库当前未引入
// cloudwatch SDK。按约实现为返回空切片(nil error),采集执行器据此跳过
// 该厂商(不视为失败)。
func (a *CDNAdapter) GetDomainMetrics(ctx context.Context, domainName, domainID string, startDate, endDate string) ([]types.CDNMetric, error) {
	return []types.CDNMetric{}, nil
}
