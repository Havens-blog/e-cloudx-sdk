package huawei

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/billing"
	"github.com/Havens-blog/e-cloudx-sdk/common/retry"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	bss "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/bss/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/bss/v2/model"
	bssregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/bss/v2/region"
)

const (
	// defaultRegion 华为云 BSS API 默认区域（全局服务）
	defaultRegion = "cn-north-1"
	// maxRetries 最大重试次数
	maxRetries = 3
	// defaultPageSize 默认分页大小
	defaultPageSize = 100
)

// HuaweiBillingAdapter 华为云计费适配器
type HuaweiBillingAdapter struct {
	account *domain.CloudAccount
	logger  *elog.Component

	// 延迟初始化客户端，避免在注册阶段因网络不通导致失败
	clientOnce sync.Once
	client     *bss.BssClient
	clientErr  error
}

func init() {
	billing.RegisterBillingAdapter(domain.CloudProviderHuawei, newHuaweiBillingAdapter)
}

// newHuaweiBillingAdapter 创建华为云计费适配器
func newHuaweiBillingAdapter(account *domain.CloudAccount) (billing.BillingAdapter, error) {
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("huawei cloud access key id or secret is empty")
	}

	logger := elog.DefaultLogger
	if logger == nil {
		logger = elog.EgoLogger
	}

	// 延迟创建客户端，不在注册阶段就连接 IAM
	return &HuaweiBillingAdapter{
		account: account,
		logger:  logger,
	}, nil
}

// getClient 延迟初始化 BSS 客户端（仅在首次调用时创建）
func (a *HuaweiBillingAdapter) getClient() (*bss.BssClient, error) {
	a.clientOnce.Do(func() {
		// 使用 SafeBuild 避免 SDK 内部 panic
		auth, err := global.NewCredentialsBuilder().
			WithAk(a.account.AccessKeyID).
			WithSk(a.account.AccessKeySecret).
			SafeBuild()
		if err != nil {
			a.clientErr = fmt.Errorf("创建华为云全局凭证失败: %w", err)
			return
		}

		region := bssregion.CN_NORTH_1

		hcClient, err := bss.BssClientBuilder().
			WithRegion(region).
			WithCredential(auth).
			SafeBuild()
		if err != nil {
			a.clientErr = fmt.Errorf("创建华为云 BSS 客户端失败: %w", err)
			return
		}

		a.client = bss.NewBssClient(hcClient)
	})
	return a.client, a.clientErr
}

// GetProvider 获取云厂商标识
func (a *HuaweiBillingAdapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderHuawei
}

// FetchBillDetails 拉取指定时间范围的账单明细
func (a *HuaweiBillingAdapter) FetchBillDetails(ctx context.Context, params billing.FetchBillParams) ([]billing.RawBillItem, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	billingCycle := params.StartTime.Format("2006-01")

	limit := int32(params.PageSize)
	if limit <= 0 {
		limit = int32(defaultPageSize)
	}

	var allItems []billing.RawBillItem
	offset := int32(0)

	for {
		var pageItems []billing.RawBillItem
		var totalCount int32
		currentOffset := offset

		err := retry.WithBackoff(ctx, maxRetries, func() error {
			request := &model.ListCustomerselfResourceRecordDetailsRequest{
				Body: &model.QueryResRecordsDetailReq{
					Cycle:  billingCycle,
					Offset: &currentOffset,
					Limit:  &limit,
				},
			}

			response, err := client.ListCustomerselfResourceRecordDetails(request)
			if err != nil {
				if authErr := asAuthError(err); authErr != nil {
					return authErr
				}
				return err
			}

			pageItems = parseBillRecords(response, billingCycle)
			if response.TotalCount != nil {
				totalCount = *response.TotalCount
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

// parseBillRecords 解析华为云账单明细响应
func parseBillRecords(response *model.ListCustomerselfResourceRecordDetailsResponse, billingCycle string) []billing.RawBillItem {
	if response == nil || response.MonthlyRecords == nil || len(*response.MonthlyRecords) == 0 {
		return nil
	}

	records := *response.MonthlyRecords
	items := make([]billing.RawBillItem, 0, len(records))

	for _, record := range records {
		rawData := map[string]interface{}{
			"Cycle":                 billing.DerefStr(record.Cycle),
			"BillDate":              billing.DerefStr(record.BillDate),
			"BillType":              int32Val(record.BillType),
			"CustomerId":            billing.DerefStr(record.CustomerId),
			"Region":                billing.DerefStr(record.Region),
			"RegionName":            billing.DerefStr(record.RegionName),
			"CloudServiceType":      billing.DerefStr(record.CloudServiceType),
			"ResourceTypeCode":      billing.DerefStr(record.ResourceTypeCode),
			"CloudServiceTypeName":  billing.DerefStr(record.CloudServiceTypeName),
			"ResourceTypeName":      billing.DerefStr(record.ResourceTypeName),
			"ResInstanceId":         billing.DerefStr(record.ResInstanceId),
			"ResourceName":          billing.DerefStr(record.ResourceName),
			"ResourceTag":           billing.DerefStr(record.ResourceTag),
			"SkuCode":               billing.DerefStr(record.SkuCode),
			"EnterpriseProjectId":   billing.DerefStr(record.EnterpriseProjectId),
			"EnterpriseProjectName": billing.DerefStr(record.EnterpriseProjectName),
			"ChargeMode":            int32Val(record.ChargeMode),
			"TradeId":               billing.DerefStr(record.TradeId),
		}

		// 解析消费金额
		amount := 0.0
		if record.ConsumeAmount != nil {
			amount, _ = record.ConsumeAmount.Float64()
		}

		// 解析官网价
		if record.OfficialAmount != nil {
			officialAmt, _ := record.OfficialAmount.Float64()
			rawData["OfficialAmount"] = officialAmt
		}
		rawData["ConsumeAmount"] = amount

		tags := make(map[string]string)
		if tag := billing.DerefStr(record.ResourceTag); tag != "" {
			tags["raw_tag"] = tag
		}
		if epId := billing.DerefStr(record.EnterpriseProjectId); epId != "" {
			tags["enterprise_project_id"] = epId
		}
		if epName := billing.DerefStr(record.EnterpriseProjectName); epName != "" {
			tags["enterprise_project_name"] = epName
		}

		// 计费方式映射
		chargeType := "postpaid"
		if record.ChargeMode != nil {
			switch *record.ChargeMode {
			case 1: // 包年/包月
				chargeType = "prepaid"
			case 3: // 按需
				chargeType = "postpaid"
			case 10: // 预留实例
				chargeType = "reserved"
			}
		}
		rawData["ChargeType"] = chargeType

		items = append(items, billing.RawBillItem{
			Provider:     domain.CloudProviderHuawei,
			RawData:      rawData,
			ServiceType:  billing.DerefStr(record.CloudServiceType),
			ResourceID:   billing.DerefStr(record.ResInstanceId),
			ResourceName: billing.DerefStr(record.ResourceName),
			Region:       billing.DerefStr(record.Region),
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
	if sdkErr, ok := err.(*sdkerr.ServiceResponseError); ok {
		code := sdkErr.ErrorCode
		status := sdkErr.StatusCode
		if status == 401 || status == 403 ||
			code == "IAM.0001" ||
			code == "IAM.0101" ||
			strings.Contains(code, "SignatureDoesNotMatch") ||
			strings.Contains(code, "Unauthorized") ||
			strings.Contains(code, "Forbidden") ||
			strings.Contains(code, "AuthFailure") {
			return fmt.Errorf("[huawei] authentication failed (code: %s): %w", code, err)
		}
	}
	return nil
}

// isRetryable 判断错误是否可重试
func isRetryable(err error) bool {
	// 认证失败不重试
	if strings.Contains(err.Error(), "[huawei] authentication failed") {
		return false
	}

	if sdkErr, ok := err.(*sdkerr.ServiceResponseError); ok {
		status := sdkErr.StatusCode
		code := sdkErr.ErrorCode

		// 401/403 认证失败 - 不重试
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
		// 限流错误码 - 重试
		if strings.Contains(code, "Throttling") ||
			strings.Contains(code, "RateLimit") ||
			strings.Contains(code, "TooManyRequests") {
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

// int32Val 安全获取 int32 指针的值
func int32Val(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

// 确保编译时检查接口实现
var _ billing.BillingAdapter = (*HuaweiBillingAdapter)(nil)
