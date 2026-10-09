package aliyun

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
)

const (
	aliyunTagMaxRetries    = 3
	aliyunTagBaseBackoffMs = 200
)

// TagAdapterImpl 阿里云标签适配器
type TagAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTagAdapter 创建阿里云标签适配器
func NewTagAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *TagAdapterImpl {
	return &TagAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// resolveRegion 解析地域
func (a *TagAdapterImpl) resolveRegion(region string) string {
	if region == "" {
		return a.defaultRegion
	}
	return region
}

// createClient 创建 ECS 客户端（阿里云标签 API 通过 ECS API 暴露）
func (a *TagAdapterImpl) createClient(region string) (*ecs.Client, error) {
	return ecs.NewClientWithAccessKey(a.resolveRegion(region), a.accessKeyID, a.accessKeySecret)
}

// retryWithBackoff 指数退避重试
func (a *TagAdapterImpl) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= aliyunTagMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < aliyunTagMaxRetries {
			backoff := time.Duration(float64(aliyunTagBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("aliyun: tag API failed after %d retries: %w", aliyunTagMaxRetries, lastErr)
}

// ListTagKeys 查询标签键列表
func (a *TagAdapterImpl) ListTagKeys(ctx context.Context, region string) ([]string, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	var keys []string
	err = a.retryWithBackoff(func() error {
		request := ecs.CreateDescribeTagsRequest()
		request.RegionId = a.resolveRegion(region)
		request.PageSize = requests.NewInteger(50)

		response, e := client.DescribeTags(request)
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, tag := range response.Tags.Tag {
			if !seen[tag.TagKey] {
				keys = append(keys, tag.TagKey)
				seen[tag.TagKey] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// ListTagValues 查询指定标签键的值列表
func (a *TagAdapterImpl) ListTagValues(ctx context.Context, region, key string) ([]string, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	var values []string
	err = a.retryWithBackoff(func() error {
		request := ecs.CreateDescribeTagsRequest()
		request.RegionId = a.resolveRegion(region)
		request.PageSize = requests.NewInteger(50)
		request.Tag = &[]ecs.DescribeTagsTag{{Key: key}}

		response, e := client.DescribeTags(request)
		if e != nil {
			return e
		}

		for _, tag := range response.Tags.Tag {
			if tag.TagKey == key {
				values = append(values, tag.TagValue)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

// GetResourceTags 查询资源绑定的标签
func (a *TagAdapterImpl) GetResourceTags(ctx context.Context, region, resourceType, resourceID string) (map[string]string, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	tags := make(map[string]string)
	err = a.retryWithBackoff(func() error {
		request := ecs.CreateDescribeTagsRequest()
		request.RegionId = a.resolveRegion(region)
		request.ResourceType = resourceType
		request.ResourceId = resourceID

		response, e := client.DescribeTags(request)
		if e != nil {
			return e
		}

		for _, tag := range response.Tags.Tag {
			tags[tag.TagKey] = tag.TagValue
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// TagResource 为资源绑定标签
func (a *TagAdapterImpl) TagResource(ctx context.Context, region, resourceType, resourceID string, tags map[string]string) error {
	client, err := a.createClient(region)
	if err != nil {
		return fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	return a.retryWithBackoff(func() error {
		request := ecs.CreateAddTagsRequest()
		request.RegionId = a.resolveRegion(region)
		request.ResourceType = resourceType
		request.ResourceId = resourceID

		tagList := make([]ecs.AddTagsTag, 0, len(tags))
		for k, v := range tags {
			tagList = append(tagList, ecs.AddTagsTag{Key: k, Value: v})
		}
		request.Tag = &tagList

		_, e := client.AddTags(request)
		return e
	})
}

// UntagResource 解绑资源标签
func (a *TagAdapterImpl) UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error {
	client, err := a.createClient(region)
	if err != nil {
		return fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	return a.retryWithBackoff(func() error {
		request := ecs.CreateRemoveTagsRequest()
		request.RegionId = a.resolveRegion(region)
		request.ResourceType = resourceType
		request.ResourceId = resourceID

		tagList := make([]ecs.RemoveTagsTag, 0, len(tagKeys))
		for _, k := range tagKeys {
			tagList = append(tagList, ecs.RemoveTagsTag{Key: k})
		}
		request.Tag = &tagList

		_, e := client.RemoveTags(request)
		return e
	})
}

// ListResourcesByTag 按标签查询资源列表
// 阿里云 DescribeTags 返回标签元数据和资源类型计数，不返回具体资源 ID。
// 此处通过 DescribeInstances 按标签过滤来获取具体资源。
func (a *TagAdapterImpl) ListResourcesByTag(ctx context.Context, region, key, value string) ([]cloudx.TaggedResource, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("aliyun: create tag client failed: %w", err)
	}

	resolvedRegion := a.resolveRegion(region)
	var resources []cloudx.TaggedResource
	err = a.retryWithBackoff(func() error {
		request := ecs.CreateDescribeInstancesRequest()
		request.RegionId = resolvedRegion
		request.PageSize = requests.NewInteger(100)
		request.Tag = &[]ecs.DescribeInstancesTag{{Key: key, Value: value}}

		response, e := client.DescribeInstances(request)
		if e != nil {
			return e
		}

		for _, inst := range response.Instances.Instance {
			resources = append(resources, cloudx.TaggedResource{
				ResourceType: "instance",
				ResourceID:   inst.InstanceId,
				Region:       resolvedRegion,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resources, nil
}

// Ensure compile-time interface compliance
var _ cloudx.TagAdapter = (*TagAdapterImpl)(nil)
