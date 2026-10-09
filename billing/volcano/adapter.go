package volcano

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/billing"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	volcbilling "github.com/volcengine/volcengine-go-sdk/service/billing"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
	"github.com/volcengine/volcengine-go-sdk/volcengine/volcengineerr"
)

const (
	// defaultRegion 火山引擎计费 API 默认区域
	defaultRegion = "cn-beijing"
	// maxRetries 最大重试次数
	maxRetries = 3
	// defaultPageSize 默认分页大小
	defaultPageSize = 100
)

// VolcanoBillingAdapter 火山引擎计费适配器
type VolcanoBillingAdapter struct {
	client  *volcbilling.BILLING
	account *domain.CloudAccount
	logger  *elog.Component
}

func init() {
	billing.RegisterBillingAdapter(domain.CloudProviderVolcano, newVolcanoBillingAdapter)
	billing.RegisterBillingAdapter(domain.CloudProviderVolcengine, newVolcanoBillingAdapter)
}

// newVolcanoBillingAdapter 创建火山引擎计费适配器
func newVolcanoBillingAdapter(account *domain.CloudAccount) (billing.BillingAdapter, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(
			account.AccessKeyID,
			account.AccessKeySecret,
			"",
		)).
		WithRegion(defaultRegion)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create volcano billing session: %w", err)
	}

	client := volcbilling.New(sess)

	return &VolcanoBillingAdapter{
		client:  client,
		account: account,
		logger:  elog.DefaultLogger,
	}, nil
}

// GetProvider 获取云厂商标识
func (a *VolcanoBillingAdapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderVolcano
}

// FetchBillDetails 拉取指定时间范围的账单明细
func (a *VolcanoBillingAdapter) FetchBillDetails(ctx context.Context, params billing.FetchBillParams) ([]billing.RawBillItem, error) {
	billingCycle := params.StartTime.Format("2006-01")

	limit := int32(params.PageSize)
	if limit <= 0 {
		limit = int32(defaultPageSize)
	}

	var allItems []billing.RawBillItem
	offset := int32(0)
	needRecordNum := int32(1)

	for {
		var pageItems []billing.RawBillItem
		var total int32
		currentOffset := offset

		err := retry.WithBackoff(ctx, maxRetries, func() error {
			input := &volcbilling.ListBillDetailInput{}
			input.SetBillPeriod(billingCycle)
			input.SetLimit(limit)
			input.SetOffset(currentOffset)
			input.SetNeedRecordNum(needRecordNum)

			response, err := a.client.ListBillDetailWithContext(ctx, input)
			if err != nil {
				if authErr := asAuthError(err); authErr != nil {
					return authErr
				}
				return err
			}

			pageItems = parseBillDetailItems(response, billingCycle)
			if response.Total != nil {
				total = *response.Total
			}
			return nil
		}, isRetryable)

		if err != nil {
			return nil, err
		}

		allItems = append(allItems, pageItems...)
		offset += limit

		if offset >= total || len(pageItems) == 0 {
			break
		}
	}

	return allItems, nil
}

// parseBillDetailItems 解析火山引擎账单明细响应
func parseBillDetailItems(output *volcbilling.ListBillDetailOutput, billingCycle string) []billing.RawBillItem {
	if output == nil || len(output.List) == 0 {
		return nil
	}

	items := make([]billing.RawBillItem, 0, len(output.List))
	for _, item := range output.List {
		rawData := map[string]interface{}{
			"BillDetailId":       billing.DerefStr(item.BillDetailId),
			"BillID":             billing.DerefStr(item.BillID),
			"BillPeriod":         billing.DerefStr(item.BillPeriod),
			"Product":            billing.DerefStr(item.Product),
			"ProductZh":          billing.DerefStr(item.ProductZh),
			"InstanceNo":         billing.DerefStr(item.InstanceNo),
			"InstanceName":       billing.DerefStr(item.InstanceName),
			"Region":             billing.DerefStr(item.Region),
			"RegionCode":         billing.DerefStr(item.RegionCode),
			"BillingMode":        billing.DerefStr(item.BillingMode),
			"ExpenseBeginTime":   billing.DerefStr(item.ExpenseBeginTime),
			"ExpenseEndTime":     billing.DerefStr(item.ExpenseEndTime),
			"PayableAmount":      billing.DerefStr(item.PayableAmount),
			"PaidAmount":         billing.DerefStr(item.PaidAmount),
			"OriginalBillAmount": billing.DerefStr(item.OriginalBillAmount),
			"DiscountBillAmount": billing.DerefStr(item.DiscountBillAmount),
			"CouponAmount":       billing.DerefStr(item.CouponAmount),
			"Currency":           billing.DerefStr(item.Currency),
			"Project":            billing.DerefStr(item.Project),
			"Tag":                billing.DerefStr(item.Tag),
			"OwnerID":            billing.DerefStr(item.OwnerID),
			"SellingMode":        billing.DerefStr(item.SellingMode),
			"SubjectName":        billing.DerefStr(item.SubjectName),
		}

		amount := parseFloat(billing.DerefStr(item.PayableAmount))

		region := billing.DerefStr(item.Region)
		if region == "" {
			region = billing.DerefStr(item.RegionCode)
		}

		tags := make(map[string]string)
		if project := billing.DerefStr(item.Project); project != "" {
			tags["project"] = project
		}
		if tag := billing.DerefStr(item.Tag); tag != "" {
			tags["raw_tag"] = tag
		}

		items = append(items, billing.RawBillItem{
			Provider:     domain.CloudProviderVolcano,
			RawData:      rawData,
			ServiceType:  billing.DerefStr(item.Product),
			ResourceID:   billing.DerefStr(item.InstanceNo),
			ResourceName: billing.DerefStr(item.InstanceName),
			Region:       region,
			Amount:       amount,
			Currency:     "CNY",
			BillingCycle: billingCycle,
			Tags:         tags,
		})
	}

	return items
}

// asAuthError 检查是否为认证失败错误，返回格式化的错误信息
func asAuthError(err error) error {
	var volcErr volcengineerr.Error
	if stderrors.As(err, &volcErr) {
		code := volcErr.Code()
		if code == "SignatureDoesNotMatch" ||
			code == "InvalidAccessKeyId" ||
			code == "Forbidden" ||
			code == "AuthFailure" ||
			code == "InvalidAccessKey" {
			return fmt.Errorf("[volcano] authentication failed (code: %s): %w", code, err)
		}
	}

	var reqErr volcengineerr.RequestFailure
	if stderrors.As(err, &reqErr) {
		status := reqErr.StatusCode()
		if status == 401 || status == 403 {
			code := reqErr.Code()
			return fmt.Errorf("[volcano] authentication failed (code: %s): %w", code, err)
		}
	}

	return nil
}

// isRetryable 判断错误是否可重试
func isRetryable(err error) bool {
	// 认证失败不重试
	if strings.Contains(err.Error(), "[volcano] authentication failed") {
		return false
	}

	var volcErr volcengineerr.Error
	if stderrors.As(err, &volcErr) {
		code := volcErr.Code()
		// 认证/授权错误 - 不重试
		if code == "SignatureDoesNotMatch" ||
			code == "InvalidAccessKeyId" ||
			code == "Forbidden" ||
			code == "AuthFailure" ||
			code == "InvalidAccessKey" {
			return false
		}
		// 限流 - 重试
		if code == "Throttling" || code == "RequestLimitExceeded" || code == "TooManyRequests" {
			return true
		}
	}

	var reqErr volcengineerr.RequestFailure
	if stderrors.As(err, &reqErr) {
		status := reqErr.StatusCode()
		// 401/403 - 不重试
		if status == 401 || status == 403 {
			return false
		}
		// 429 限流 - 重试
		if status == 429 {
			return true
		}
		// 5xx 服务端错误 - 重试
		if status >= 500 {
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

// parseFloat 安全解析浮点数字符串
func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
