package iam

import (
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

func TestRegisterAndGetIAMAdapterCreator(t *testing.T) {
	creator := func(logger *elog.Component) (CloudIAMAdapter, error) {
		return nil, nil
	}

	RegisterIAMAdapter(domain.CloudProviderAliyun, creator)
	got, err := GetIAMAdapterCreator(domain.CloudProviderAliyun)
	if err != nil {
		t.Fatalf("已注册厂商不应返回错误: %v", err)
	}
	if got == nil {
		t.Fatal("creator 不应为 nil")
	}

	if _, err := GetIAMAdapterCreator(domain.CloudProviderAzure); !errors.Is(err, ErrUnsupportedIAMProvider) {
		t.Errorf("未注册厂商应返回 ErrUnsupportedIAMProvider，got: %v", err)
	}

	// 幂等覆盖：重复注册以最后一次为准
	RegisterIAMAdapter(domain.CloudProviderAliyun, creator)
	if _, err := GetIAMAdapterCreator(domain.CloudProviderAliyun); err != nil {
		t.Fatalf("重复注册后仍应可获取: %v", err)
	}

	// 非法输入不 panic 也不污染注册表
	RegisterIAMAdapter("", creator)
	RegisterIAMAdapter(domain.CloudProviderAWS, nil)
}
