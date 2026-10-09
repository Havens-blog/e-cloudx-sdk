package volcano

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/ecs"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

const (
	volcanoTagMaxRetries    = 3
	volcanoTagBaseBackoffMs = 200
)

// TagAdapterImpl 火山引擎标签适配器
type TagAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTagAdapter 创建火山引擎标签适配器
func NewTagAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *TagAdapterImpl {
	return &TagAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *TagAdapterImpl) resolveRegion(region string) string {
	if region == "" {
		return a.defaultRegion
	}
	return region
}

// createClient 创建 ECS 客户端
func (a *TagAdapterImpl) createClient(region string) (*ecs.ECS, error) {
	config := volcengine.NewConfig().
		WithCredentials(credentials.NewStaticCredentials(a.accessKeyID, a.accessKeySecret, "")).
		WithRegion(a.resolveRegion(region))

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("volcano: create session failed: %w", err)
	}

	return ecs.New(sess), nil
}

// retryWithBackoff 指数退避重试
func (a *TagAdapterImpl) retryWithBackoff(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= volcanoTagMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < volcanoTagMaxRetries {
			backoff := time.Duration(float64(volcanoTagBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			time.Sleep(backoff)
		}
	}
	return fmt.Errorf("volcano: tag API failed after %d retries: %w", volcanoTagMaxRetries, lastErr)
}

// ListTagKeys 查询标签键列表
func (a *TagAdapterImpl) ListTagKeys(ctx context.Context, region string) ([]string, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var keys []string
	err = a.retryWithBackoff(func() error {
		maxResults := int32(100)
		output, e := client.DescribeTags(&ecs.DescribeTagsInput{
			ResourceType: volcengine.String("instance"),
			MaxResults:   &maxResults,
		})
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, tr := range output.TagResources {
			k := volcengine.StringValue(tr.TagKey)
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
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
		return nil, err
	}

	var values []string
	err = a.retryWithBackoff(func() error {
		maxResults := int32(100)
		output, e := client.DescribeTags(&ecs.DescribeTagsInput{
			ResourceType: volcengine.String("instance"),
			MaxResults:   &maxResults,
			TagFilters: []*ecs.TagFilterForDescribeTagsInput{
				{Key: volcengine.String(key)},
			},
		})
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, tr := range output.TagResources {
			if volcengine.StringValue(tr.TagKey) == key {
				v := volcengine.StringValue(tr.TagValue)
				if !seen[v] {
					values = append(values, v)
					seen[v] = true
				}
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
		return nil, err
	}

	tags := make(map[string]string)
	err = a.retryWithBackoff(func() error {
		rt := resourceType
		if rt == "" {
			rt = "instance"
		}
		output, e := client.DescribeTags(&ecs.DescribeTagsInput{
			ResourceType: volcengine.String(rt),
			ResourceIds:  volcengine.StringSlice([]string{resourceID}),
		})
		if e != nil {
			return e
		}

		for _, tr := range output.TagResources {
			tags[volcengine.StringValue(tr.TagKey)] = volcengine.StringValue(tr.TagValue)
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
		return err
	}

	rt := resourceType
	if rt == "" {
		rt = "instance"
	}

	ecsTags := make([]*ecs.TagForCreateTagsInput, 0, len(tags))
	for k, v := range tags {
		ecsTags = append(ecsTags, &ecs.TagForCreateTagsInput{
			Key:   volcengine.String(k),
			Value: volcengine.String(v),
		})
	}

	return a.retryWithBackoff(func() error {
		_, e := client.CreateTags(&ecs.CreateTagsInput{
			ResourceType: volcengine.String(rt),
			ResourceIds:  volcengine.StringSlice([]string{resourceID}),
			Tags:         ecsTags,
		})
		return e
	})
}

// UntagResource 解绑资源标签
func (a *TagAdapterImpl) UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error {
	client, err := a.createClient(region)
	if err != nil {
		return err
	}

	rt := resourceType
	if rt == "" {
		rt = "instance"
	}

	return a.retryWithBackoff(func() error {
		_, e := client.DeleteTags(&ecs.DeleteTagsInput{
			ResourceType: volcengine.String(rt),
			ResourceIds:  volcengine.StringSlice([]string{resourceID}),
			TagKeys:      volcengine.StringSlice(tagKeys),
		})
		return e
	})
}

// ListResourcesByTag 按标签查询资源列表
func (a *TagAdapterImpl) ListResourcesByTag(ctx context.Context, region, key, value string) ([]cloudx.TaggedResource, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	resolvedRegion := a.resolveRegion(region)
	var resources []cloudx.TaggedResource
	err = a.retryWithBackoff(func() error {
		maxResults := int32(100)
		output, e := client.DescribeTags(&ecs.DescribeTagsInput{
			ResourceType: volcengine.String("instance"),
			MaxResults:   &maxResults,
			TagFilters: []*ecs.TagFilterForDescribeTagsInput{
				{
					Key:    volcengine.String(key),
					Values: volcengine.StringSlice([]string{value}),
				},
			},
		})
		if e != nil {
			return e
		}

		for _, tr := range output.TagResources {
			resources = append(resources, cloudx.TaggedResource{
				ResourceType: volcengine.StringValue(tr.ResourceType),
				ResourceID:   volcengine.StringValue(tr.ResourceId),
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
