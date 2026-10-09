package aliyun

import (
	"context"
	"fmt"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/gotomicro/ego/core/elog"
)

// OSSAdapter 阿里云OSS适配器
type OSSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// OSS 指标查询(CMS acs_oss_dashboard)状态,见 oss_metrics.go
	ossMetricMu      sync.Mutex
	ossMetricClients map[string]cmsMetricClient
	// ossMetricHooks 测试注入钩子(非 nil 时替代真实 CMS 客户端创建)
	ossMetricHooks *ossMetricHooks
}

// NewOSSAdapter 创建OSS适配器
func NewOSSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *OSSAdapter {
	return &OSSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建OSS客户端
func (a *OSSAdapter) createClient(region string) (*oss.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	endpoint := fmt.Sprintf("oss-%s.aliyuncs.com", region)
	return oss.New(endpoint, a.accessKeyID, a.accessKeySecret)
}

// ListBuckets 获取存储桶列表
func (a *OSSAdapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	var buckets []types.OSSBucket
	marker := ""

	for {
		result, err := client.ListBuckets(oss.Marker(marker), oss.MaxKeys(100))
		if err != nil {
			return nil, fmt.Errorf("获取OSS存储桶列表失败: %w", err)
		}

		for _, bucket := range result.Buckets {
			ossBucket := a.convertToOSSBucket(bucket)

			// 如果指定了region，只返回该region的bucket
			if region != "" && ossBucket.Region != region {
				continue
			}

			// N+1 回填标签:ListBuckets 响应不含标签,需逐桶 GetBucketTagging。
			// 请求数随桶数线性增长(标签为审计要求字段,故接受该成本;
			// 阿里云 SDK 默认开启 301 跨区重定向,任意 region 端点均可读桶标签);
			// 无标签返回空 TagSet,失败(无权限/桶已删除)静默降级为空,不打断列表。
			if tagResult, err := client.GetBucketTagging(bucket.Name); err == nil {
				ossBucket.Tags = a.mapAliyunTags(tagResult.Tags)
			}

			// 获取 bucket 统计信息（存储大小和对象数量）
			stats, err := a.GetBucketStats(ctx, bucket.Name)
			if err == nil && stats != nil {
				ossBucket.ObjectCount = stats.ObjectCount
				ossBucket.StorageSize = stats.StorageSize
			} else {
				a.logger.Warn("获取OSS存储桶统计信息失败",
					elog.String("bucket", bucket.Name),
					elog.FieldErr(err))
			}

			buckets = append(buckets, ossBucket)
		}

		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}

	return buckets, nil
}

// GetBucket 获取单个存储桶详情
func (a *OSSAdapter) GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error) {
	// 先获取bucket列表找到bucket的region
	client, err := a.createClient("")
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	// 获取bucket信息
	result, err := client.GetBucketInfo(bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取OSS存储桶信息失败: %w", err)
	}

	bucket := a.convertBucketInfoToOSSBucket(result.BucketInfo)

	// 获取bucket统计信息
	stats, err := a.GetBucketStats(ctx, bucketName)
	if err == nil && stats != nil {
		bucket.ObjectCount = stats.ObjectCount
		bucket.StorageSize = stats.StorageSize
	}

	// 获取桶标签:GetBucketInfo 响应不含标签,需单独 GetBucketTagging,
	// 与列表口径一致(无标签返回空 TagSet,失败静默留空)。
	tagResult, err := client.GetBucketTagging(bucketName)
	if err == nil {
		bucket.Tags = a.mapAliyunTags(tagResult.Tags)
	}

	return &bucket, nil
}

// GetBucketStats 获取存储桶统计信息
func (a *OSSAdapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	client, err := a.createClient("")
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	result, err := client.GetBucketStat(bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取OSS存储桶统计信息失败: %w", err)
	}

	return &types.OSSBucketStats{
		BucketName:       bucketName,
		ObjectCount:      result.ObjectCount,
		StorageSize:      result.Storage,
		MultipartCount:   result.MultipartUploadCount,
		LiveChannelCount: result.LiveChannelCount,
		LastModifiedTime: result.LastModifiedTime,
	}, nil
}

// ListBucketsWithFilter 带过滤条件获取存储桶列表
func (a *OSSAdapter) ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	var options []oss.Option
	if filter != nil && filter.Prefix != "" {
		options = append(options, oss.Prefix(filter.Prefix))
	}

	var buckets []types.OSSBucket
	marker := ""

	for {
		opts := append(options, oss.Marker(marker), oss.MaxKeys(100))
		result, err := client.ListBuckets(opts...)
		if err != nil {
			return nil, fmt.Errorf("获取OSS存储桶列表失败: %w", err)
		}

		for _, bucket := range result.Buckets {
			ossBucket := a.convertToOSSBucket(bucket)

			// 应用过滤条件
			if region != "" && ossBucket.Region != region {
				continue
			}
			if filter != nil && filter.StorageClass != "" && ossBucket.StorageClass != filter.StorageClass {
				continue
			}

			// N+1 回填标签:与 ListBuckets 同口径(成本与降级策略见其注释);
			// 失败或无标签静默留空。
			if tagResult, err := client.GetBucketTagging(bucket.Name); err == nil {
				ossBucket.Tags = a.mapAliyunTags(tagResult.Tags)
			}

			buckets = append(buckets, ossBucket)
		}

		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}

	return buckets, nil
}

// convertToOSSBucket 转换为统一的OSS存储桶结构
func (a *OSSAdapter) convertToOSSBucket(bucket oss.BucketProperties) types.OSSBucket {
	// 从Location提取region (如 oss-cn-hangzhou -> cn-hangzhou)
	region := bucket.Location
	if len(region) > 4 && region[:4] == "oss-" {
		region = region[4:]
	}

	return types.OSSBucket{
		BucketName:   bucket.Name,
		Region:       region,
		Location:     bucket.Location,
		CreationTime: bucket.CreationDate,
		// StorageClass 直接透传列表接口真实值(阿里云 ListBuckets 响应携带桶存储类型),
		// 不硬编码默认值:响应缺失时保持空,由前端显示 '-'。
		StorageClass:     bucket.StorageClass,
		ExtranetEndpoint: fmt.Sprintf("%s.oss-%s.aliyuncs.com", bucket.Name, region),
		IntranetEndpoint: fmt.Sprintf("%s.oss-%s-internal.aliyuncs.com", bucket.Name, region),
		Provider:         "aliyun",
	}
}

// convertBucketInfoToOSSBucket 从BucketInfo转换
func (a *OSSAdapter) convertBucketInfoToOSSBucket(info oss.BucketInfo) types.OSSBucket {
	region := info.Location
	if len(region) > 4 && region[:4] == "oss-" {
		region = region[4:]
	}

	bucket := types.OSSBucket{
		BucketName:       info.Name,
		Region:           region,
		Location:         info.Location,
		CreationTime:     info.CreationDate,
		StorageClass:     info.StorageClass,
		ACL:              string(info.ACL),
		ExtranetEndpoint: info.ExtranetEndpoint,
		IntranetEndpoint: info.IntranetEndpoint,
		Provider:         "aliyun",
	}

	// 版本控制
	if info.Versioning != "" {
		bucket.Versioning = info.Versioning
	}

	// 跨区域复制
	bucket.CrossRegionReplication = info.CrossRegionReplication == "Enabled"

	// 传输加速
	bucket.TransferAcceleration = info.TransferAcceleration == "Enabled"

	return bucket
}

// mapAliyunTags 将阿里云 GetBucketTagging 返回的 TagSet 规整为统一的标签 map。
// 列表与详情共用,保证口径一致;空 Key 或无标签时返回 nil(即不填)。
func (a *OSSAdapter) mapAliyunTags(tags []oss.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key == "" {
			continue
		}
		m[t.Key] = t.Value
	}
	return m
}
