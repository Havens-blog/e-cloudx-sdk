package volcano

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/waf"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// WAFAdapter 火山引擎WAF适配器
type WAFAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewWAFAdapter 创建WAF适配器
func NewWAFAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *WAFAdapter {
	return &WAFAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建WAF客户端
func (a *WAFAdapter) createClient(region string) (*waf.WAF, error) {
	if region == "" {
		region = a.defaultRegion
	}
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(region)

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}
	return waf.New(sess), nil
}

// ListInstances 获取WAF防护域名列表
func (a *WAFAdapter) ListInstances(ctx context.Context, region string) ([]types.WAFInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个WAF防护域名详情
func (a *WAFAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.WAFInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.InstanceID == instanceID {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("WAF实例不存在: %s", instanceID)
}

// ListInstancesByIDs 批量获取WAF防护域名
func (a *WAFAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.WAFInstance, error) {
	var result []types.WAFInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取WAF实例失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取实例状态
func (a *WAFAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取WAF防护域名列表
func (a *WAFAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	if region == "" {
		region = a.defaultRegion
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建WAF客户端失败: %w", err)
	}

	var allInstances []types.WAFInstance
	page := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}

	for {
		input := &waf.ListDomainInput{}
		input.SetPage(page)
		input.SetPageSize(pageSize)
		input.SetRegion(region)

		if filter != nil && filter.InstanceName != "" {
			input.SetDomain(filter.InstanceName)
		}

		output, err := client.ListDomain(input)
		if err != nil {
			// WAF 可能未开通
			a.logger.Debug("获取WAF域名列表失败(可能未开通)", elog.String("region", region), elog.FieldErr(err))
			return nil, nil
		}

		if output.Data == nil || len(output.Data) == 0 {
			break
		}

		for _, d := range output.Data {
			allInstances = append(allInstances, a.convertToInstance(d, region))
		}

		total := int32(0)
		if output.TotalCount != nil {
			total = *output.TotalCount
		}
		if int32(len(allInstances)) >= total {
			break
		}
		page++
	}

	a.logger.Info("获取火山引擎WAF域名列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// convertToInstance 转换火山引擎WAF域名为通用WAF实例
func (a *WAFAdapter) convertToInstance(d *waf.DataForListDomainOutput, region string) types.WAFInstance {
	domain := ""
	if d.Domain != nil {
		domain = *d.Domain
	}
	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}

	// 防护模式: 0=关闭 1=拦截 2=观察
	wafEnabled := false
	ccEnabled := false
	antiBotEnabled := false
	status := "active"
	// 状态映射(保守降级):SDK 的 Status 字段无公开注释,语义待 live 数据校准。
	// 当前仅把显式 0 判为 suspended,其余(含 nil)保持 active,避免误标停用。
	if d.Status != nil && *d.Status == 0 {
		status = "suspended"
	}

	if d.DefenceMode != nil && *d.DefenceMode > 0 {
		wafEnabled = true
	}
	if d.CcEnable != nil && *d.CcEnable > 0 {
		ccEnabled = true
	}
	if d.CustomBotEnable != nil && *d.CustomBotEnable > 0 {
		antiBotEnabled = true
	}

	// 防护模式: 0=关闭 1=拦截 2=观察
	protectionMode := ""
	if d.DefenceMode != nil {
		switch *d.DefenceMode {
		case 1:
			protectionMode = "block"
		case 2:
			protectionMode = "observe"
		default:
			protectionMode = "off"
		}
	}

	// 统计防护域名数
	domainCount := 0
	var protectedHosts []string
	if domain != "" {
		domainCount = 1
		protectedHosts = []string{domain}
	}

	updateTime := ""
	if d.UpdateTime != nil {
		updateTime = *d.UpdateTime
	}
	// 列表接口 ListDomain 返回的 DataForListDomainOutput 仅有 UpdateTime,没有创建时间输出
	// 字段(已核实 SDK:输入侧只有 SortByCreateTime 排序参数;GetDomainInfo 返回通用
	// map[string]interface{} 且键未文档化,不解析)。故 CreationTime 留空不填,最近更新时间
	// 并入 Description,不硬造值。
	description := ""
	if updateTime != "" {
		description = fmt.Sprintf("updated=%s", updateTime)
	}

	// 提取源站 IP（从 BackendGroups 和 ServerIps 中提取）
	var sourceIPs []string
	if d.BackendGroups != nil {
		for _, bg := range d.BackendGroups {
			if bg.Backends != nil {
				for _, b := range bg.Backends {
					if b.IP != nil && *b.IP != "" {
						sourceIPs = append(sourceIPs, *b.IP)
					}
				}
			}
		}
	}
	// 如果 BackendGroups 没有，尝试从 ServerIps 提取（逗号分隔）
	if len(sourceIPs) == 0 && d.ServerIps != nil && *d.ServerIps != "" {
		for _, ip := range strings.Split(*d.ServerIps, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				sourceIPs = append(sourceIPs, ip)
			}
		}
	}

	// 使用域名作为实例ID（火山引擎WAF以域名为粒度）
	// CreationTime:火山引擎 WAF SDK(ListDomain 输出)无创建时间字段,留空(理由见上方注释)。
	// Tags:WAF 列表/详情接口均不返回标签;同包 tag.go 的 DescribeTags 走 ECS 服务
	// (ResourceType 仅覆盖 ECS 资源),无法查询 WAF 域名标签,留空 map,不硬造值。
	return types.WAFInstance{
		InstanceID:     domain,
		InstanceName:   domain,
		Status:         status,
		Region:         region,
		DomainCount:    domainCount,
		ProtectedHosts: protectedHosts,
		SourceIPs:      sourceIPs,
		Cname:          cname,
		WAFEnabled:     wafEnabled,
		CCEnabled:      ccEnabled,
		AntiBotEnabled: antiBotEnabled,
		ProtectionMode: protectionMode,
		Provider:       "volcano",
		CreationTime:   "",
		Description:    description,
		Tags:           make(map[string]string),
	}
}
