package volcano

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

// TOSAdapter 火山引擎TOS对象存储适配器
type TOSAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTOSAdapter 创建TOS适配器
func NewTOSAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *TOSAdapter {
	return &TOSAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建TOS客户端
func (a *TOSAdapter) createClient(region string) (*tos.ClientV2, error) {
	if region == "" {
		region = a.defaultRegion
	}

	endpoint := fmt.Sprintf("tos-%s.volces.com", region)

	client, err := tos.NewClientV2(
		endpoint,
		tos.WithRegion(region),
		tos.WithCredentials(tos.NewStaticCredentials(a.accessKeyID, a.accessKeySecret)),
	)
	if err != nil {
		return nil, fmt.Errorf("创建TOS客户端失败: %w", err)
	}

	return client, nil
}

// ListBuckets 获取存储桶列表
func (a *TOSAdapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	output, err := client.ListBuckets(ctx, &tos.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("获取TOS存储桶列表失败: %w", err)
	}

	// 列表 N+1 回填(存储类型/标签)需要桶所在 region 的客户端;
	// 按 region 惰性创建并复用,避免每桶新建客户端放大成本。
	regionClients := map[string]*tos.ClientV2{}
	getRegionClient := func(r string) *tos.ClientV2 {
		if c, ok := regionClients[r]; ok {
			return c
		}
		c, err := a.createClient(r)
		if err != nil {
			return nil
		}
		regionClients[r] = c
		return c
	}

	var buckets []types.OSSBucket
	for _, bucket := range output.Buckets {
		ossBucket := a.convertToOSSBucket(bucket)

		// 如果指定了region，只返回该region的bucket
		if region != "" && ossBucket.Region != region {
			continue
		}

		// N+1 回填存储类型与标签:ListBuckets 响应不含这两个字段,须逐桶调用
		// 详情接口——HeadBucket 取存储类型、GetBucketTagging 取标签,与
		// GetBucket 详情口径一致。每桶 2 次额外请求,请求数随桶数线性增长;
		// 失败(无权限/未设置标签)静默降级为空,前端显示 '-',不打断列表。
		if rc := getRegionClient(ossBucket.Region); rc != nil {
			headOutput, err := rc.HeadBucket(ctx, &tos.HeadBucketInput{Bucket: bucket.Name})
			if err == nil && headOutput != nil && headOutput.StorageClass != "" {
				ossBucket.StorageClass = string(headOutput.StorageClass)
			}
			tagOutput, err := rc.GetBucketTagging(ctx, &tos.GetBucketTaggingInput{Bucket: bucket.Name})
			if err == nil {
				ossBucket.Tags = mapTOSTags(tagOutput.TagSet.Tags)
			}
		}

		// 获取 bucket 统计信息（存储大小和对象数量）
		stats, err := a.GetBucketStats(ctx, bucket.Name)
		if err == nil && stats != nil {
			ossBucket.ObjectCount = stats.ObjectCount
			ossBucket.StorageSize = stats.StorageSize
		} else {
			a.logger.Warn("获取TOS存储桶统计信息失败",
				elog.String("bucket", bucket.Name),
				elog.FieldErr(err))
		}

		buckets = append(buckets, ossBucket)
	}

	a.logger.Info("获取火山引擎TOS存储桶列表成功",
		elog.String("region", region),
		elog.Int("count", len(buckets)))

	return buckets, nil
}

// GetBucket 获取单个存储桶详情
func (a *TOSAdapter) GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error) {
	// 先获取bucket列表找到bucket的region
	client, err := a.createClient("")
	if err != nil {
		return nil, err
	}

	// 获取bucket列表
	listOutput, err := client.ListBuckets(ctx, &tos.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("获取TOS存储桶列表失败: %w", err)
	}

	// 查找目标bucket
	var targetBucket *tos.ListedBucket
	for i := range listOutput.Buckets {
		if listOutput.Buckets[i].Name == bucketName {
			targetBucket = &listOutput.Buckets[i]
			break
		}
	}

	if targetBucket == nil {
		return nil, fmt.Errorf("存储桶不存在: %s", bucketName)
	}

	bucket := a.convertToOSSBucket(*targetBucket)

	// 获取bucket详细信息
	regionClient, err := a.createClient(bucket.Region)
	if err == nil {
		// 获取ACL
		aclOutput, err := regionClient.GetBucketACL(ctx, &tos.GetBucketACLInput{Bucket: bucketName})
		if err == nil {
			bucket.ACL = a.convertGrantsToACL(aclOutput.Grants)
		}

		// 获取版本控制状态
		versionOutput, err := regionClient.GetBucketVersioning(ctx, &tos.GetBucketVersioningInput{Bucket: bucketName})
		if err == nil {
			bucket.Versioning = string(versionOutput.Status)
		}

		// 获取存储类型(桶 HEAD 详情响应才携带 StorageClass;
		// 与列表 N+1 同源,口径一致,拿不到保持空由前端显示 '-')
		headOutput, err := regionClient.HeadBucket(ctx, &tos.HeadBucketInput{Bucket: bucketName})
		if err == nil && headOutput != nil && headOutput.StorageClass != "" {
			bucket.StorageClass = string(headOutput.StorageClass)
		}

		// 获取桶标签(与列表口径一致;未设置标签或失败均静默留空)
		tagOutput, err := regionClient.GetBucketTagging(ctx, &tos.GetBucketTaggingInput{Bucket: bucketName})
		if err == nil {
			bucket.Tags = mapTOSTags(tagOutput.TagSet.Tags)
		}
	}

	return &bucket, nil
}

// GetBucketStats 获取存储桶统计信息
func (a *TOSAdapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	// TOS API 不直接提供统计信息，需要通过其他方式获取
	// 这里返回基本信息
	return &types.OSSBucketStats{
		BucketName: bucketName,
	}, nil
}

// ListBucketsWithFilter 带过滤条件获取存储桶列表
func (a *TOSAdapter) ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error) {
	buckets, err := a.ListBuckets(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return buckets, nil
	}

	var filtered []types.OSSBucket
	for _, bucket := range buckets {
		// 按名称前缀过滤
		if filter.Prefix != "" && !hasPrefix(bucket.BucketName, filter.Prefix) {
			continue
		}

		// 按存储类型过滤
		if filter.StorageClass != "" && bucket.StorageClass != filter.StorageClass {
			continue
		}

		// 按bucket名称列表过滤
		if len(filter.BucketNames) > 0 && !slices.Contains(filter.BucketNames, bucket.BucketName) {
			continue
		}

		filtered = append(filtered, bucket)
	}

	return filtered, nil
}

// convertToOSSBucket 转换为统一的OSS存储桶结构
func (a *TOSAdapter) convertToOSSBucket(bucket tos.ListedBucket) types.OSSBucket {
	region := bucket.Location
	// 火山引擎的Location格式可能是 cn-beijing
	if region == "" {
		region = a.defaultRegion
	}

	// 解析创建时间
	var creationTime time.Time
	if bucket.CreationDate != "" {
		if t, err := time.Parse(time.RFC3339, bucket.CreationDate); err == nil {
			creationTime = t
		} else if t, err := time.Parse("2006-01-02T15:04:05Z", bucket.CreationDate); err == nil {
			creationTime = t
		}
	}

	// 使用SDK返回的endpoint，如果没有则构造
	extranetEndpoint := bucket.ExtranetEndpoint
	if extranetEndpoint == "" {
		extranetEndpoint = fmt.Sprintf("%s.tos-%s.volces.com", bucket.Name, region)
	}
	intranetEndpoint := bucket.IntranetEndpoint
	if intranetEndpoint == "" {
		intranetEndpoint = fmt.Sprintf("%s.tos-%s.ivolces.com", bucket.Name, region)
	}

	// 返回统一结构。StorageClass 与 Tags 不在转换函数里伪造:
	// ListBuckets 响应不含这两个字段,由 ListBuckets/GetBucket 的 N+1
	// (HeadBucket/GetBucketTagging)同源回填,列表与详情口径一致。
	return types.OSSBucket{
		BucketName:       bucket.Name,
		Region:           region,
		Location:         bucket.Location,
		CreationTime:     creationTime,
		ExtranetEndpoint: extranetEndpoint,
		IntranetEndpoint: intranetEndpoint,
		Provider:         "volcano",
	}
}

// mapTOSTags 将 TOS GetBucketTagging 返回的 TagSet 规整为统一的标签 map。
// 列表与详情共用,保证口径一致;空 Key 或无标签时返回 nil(即不填)。
func mapTOSTags(tags []tos.Tag) map[string]string {
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

// convertGrantsToACL 将权限列表转换为ACL字符串
func (a *TOSAdapter) convertGrantsToACL(grants []tos.GrantV2) string {
	// 简化处理，根据权限判断ACL类型
	hasPublicRead := false
	hasPublicWrite := false

	for _, grant := range grants {
		if grant.GranteeV2.Canned == enum.CannedAllUsers {
			if grant.Permission == enum.PermissionRead {
				hasPublicRead = true
			}
			if grant.Permission == enum.PermissionWrite {
				hasPublicWrite = true
			}
		}
	}

	if hasPublicRead && hasPublicWrite {
		return "public-read-write"
	}
	if hasPublicRead {
		return "public-read"
	}
	return "private"
}

// hasPrefix 检查字符串是否有指定前缀
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
