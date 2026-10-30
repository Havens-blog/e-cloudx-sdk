package logquery

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// ErrUnsupportedLogProvider 不支持的 (云, 日志类型) 组合。
var ErrUnsupportedLogProvider = errors.New("unsupported log provider")

// LogSource 日志源(域名 / LB 实例 / WAF 防护对象)。
type LogSource struct {
	Cloud       domain.CloudProvider `json:"cloud"`
	AccountID   string               `json:"account_id"`
	AccountName string               `json:"account_name"`
	Region      string               `json:"region"`
	LogType     LogType              `json:"log_type"`
	ResourceID  string               `json:"resource_id"` // 域名 / LB 实例 ID / 分发 ID
	Name        string               `json:"name"`        // 展示名
	Enabled     bool                 `json:"enabled"`     // 投递是否开启(查询可用性)
	Stale       bool                 `json:"stale"`       // 近 staleWindow 无新数据(可能停采;历史仍可查)
	Note        string               `json:"note"`        // 未开启原因 / 延迟特征等引导信息
}

// SearchParams 联邦查询参数(单账号单云;service 层解析账号后逐源下发)。
type SearchParams struct {
	StartTime int64         `json:"start_time"` // Unix 毫秒 UTC(含)
	EndTime   int64         `json:"end_time"`   // Unix 毫秒 UTC(含)
	Query     string        `json:"query"`      // 原生检索式透传(各云语法由 provider 翻译;空=全量)
	Limit     int           `json:"limit"`      // 单源上限(ADR D4:窗口内每源上限+截断标记)
	Offset    int64         `json:"offset"`     // 可选:云引擎侧游标起点(导出分页用;0=从头)
	Regions   []string      `json:"regions"`    // 可选:限定区域
	Resources []string      `json:"resources"`  // 可选:限定资源(域名/LB ID/分发 ID)
	Filters   []FieldFilter `json:"filters"`    // 字段筛选(映射后统一字段过滤,AND 叠加)
}

// LogProvider 日志源适配器接口(照 cloudx/billing 模式:每云每类型注册,
// 按账号凭证构造)。
type LogProvider interface {
	// Cloud 云厂商标识。
	Cloud() domain.CloudProvider
	// LogType 日志类型。
	LogType() LogType
	// ListLogSources 枚举该账号该类型的日志源(含"未开启投递"状态)。
	ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]LogSource, error)
	// Search 查询窗口内日志并映射为统一模型;entries 按时间倒序。
	// 单源失败应返回 error(由联邦编排层隔离,不阻塞其他源)。
	Search(ctx context.Context, account *domain.CloudAccount, params SearchParams) ([]LogEntry, error)
}

// LogExporter 可选导出能力:逐源分页流式拉取,不设 provider 级截断。
// emit 每批回调映射后的条目;返回值 = 已导出总条数(跨源求和)。
// 未实现该能力的 provider(如 S3 文件类)由调用方回退到 Search 采样。
type LogExporter interface {
	Export(ctx context.Context, account *domain.CloudAccount, params SearchParams, emit func([]LogEntry) error) (int, error)
}

// ProviderCreator 按云账号构造 provider(凭证解密后的 domain.CloudAccount)。
type ProviderCreator func(account *domain.CloudAccount) (LogProvider, error)

// providerKey 注册表键。
type providerKey struct {
	cloud   domain.CloudProvider
	logType LogType
}

var providerRegistry = &registry{
	creators: make(map[providerKey]ProviderCreator),
}

type registry struct {
	mu       sync.RWMutex
	creators map[providerKey]ProviderCreator
}

// RegisterProvider 注册 (云, 日志类型) 的 provider 构造函数。
// 各云包在 init() 中调用(照 billing.RegisterBillingAdapter 先例)。
func RegisterProvider(cloud domain.CloudProvider, logType LogType, creator ProviderCreator) {
	providerRegistry.mu.Lock()
	defer providerRegistry.mu.Unlock()
	providerRegistry.creators[providerKey{cloud: cloud, logType: logType}] = creator
}

// GetProvider 获取 (云, 日志类型) 的 provider 构造函数。
func GetProvider(cloud domain.CloudProvider, logType LogType) (ProviderCreator, error) {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	creator, ok := providerRegistry.creators[providerKey{cloud: cloud, logType: logType}]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrUnsupportedLogProvider, cloud, logType)
	}
	return creator, nil
}

// IsProviderRegistered 检查组合是否已注册。
func IsProviderRegistered(cloud domain.CloudProvider, logType LogType) bool {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	_, ok := providerRegistry.creators[providerKey{cloud: cloud, logType: logType}]
	return ok
}

// RegisteredProviderKeys 已注册组合清单(sources 接口展示用)。
func RegisteredProviderKeys() []providerKey {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	keys := make([]providerKey, 0, len(providerRegistry.creators))
	for k := range providerRegistry.creators {
		keys = append(keys, k)
	}
	return keys
}
