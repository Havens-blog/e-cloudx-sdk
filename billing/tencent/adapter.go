package tencent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/billing"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	tcbilling "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/billing/v20180709"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

const (
	maxRetries      = 3
	defaultPageSize = 100
)

type TencentBillingAdapter struct {
	client  *tcbilling.Client
	account *domain.CloudAccount
	logger  *elog.Component
}

func init() {
	billing.RegisterBillingAdapter(domain.CloudProviderTencent, newTencentBillingAdapter)
}

func newTencentBillingAdapter(account *domain.CloudAccount) (billing.BillingAdapter, error) {
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("tencent cloud secret id or secret key is empty")
	}
	credential := common.NewCredential(account.AccessKeyID, account.AccessKeySecret)
	cpf := profile.NewClientProfile()
	client, err := tcbilling.NewClient(credential, "", cpf)
	if err != nil {
		return nil, fmt.Errorf("failed to create tencent billing client: %w", err)
	}
	return &TencentBillingAdapter{client: client, account: account, logger: elog.DefaultLogger}, nil
}

func (a *TencentBillingAdapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderTencent
}

func (a *TencentBillingAdapter) FetchBillDetails(ctx context.Context, params billing.FetchBillParams) ([]billing.RawBillItem, error) {
	billingCycle := params.StartTime.Format("2006-01")
	limit := uint64(params.PageSize)
	if limit <= 0 {
		limit = uint64(defaultPageSize)
	}
	var allItems []billing.RawBillItem
	offset := uint64(0)
	for {
		var pageItems []billing.RawBillItem
		var totalCount uint64
		currentOffset := offset
		err := retry.WithBackoff(ctx, maxRetries, func() error {
			request := tcbilling.NewDescribeBillDetailRequest()
			request.Month = &billingCycle
			request.Offset = &currentOffset
			request.Limit = &limit
			needRecordNum := int64(1)
			request.NeedRecordNum = &needRecordNum
			response, err := a.client.DescribeBillDetail(request)
			if err != nil {
				if authErr := asAuthError(err); authErr != nil {
					return authErr
				}
				return err
			}
			pageItems = parseBillDetails(response, billingCycle)
			if response.Response != nil && response.Response.Total != nil {
				totalCount = *response.Response.Total
			}
			return nil
		}, isRetryable)
		if err != nil {
			return nil, err
		}
		allItems = append(allItems, pageItems...)
		offset += limit
		if offset >= totalCount || len(pageItems) == 0 {
			break
		}
	}
	return allItems, nil
}

func parseBillDetails(response *tcbilling.DescribeBillDetailResponse, billingCycle string) []billing.RawBillItem {
	if response == nil || response.Response == nil || len(response.Response.DetailSet) == 0 {
		return nil
	}
	items := make([]billing.RawBillItem, 0, len(response.Response.DetailSet))
	for _, detail := range response.Response.DetailSet {
		rawData := map[string]interface{}{
			"BusinessCode":     billing.DerefStr(detail.BusinessCode),
			"BusinessCodeName": billing.DerefStr(detail.BusinessCodeName),
			"ProductCode":      billing.DerefStr(detail.ProductCode),
			"ProductCodeName":  billing.DerefStr(detail.ProductCodeName),
			"ActionType":       billing.DerefStr(detail.ActionType),
			"ActionTypeName":   billing.DerefStr(detail.ActionTypeName),
			"RegionId":         billing.DerefStr(detail.RegionId),
			"RegionName":       billing.DerefStr(detail.RegionName),
			"ZoneName":         billing.DerefStr(detail.ZoneName),
			"ResourceId":       billing.DerefStr(detail.ResourceId),
			"ResourceName":     billing.DerefStr(detail.ResourceName),
			"PayModeName":      billing.DerefStr(detail.PayModeName),
			"ProjectName":      billing.DerefStr(detail.ProjectName),
			"OrderId":          billing.DerefStr(detail.OrderId),
			"BillId":           billing.DerefStr(detail.BillId),
			"PayTime":          billing.DerefStr(detail.PayTime),
			"FeeBeginTime":     billing.DerefStr(detail.FeeBeginTime),
			"FeeEndTime":       billing.DerefStr(detail.FeeEndTime),
			"PayerUin":         billing.DerefStr(detail.PayerUin),
			"OwnerUin":         billing.DerefStr(detail.OwnerUin),
			"OperateUin":       billing.DerefStr(detail.OperateUin),
			"BillDay":          billing.DerefStr(detail.BillDay),
			"BillMonth":        billing.DerefStr(detail.BillMonth),
		}
		amount := 0.0
		if detail.ComponentSet != nil {
			for _, comp := range detail.ComponentSet {
				if comp.RealCost != nil {
					amount += parseFloat(billing.DerefStr(comp.RealCost))
				}
			}
		}
		rawData["TotalCost"] = amount
		tags := make(map[string]string)
		if detail.Tags != nil {
			for _, tag := range detail.Tags {
				if tag.TagKey != nil && tag.TagValue != nil {
					tags[*tag.TagKey] = *tag.TagValue
				}
			}
		}
		if project := billing.DerefStr(detail.ProjectName); project != "" {
			tags["project"] = project
		}
		chargeType := "postpaid"
		if payMode := billing.DerefStr(detail.PayModeName); payMode != "" {
			if strings.Contains(payMode, "包年包月") || strings.Contains(payMode, "prePay") {
				chargeType = "prepaid"
			}
			rawData["ChargeType"] = chargeType
		}
		items = append(items, billing.RawBillItem{
			Provider:     domain.CloudProviderTencent,
			RawData:      rawData,
			ServiceType:  billing.DerefStr(detail.BusinessCode),
			ResourceID:   billing.DerefStr(detail.ResourceId),
			ResourceName: billing.DerefStr(detail.ResourceName),
			Region:       billing.DerefStr(detail.RegionId),
			Amount:       amount,
			Currency:     "CNY",
			BillingCycle: billingCycle,
			Tags:         tags,
		})
	}
	return items
}

func asAuthError(err error) error {
	if sdkErr, ok := err.(*tcerr.TencentCloudSDKError); ok {
		code := sdkErr.Code
		if strings.HasPrefix(code, "AuthFailure") || code == "UnauthorizedOperation" {
			return fmt.Errorf("[tencent] authentication failed (code: %s): %w", code, err)
		}
	}
	return nil
}

func isRetryable(err error) bool {
	if strings.Contains(err.Error(), "[tencent] authentication failed") {
		return false
	}
	if sdkErr, ok := err.(*tcerr.TencentCloudSDKError); ok {
		code := sdkErr.Code
		if strings.HasPrefix(code, "AuthFailure") || code == "UnauthorizedOperation" {
			return false
		}
		if code == "RequestLimitExceeded" || code == "LimitExceeded" ||
			strings.Contains(code, "Throttling") || strings.Contains(code, "TooManyRequests") {
			return true
		}
		if strings.HasPrefix(code, "InternalError") {
			return true
		}
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") || strings.Contains(errMsg, "i/o timeout") ||
		strings.Contains(errMsg, "net/http") {
		return true
	}
	return false
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

var _ billing.BillingAdapter = (*TencentBillingAdapter)(nil)
