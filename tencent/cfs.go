package tencent

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cfs "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cfs/v20190719"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// CFSAdapter 腾讯云 CFS (文件存储) 适配器
type CFSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// nasMonitorClients 云监控客户端按实例 region 缓存(nas_metrics.go)
	nasMonitorClients map[string]nasMonitorClient
	// nasMonitorMu 保护 nasMonitorClients 的并发创建
	nasMonitorMu sync.Mutex
	// appid 缓存:CAM GetUserAppId 解析结果(CFS 云监控维度必带),once 防并发重复解析
	appidOnce  sync.Once
	appidValue string
	appidErr   error
	// nasMonitorHooks NAS 指标查询测试注入钩子(非 nil 时替代真实云监控/
	// CAM 客户端创建;仅单测使用),见 nas_metrics.go
	nasMonitorHooks *nasMetricHooks
}

// NewCFSAdapter 创建 CFS 适配器
func NewCFSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *CFSAdapter {
	return &CFSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 CFS 客户端
func (a *CFSAdapter) createClient(region string) (*cfs.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cfs.tencentcloudapi.com"

	return cfs.NewClient(credential, region, cpf)
}

// ListInstances 获取 CFS 文件系统列表
func (a *CFSAdapter) ListInstances(ctx context.Context, region string) ([]types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CFS客户端失败: %w", err)
	}

	var instances []types.NASInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := cfs.NewDescribeCfsFileSystemsRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeCfsFileSystems(request)
		if err != nil {
			return nil, fmt.Errorf("获取CFS文件系统列表失败: %w", err)
		}

		if response.Response == nil || response.Response.FileSystems == nil {
			break
		}

		for _, fs := range response.Response.FileSystems {
			instance := a.convertToNASInstance(fs, region)
			instances = append(instances, instance)
		}

		if len(response.Response.FileSystems) < int(limit) {
			break
		}
		offset += limit
	}

	return instances, nil
}

// GetInstance 获取单个 CFS 文件系统详情
func (a *CFSAdapter) GetInstance(ctx context.Context, region, fileSystemID string) (*types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CFS客户端失败: %w", err)
	}

	request := cfs.NewDescribeCfsFileSystemsRequest()
	request.FileSystemId = &fileSystemID

	response, err := client.DescribeCfsFileSystems(request)
	if err != nil {
		return nil, fmt.Errorf("获取CFS文件系统详情失败: %w", err)
	}

	if response.Response == nil || response.Response.FileSystems == nil || len(response.Response.FileSystems) == 0 {
		return nil, fmt.Errorf("CFS文件系统不存在: %s", fileSystemID)
	}

	instance := a.convertToNASInstance(response.Response.FileSystems[0], region)

	// 获取挂载点信息
	mountTargets, err := a.getMountTargets(client, fileSystemID)
	if err == nil {
		instance.MountTargets = mountTargets
	}

	return &instance, nil
}

// getMountTargets 获取挂载点列表
func (a *CFSAdapter) getMountTargets(client *cfs.Client, fileSystemID string) ([]types.MountTarget, error) {
	request := cfs.NewDescribeMountTargetsRequest()
	request.FileSystemId = &fileSystemID

	response, err := client.DescribeMountTargets(request)
	if err != nil {
		return nil, err
	}

	var mountTargets []types.MountTarget
	if response.Response != nil && response.Response.MountTargets != nil {
		for _, mt := range response.Response.MountTargets {
			mountTarget := types.MountTarget{
				NetworkType: "VPC",
			}
			if mt.MountTargetId != nil {
				mountTarget.MountTargetID = *mt.MountTargetId
			}
			if mt.IpAddress != nil {
				mountTarget.MountTargetDomain = *mt.IpAddress
			}
			if mt.VpcId != nil {
				mountTarget.VPCID = *mt.VpcId
			}
			if mt.SubnetId != nil {
				mountTarget.VSwitchID = *mt.SubnetId
			}
			if mt.LifeCycleState != nil {
				mountTarget.Status = *mt.LifeCycleState
			}
			mountTargets = append(mountTargets, mountTarget)
		}
	}

	return mountTargets, nil
}

// ListInstancesByIDs 批量获取 CFS 文件系统
func (a *CFSAdapter) ListInstancesByIDs(ctx context.Context, region string, fileSystemIDs []string) ([]types.NASInstance, error) {
	var instances []types.NASInstance
	for _, id := range fileSystemIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取CFS文件系统失败", elog.String("file_system_id", id), elog.FieldErr(err))
			continue
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

// GetInstanceStatus 获取文件系统状态
func (a *CFSAdapter) GetInstanceStatus(ctx context.Context, region, fileSystemID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, fileSystemID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取文件系统列表
func (a *CFSAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.NASInstanceFilter) ([]types.NASInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建CFS客户端失败: %w", err)
	}

	request := cfs.NewDescribeCfsFileSystemsRequest()

	if filter != nil {
		if filter.VPCID != "" {
			request.VpcId = &filter.VPCID
		}
	}

	response, err := client.DescribeCfsFileSystems(request)
	if err != nil {
		return nil, fmt.Errorf("获取CFS文件系统列表失败: %w", err)
	}

	var instances []types.NASInstance
	if response.Response != nil && response.Response.FileSystems != nil {
		for _, fs := range response.Response.FileSystems {
			instance := a.convertToNASInstance(fs, region)

			// 应用额外过滤条件
			if filter != nil {
				if len(filter.Status) > 0 && !slices.Contains(filter.Status, instance.Status) {
					continue
				}
				if filter.FileSystemType != "" && instance.FileSystemType != filter.FileSystemType {
					continue
				}
				if filter.ProtocolType != "" && instance.ProtocolType != filter.ProtocolType {
					continue
				}
			}

			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// convertToNASInstance 转换为统一的 NAS 实例结构
func (a *CFSAdapter) convertToNASInstance(fs *cfs.FileSystemInfo, region string) types.NASInstance {
	instance := types.NASInstance{
		Region:   region,
		Provider: string(domain.CloudProviderTencent),
	}

	if fs.FileSystemId != nil {
		instance.FileSystemID = *fs.FileSystemId
	}
	if fs.FsName != nil {
		instance.FileSystemName = *fs.FsName
		instance.Description = *fs.FsName
	}
	if fs.LifeCycleState != nil {
		// 状态统一走 types.NASStatus 标准化(原本地映射表已并入 types/nas.go 的 tencent 分表)
		instance.Status = types.NASStatus("tencent", *fs.LifeCycleState)
	}
	if fs.Zone != nil {
		instance.Zone = *fs.Zone
	}
	if fs.StorageType != nil {
		instance.FileSystemType = *fs.StorageType // SD (标准型) / HP (性能型) / TB (Turbo标准型) / TP (Turbo性能型)
	}
	if fs.Protocol != nil {
		instance.ProtocolType = *fs.Protocol // NFS / CIFS / TURBO
	}
	if fs.StorageResourcePkg != nil {
		instance.StorageType = *fs.StorageResourcePkg
	}
	if fs.SizeByte != nil {
		instance.MeteredSize = int64(*fs.SizeByte)
		instance.UsedCapacity = int64(*fs.SizeByte) / (1024 * 1024 * 1024)
	}
	if fs.SizeLimit != nil {
		instance.Capacity = int64(*fs.SizeLimit) / (1024 * 1024 * 1024)
	}
	if fs.BandwidthLimit != nil {
		// 带宽限制
		instance.Description = fmt.Sprintf("%s (带宽: %.2f MB/s)", instance.Description, *fs.BandwidthLimit)
	}
	if fs.Encrypted != nil && *fs.Encrypted {
		instance.EncryptType = 1
		if fs.KmsKeyId != nil {
			instance.KMSKeyID = *fs.KmsKeyId
		}
	}
	if fs.CreationTime != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", *fs.CreationTime); err == nil {
			instance.CreationTime = t
		}
	}

	// 解析标签
	if fs.Tags != nil && len(fs.Tags) > 0 {
		instance.Tags = make(map[string]string)
		for _, tag := range fs.Tags {
			if tag.TagKey != nil && tag.TagValue != nil {
				instance.Tags[*tag.TagKey] = *tag.TagValue
			}
		}
	}

	// 计费类型
	if fs.PGroup != nil && fs.PGroup.PGroupId != nil {
		// 有权限组表示已配置
	}

	return instance
}

// parseInt64 安全转换字符串到 int64
func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
