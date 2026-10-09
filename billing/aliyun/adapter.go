package aliyun

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/billing"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/bssopenapi"
	"github.com/gotomicro/ego/core/elog"
)

const (
	// defaultRegion 阿里云 BSS API 默认区域
	defaultRegion = "cn-hangzhou"
	// maxRetries 最大重试次数
	maxRetries = 3
)

// AliyunBillingAdapter 阿里云计费适配器
type AliyunBillingAdapter struct {
	client  *bssopenapi.Client
	account *domain.CloudAccount
	logger  *elog.Component
}

func init() {
	billing.RegisterBillingAdapter(domain.CloudProviderAliyun, newAliyunBillingAdapter)
}

// newAliyunBillingAdapter 创建阿里云计费适配器
func newAliyunBillingAdapter(account *domain.CloudAccount) (billing.BillingAdapter, error) {
	client, err := bssopenapi.NewClientWithAccessKey(
		defaultRegion,
		account.AccessKeyID,
		account.AccessKeySecret,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create aliyun bss client: %w", err)
	}

	return &AliyunBillingAdapter{
		client:  client,
		account: account,
		logger:  elog.DefaultLogger,
	}, nil
}

// GetProvider 获取云厂商标识
func (a *AliyunBillingAdapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderAliyun
}

// FetchBillDetails 拉取指定时间范围的账单明细
// 阿里云 BSS API 按月查询，自动遍历 StartTime 到 EndTime 涉及的所有月份
// 使用 NextToken 进行分页
func (a *AliyunBillingAdapter) FetchBillDetails(ctx context.Context, params billing.FetchBillParams) ([]billing.RawBillItem, error) {
	maxResults := params.PageSize
	if maxResults <= 0 {
		maxResults = 300
	}

	var allItems []billing.RawBillItem

	// 遍历 StartTime 到 EndTime 涉及的每个月
	current := time.Date(params.StartTime.Year(), params.StartTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	endMonth := time.Date(params.EndTime.Year(), params.EndTime.Month(), 1, 0, 0, 0, 0, time.UTC)

	for !current.After(endMonth) {
		billingCycle := current.Format("2006-01")
		a.logger.Info("[aliyun] fetching bill details",
			elog.String("billing_cycle", billingCycle),
			elog.String("granularity", params.Granularity),
		)

		items, err := a.fetchBillForMonth(ctx, billingCycle, maxResults, params.Granularity)
		if err != nil {
			return nil, fmt.Errorf("fetch bill for %s: %w", billingCycle, err)
		}

		a.logger.Info("[aliyun] fetched bill details",
			elog.String("billing_cycle", billingCycle),
			elog.Int("item_count", len(items)),
		)

		allItems = append(allItems, items...)
		current = current.AddDate(0, 1, 0)
	}

	return allItems, nil
}

// fetchBillForMonth 拉取单月账单，使用 NextToken 自动分页
// 当 granularity 为 DAILY 时，需要逐天设置 BillingDate 参数（阿里云 API 要求）
func (a *AliyunBillingAdapter) fetchBillForMonth(ctx context.Context, billingCycle string, maxResults int, granularity string) ([]billing.RawBillItem, error) {
	mapped := mapGranularity(granularity)

	if mapped == "DAILY" {
		return a.fetchBillForMonthDaily(ctx, billingCycle, maxResults)
	}
	return a.fetchBillPaginated(ctx, billingCycle, maxResults, mapped, "")
}

// fetchBillForMonthDaily 按天遍历拉取 DAILY 粒度账单
// 阿里云要求 Granularity=DAILY 时必须设置 BillingDate（YYYY-MM-DD），且月份需与 BillingCycle 一致
func (a *AliyunBillingAdapter) fetchBillForMonthDaily(ctx context.Context, billingCycle string, maxResults int) ([]billing.RawBillItem, error) {
	cycleTime, err := time.Parse("2006-01", billingCycle)
	if err != nil {
		return nil, fmt.Errorf("parse billing cycle %s: %w", billingCycle, err)
	}

	var allItems []billing.RawBillItem
	now := time.Now()

	// 遍历该月每一天（数据有 24h 延迟，跳过今天和未来日期）
	nextMonth := cycleTime.AddDate(0, 1, 0)
	for day := cycleTime; day.Before(nextMonth); day = day.AddDate(0, 0, 1) {
		if day.After(now.AddDate(0, 0, -1)) {
			break // 跳过昨天之后的日期（数据尚未生成）
		}

		billingDate := day.Format("2006-01-02")
		a.logger.Info("[aliyun] fetching daily bill",
			elog.String("billing_cycle", billingCycle),
			elog.String("billing_date", billingDate),
		)

		items, fetchErr := a.fetchBillPaginated(ctx, billingCycle, maxResults, "DAILY", billingDate)
		if fetchErr != nil {
			return nil, fmt.Errorf("fetch daily bill for %s: %w", billingDate, fetchErr)
		}
		allItems = append(allItems, items...)
	}

	return allItems, nil
}

// fetchBillPaginated 分页拉取账单（通用），billingDate 仅在 DAILY 粒度时设置
func (a *AliyunBillingAdapter) fetchBillPaginated(ctx context.Context, billingCycle string, maxResults int, granularity string, billingDate string) ([]billing.RawBillItem, error) {
	var items []billing.RawBillItem
	nextToken := ""

	for {
		var pageItems []billing.RawBillItem
		var respNextToken string

		err := retry.WithBackoff(ctx, maxRetries, func() error {
			request := bssopenapi.CreateDescribeInstanceBillRequest()
			request.BillingCycle = billingCycle
			request.MaxResults = requests.NewInteger(maxResults)
			request.Granularity = granularity
			if billingDate != "" {
				request.BillingDate = billingDate
			}
			if nextToken != "" {
				request.NextToken = nextToken
			}

			response, err := a.client.DescribeInstanceBill(request)
			if err != nil {
				if authErr := asAuthError(err); authErr != nil {
					return authErr
				}
				return err
			}

			a.logger.Info("[aliyun] DescribeInstanceBill response",
				elog.String("billing_cycle", billingCycle),
				elog.String("billing_date", billingDate),
				elog.String("granularity", granularity),
				elog.Int("total_count", response.Data.TotalCount),
				elog.Int("item_count", len(response.Data.Items)),
				elog.String("next_token", response.Data.NextToken),
				elog.String("code", response.Code),
				elog.String("message", response.Message),
				elog.String("success", fmt.Sprintf("%v", response.Success)),
			)

			respNextToken = response.Data.NextToken

			for _, item := range response.Data.Items {
				rawData := map[string]interface{}{
					"InstanceID":            item.InstanceID,
					"InstanceSpec":          item.InstanceSpec,
					"ProductCode":           item.ProductCode,
					"ProductType":           item.ProductType,
					"ProductName":           item.ProductName,
					"SubscriptionType":      item.SubscriptionType,
					"PretaxAmount":          item.PretaxAmount,
					"PretaxGrossAmount":     item.PretaxGrossAmount,
					"InvoiceDiscount":       item.InvoiceDiscount,
					"DeductedByCoupons":     item.DeductedByCoupons,
					"DeductedByCashCoupons": item.DeductedByCashCoupons,
					"DeductedByPrepaidCard": item.DeductedByPrepaidCard,
					"PaymentAmount":         item.PaymentAmount,
					"Region":                item.Region,
					"ResourceGroup":         item.ResourceGroup,
					"NickName":              item.NickName,
					"BillingDate":           item.BillingDate,
					"CostUnit":              item.CostUnit,
					"PipCode":               item.PipCode,
					"ServicePeriod":         item.ServicePeriod,
					"BillingType":           item.BillingType,
					"Tag":                   item.Tag,
				}

				tags := make(map[string]string)
				if item.ResourceGroup != "" {
					tags["resource_group"] = item.ResourceGroup
				}
				if item.CostUnit != "" {
					tags["cost_unit"] = item.CostUnit
				}
				if item.Tag != "" {
					tags["raw_tag"] = item.Tag
				}

				pageItems = append(pageItems, billing.RawBillItem{
					Provider:     domain.CloudProviderAliyun,
					RawData:      rawData,
					ServiceType:  item.ProductCode,
					ResourceID:   item.InstanceID,
					ResourceName: item.NickName,
					Region:       item.Region,
					Amount:       item.PretaxAmount,
					Currency:     "CNY",
					BillingCycle: billingCycle,
					Tags:         tags,
				})
			}

			return nil
		}, isRetryable)

		if err != nil {
			return nil, err
		}

		items = append(items, pageItems...)

		if respNextToken == "" {
			break
		}
		nextToken = respNextToken
	}

	return items, nil
}

// mapGranularity 将通用粒度映射为阿里云 API 粒度参数
func mapGranularity(granularity string) string {
	switch strings.ToLower(granularity) {
	case "daily":
		return "DAILY"
	case "monthly":
		return "MONTHLY"
	default:
		return "MONTHLY"
	}
}

// asAuthError 检查是否为认证失败错误，返回格式化的错误信息
func asAuthError(err error) error {
	var serverErr *sdkerrors.ServerError
	if stderrors.As(err, &serverErr) {
		code := serverErr.ErrorCode()
		httpStatus := serverErr.HttpStatus()
		if httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden ||
			code == "InvalidAccessKeyId.NotFound" || code == "SignatureDoesNotMatch" ||
			code == "Forbidden.RAM" {
			return fmt.Errorf("[aliyun] authentication failed (code: %s): %w", code, err)
		}
	}
	return nil
}

// isRetryable 判断错误是否可重试
func isRetryable(err error) bool {
	// 认证失败不重试
	if strings.Contains(err.Error(), "[aliyun] authentication failed") {
		return false
	}

	var serverErr *sdkerrors.ServerError
	if stderrors.As(err, &serverErr) {
		httpStatus := serverErr.HttpStatus()
		// 429 限流 - 重试
		if httpStatus == http.StatusTooManyRequests {
			return true
		}
		// 401/403 认证失败 - 不重试
		if httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden {
			return false
		}
		// 5xx 服务端错误 - 重试
		if httpStatus >= http.StatusInternalServerError {
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
