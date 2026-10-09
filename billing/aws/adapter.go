package aws

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/billing"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/smithy-go"
	"github.com/gotomicro/ego/core/elog"
)

const (
	// defaultRegion AWS Cost Explorer API 默认区域
	defaultRegion = "us-east-1"
	// maxRetries 最大重试次数
	maxRetries = 3
)

// AWSBillingAdapter AWS 计费适配器
type AWSBillingAdapter struct {
	client  *costexplorer.Client
	account *domain.CloudAccount
	logger  *elog.Component
}

func init() {
	billing.RegisterBillingAdapter(domain.CloudProviderAWS, newAWSBillingAdapter)
}

// newAWSBillingAdapter 创建 AWS 计费适配器
func newAWSBillingAdapter(account *domain.CloudAccount) (billing.BillingAdapter, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(defaultRegion),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			account.AccessKeyID,
			account.AccessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %w", err)
	}

	client := costexplorer.NewFromConfig(cfg)

	return &AWSBillingAdapter{
		client:  client,
		account: account,
		logger:  elog.DefaultLogger,
	}, nil
}

// GetProvider 获取云厂商标识
func (a *AWSBillingAdapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderAWS
}

// FetchBillDetails 拉取指定时间范围的账单明细
func (a *AWSBillingAdapter) FetchBillDetails(ctx context.Context, params billing.FetchBillParams) ([]billing.RawBillItem, error) {
	billingCycle := params.StartTime.Format("2006-01")
	startDate := params.StartTime.Format("2006-01-02")
	endDate := params.EndTime.Format("2006-01-02")

	var items []billing.RawBillItem
	var nextPageToken *string

	for {
		var pageItems []billing.RawBillItem
		var pageNextToken *string

		err := retry.WithBackoff(ctx, maxRetries, func() error {
			input := &costexplorer.GetCostAndUsageInput{
				TimePeriod: &cetypes.DateInterval{
					Start: &startDate,
					End:   &endDate,
				},
				Granularity: mapGranularity(params.Granularity),
				Metrics:     []string{"UnblendedCost", "UsageQuantity"},
				GroupBy: []cetypes.GroupDefinition{
					{
						Type: cetypes.GroupDefinitionTypeDimension,
						Key:  strPtr("SERVICE"),
					},
					{
						Type: cetypes.GroupDefinitionTypeDimension,
						Key:  strPtr("REGION"),
					},
				},
				NextPageToken: nextPageToken,
			}

			response, err := a.client.GetCostAndUsage(ctx, input)
			if err != nil {
				if authErr := asAuthError(err); authErr != nil {
					return authErr
				}
				return err
			}

			pageItems = parseResultsByTime(response.ResultsByTime, billingCycle)
			pageNextToken = response.NextPageToken
			return nil
		}, isRetryable)

		if err != nil {
			return nil, err
		}

		items = append(items, pageItems...)

		if pageNextToken == nil {
			break
		}
		nextPageToken = pageNextToken
	}

	return items, nil
}

// parseResultsByTime 解析 AWS Cost Explorer 响应中的 ResultsByTime
func parseResultsByTime(results []cetypes.ResultByTime, billingCycle string) []billing.RawBillItem {
	var items []billing.RawBillItem

	for _, result := range results {
		periodStart := ""
		periodEnd := ""
		if result.TimePeriod != nil {
			if result.TimePeriod.Start != nil {
				periodStart = *result.TimePeriod.Start
			}
			if result.TimePeriod.End != nil {
				periodEnd = *result.TimePeriod.End
			}
		}

		for _, group := range result.Groups {
			serviceName := ""
			region := ""
			if len(group.Keys) > 0 {
				serviceName = group.Keys[0]
			}
			if len(group.Keys) > 1 {
				region = group.Keys[1]
			}

			amount := 0.0
			usageQty := 0.0
			currency := "USD"

			if cost, ok := group.Metrics["UnblendedCost"]; ok {
				if cost.Amount != nil {
					amount, _ = strconv.ParseFloat(*cost.Amount, 64)
				}
				if cost.Unit != nil {
					currency = *cost.Unit
				}
			}
			if usage, ok := group.Metrics["UsageQuantity"]; ok {
				if usage.Amount != nil {
					usageQty, _ = strconv.ParseFloat(*usage.Amount, 64)
				}
			}

			rawData := map[string]any{
				"Service":       serviceName,
				"Region":        region,
				"PeriodStart":   periodStart,
				"PeriodEnd":     periodEnd,
				"UnblendedCost": amount,
				"UsageQuantity": usageQty,
				"Currency":      currency,
			}

			items = append(items, billing.RawBillItem{
				Provider:     domain.CloudProviderAWS,
				RawData:      rawData,
				ServiceType:  serviceName,
				ResourceID:   "",
				ResourceName: serviceName,
				Region:       region,
				Amount:       amount,
				Currency:     currency,
				BillingCycle: billingCycle,
				Tags:         make(map[string]string),
			})
		}
	}

	return items
}

// mapGranularity 将通用粒度映射为 AWS Cost Explorer 粒度参数
func mapGranularity(granularity string) cetypes.Granularity {
	switch strings.ToLower(granularity) {
	case "daily":
		return cetypes.GranularityDaily
	case "monthly":
		return cetypes.GranularityMonthly
	default:
		return cetypes.GranularityMonthly
	}
}

// asAuthError 检查是否为认证失败错误，返回格式化的错误信息
func asAuthError(err error) error {
	var apiErr smithy.APIError
	if stderrors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "UnrecognizedClientException" ||
			code == "InvalidClientTokenId" ||
			code == "SignatureDoesNotMatch" ||
			code == "AccessDeniedException" ||
			code == "ExpiredTokenException" {
			return fmt.Errorf("[aws] authentication failed (code: %s): %w", code, err)
		}
	}

	var respErr interface{ HTTPStatusCode() int }
	if stderrors.As(err, &respErr) {
		status := respErr.HTTPStatusCode()
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			code := "unknown"
			var ae smithy.APIError
			if stderrors.As(err, &ae) {
				code = ae.ErrorCode()
			}
			return fmt.Errorf("[aws] authentication failed (code: %s): %w", code, err)
		}
	}

	return nil
}

// isRetryable 判断错误是否可重试
func isRetryable(err error) bool {
	// 认证失败不重试
	if strings.Contains(err.Error(), "[aws] authentication failed") {
		return false
	}

	var apiErr smithy.APIError
	if stderrors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		// 认证/授权错误 - 不重试
		if code == "UnrecognizedClientException" ||
			code == "InvalidClientTokenId" ||
			code == "SignatureDoesNotMatch" ||
			code == "AccessDeniedException" ||
			code == "ExpiredTokenException" {
			return false
		}
		// 限流 - 重试
		if code == "LimitExceededException" || code == "Throttling" || code == "ThrottlingException" {
			return true
		}
	}

	var respErr interface{ HTTPStatusCode() int }
	if stderrors.As(err, &respErr) {
		status := respErr.HTTPStatusCode()
		// 401/403 - 不重试
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return false
		}
		// 429 限流 - 重试
		if status == http.StatusTooManyRequests {
			return true
		}
		// 5xx 服务端错误 - 重试
		if status >= http.StatusInternalServerError {
			return true
		}
	}

	// 网络/超时错误 - 重试
	errMsg := err.Error()
	if strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "i/o timeout") ||
		strings.Contains(errMsg, "net/http") {
		return true
	}

	return false
}

// strPtr 返回字符串指针
func strPtr(s string) *string {
	return &s
}
