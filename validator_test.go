package cloudx

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCloudValidatorFactory_CreateValidator(t *testing.T) {
	factory := NewCloudValidatorFactory()

	tests := []struct {
		name     string
		provider domain.CloudProvider
		wantErr  bool
	}{
		{"阿里云", domain.CloudProviderAliyun, false},
		{"AWS", domain.CloudProviderAWS, false},
		{"Azure", domain.CloudProviderAzure, false},
		{"腾讯云", domain.CloudProviderTencent, false},
		{"华为云", domain.CloudProviderHuawei, false},
		{"火山云", domain.CloudProviderVolcano, false},
		{"火山引擎", domain.CloudProviderVolcengine, false},
		{"不支持的厂商", domain.CloudProvider("gcp"), true},
		{"空字符串", domain.CloudProvider(""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator, err := factory.CreateValidator(tt.provider)
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrUnsupportedProvider)
				assert.Nil(t, validator)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, validator)
			}
		})
	}
}
