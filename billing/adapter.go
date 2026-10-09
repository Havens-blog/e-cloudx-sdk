package billing

import (
	"context"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// BillingAdapter 云厂商计费适配器接口
type BillingAdapter interface {
	// GetProvider 获取云厂商标识
	GetProvider() domain.CloudProvider

	// FetchBillDetails 拉取指定时间范围的账单明细
	// startTime/endTime 为计费周期的起止时间（UTC）
	// 返回原始账单数据列表，每条记录对应一笔费用明细
	FetchBillDetails(ctx context.Context, params FetchBillParams) ([]RawBillItem, error)
}

// FetchBillParams 账单拉取参数
type FetchBillParams struct {
	AccountID   string    // 云账号 ID
	StartTime   time.Time // 起始时间
	EndTime     time.Time // 结束时间
	PageNumber  int       // 分页页码
	PageSize    int       // 分页大小
	Granularity string    // 粒度: "daily" | "monthly"
}

// RawBillItem 原始账单条目（云厂商原始格式）
type RawBillItem struct {
	Provider     domain.CloudProvider   // 云厂商
	RawData      map[string]interface{} // 原始数据（保留完整字段用于审计）
	ServiceType  string                 // 云厂商原始服务类型
	ResourceID   string                 // 资源 ID
	ResourceName string                 // 资源名称
	Region       string                 // 地域
	Amount       float64                // 费用金额
	Currency     string                 // 币种
	BillingCycle string                 // 计费周期
	Tags         map[string]string      // 资源标签
}

// BillingAdapterCreator 计费适配器创建函数
type BillingAdapterCreator func(account *domain.CloudAccount) (BillingAdapter, error)
