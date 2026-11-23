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

// ossEndpoint 返回地域对应的 OSS 公网端点。
//
// 必须显式带 https:// 前缀:aliyun-oss-go-sdk 的 urlMaker.InitExt 对不带 scheme 的
// endpoint 默认走 http(oss/conn.go 的 InitExt,"http://" > "https://" > 兜底 http),
// 于是桶列表/桶统计等所有请求(含 Authorization 头里的 AccessKeyId 与签名)都以明文
// 传输;出向 80 端口被封的网络里还会直接 dial 超时——生产环境 cn-nanjing 的 OSS 同步
// 就是这样 100% 失败的(connectex: 47.122.12.194:80 无响应)。
func ossEndpoint(region string) string {
	return fmt.Sprintf("https://oss-%s.aliyuncs.com", region)
}

// ossStatsRegion 决定「取某个桶的统计信息」该用哪个地域的端点。
//
// 阿里云 OSS 的 GetBucketStat 必须在**桶自身所在地域**的端点上调用:用其它地域的
// 端点会返回 403 AccessDenied("The bucket you are attempting to access must be
// addressed using the specified endpoint")。原实现一律用适配器的 defaultRegion,
// 导致只要桶不在 defaultRegion 就永远取不到容量/对象数(线上实测 982 个桶里 700 个
// 是 0)。
//
// 优先级:桶自身地域 → 本次列举使用的地域 → 适配器 defaultRegion。
func ossStatsRegion(bucketRegion, listRegion, defaultRegion string) string {
	if bucketRegion != "" {
		return bucketRegion
	}
	if listRegion != "" {
		return listRegion
	}
	return defaultRegion
}

// createClient 创建OSS客户端
func (a *OSSAdapter) createClient(region string) (*oss.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	return oss.New(ossEndpoint(region), a.accessKeyID, a.accessKeySecret)
}

// ListBuckets 获取存储桶列表
func (a *OSSAdapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	var buckets []types.OSSBucket
	marker := ""

	// statsClients 按地域缓存客户端:同一次列举内同地域的桶复用同一个客户端
	// (oss.New 只装配配置不发请求,缓存省掉重复构造)。
	statsClients := map[string]*oss.Client{}
	statsClientFor := func(r string) (*oss.Client, error) {
		if c, ok := statsClients[r]; ok {
			return c, nil
		}
		c, err := a.createClient(r)
		if err != nil {
			return nil, err
		}
		statsClients[r] = c
		return c, nil
	}

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

			// 获取 bucket 统计信息（存储大小和对象数量）。
			// 必须用**桶自身地域**的端点:跨地域端点一律 403 AccessDenied
			// ("must be addressed using the specified endpoint"),结果就是容量/
			// 对象数永远为 0(详见 ossStatsRegion 注释)。
			statsRegion := ossStatsRegion(ossBucket.Region, region, a.defaultRegion)
			var stats *types.OSSBucketStats
			statsClient, clientErr := statsClientFor(statsRegion)
			if clientErr != nil {
				err = clientErr
			} else {
				stats, err = a.getBucketStatsWithClient(statsClient, bucket.Name)
			}
			if err == nil && stats != nil {
				ossBucket.ObjectCount = stats.ObjectCount
				ossBucket.StorageSize = stats.StorageSize
			} else {
				a.logger.Warn("获取OSS存储桶统计信息失败",
					elog.String("bucket", bucket.Name),
					elog.String("region", statsRegion),
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

	// 获取bucket统计信息:用桶自身地域的端点(跨地域端点 403,见 ossStatsRegion)。
	if statsClient, clientErr := a.createClient(bucket.Region); clientErr == nil {
		if stats, statsErr := a.getBucketStatsWithClient(statsClient, bucketName); statsErr == nil && stats != nil {
			bucket.ObjectCount = stats.ObjectCount
			bucket.StorageSize = stats.StorageSize
		}
	}

	// 获取桶标签:GetBucketInfo 响应不含标签,需单独 GetBucketTagging,
	// 与列表口径一致(无标签返回空 TagSet,失败静默留空)。
	tagResult, err := client.GetBucketTagging(bucketName)
	if err == nil {
		bucket.Tags = a.mapAliyunTags(tagResult.Tags)
	}

	return &bucket, nil
}

// GetBucketStats 获取存储桶统计信息。
//
// 注意:它使用适配器的 defaultRegion 端点,只适用于「桶就在 defaultRegion」的场景。
// 已知道桶所在地域时应走 getBucketStatsWithClient(用该地域的客户端),否则跨地域桶
// 会 403。保留该方法是为了不破坏既有调用方/接口。
func (a *OSSAdapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	client, err := a.createClient("")
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %w", err)
	}

	return a.getBucketStatsWithClient(client, bucketName)
}

// getBucketStatsWithClient 用给定客户端读取桶统计信息(客户端的地域决定端点)。
func (a *OSSAdapter) getBucketStatsWithClient(client *oss.Client, bucketName string) (*types.OSSBucketStats, error) {
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
