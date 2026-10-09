package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudAccount_IsActive(t *testing.T) {
	tests := []struct {
		name   string
		status CloudAccountStatus
		want   bool
	}{
		{"活跃账号", CloudAccountStatusActive, true},
		{"禁用账号", CloudAccountStatusDisabled, false},
		{"错误状态", CloudAccountStatusError, false},
		{"测试中", CloudAccountStatusTesting, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &CloudAccount{Status: tt.status}
			assert.Equal(t, tt.want, a.IsActive())
		})
	}
}

func TestCloudAccount_IsReadOnly(t *testing.T) {
	a := &CloudAccount{Config: CloudAccountConfig{ReadOnly: true}}
	assert.True(t, a.IsReadOnly())

	a2 := &CloudAccount{Config: CloudAccountConfig{ReadOnly: false}}
	assert.False(t, a2.IsReadOnly())
}

func TestCloudAccount_CanAutoSync(t *testing.T) {
	tests := []struct {
		name   string
		status CloudAccountStatus
		auto   bool
		want   bool
	}{
		{"活跃+自动同步", CloudAccountStatusActive, true, true},
		{"活跃+手动同步", CloudAccountStatusActive, false, false},
		{"禁用+自动同步", CloudAccountStatusDisabled, true, false},
		{"禁用+手动同步", CloudAccountStatusDisabled, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &CloudAccount{
				Status: tt.status,
				Config: CloudAccountConfig{EnableAutoSync: tt.auto},
			}
			assert.Equal(t, tt.want, a.CanAutoSync())
		})
	}
}

func TestCloudAccount_MaskSensitiveData(t *testing.T) {
	a := &CloudAccount{
		AccessKeyID:     "LTAI5tAbcDefGhiJklMnOpQr",
		AccessKeySecret: "super-secret-key-12345",
	}
	masked := a.MaskSensitiveData()

	assert.Equal(t, "LTAI5t***OpQr", masked.AccessKeyID)
	assert.Equal(t, "***", masked.AccessKeySecret)
	// 原始对象不变
	assert.Equal(t, "LTAI5tAbcDefGhiJklMnOpQr", a.AccessKeyID)
}

func TestCloudAccount_MaskSensitiveData_ShortKey(t *testing.T) {
	a := &CloudAccount{
		AccessKeyID:     "short",
		AccessKeySecret: "secret",
	}
	masked := a.MaskSensitiveData()
	assert.Equal(t, "***", masked.AccessKeyID)
}

func TestCloudAccount_UpdateSyncStatus(t *testing.T) {
	a := &CloudAccount{}
	syncTime := time.Now()
	a.UpdateSyncStatus(syncTime, 42)

	assert.Equal(t, &syncTime, a.LastSyncTime)
	assert.Equal(t, int64(42), a.AssetCount)
	assert.NotZero(t, a.UTime)
}

func TestCloudAccount_UpdateTestStatus(t *testing.T) {
	a := &CloudAccount{}
	testTime := time.Now()
	a.UpdateTestStatus(testTime, CloudAccountStatusError, "connection refused")

	assert.Equal(t, &testTime, a.LastTestTime)
	assert.Equal(t, CloudAccountStatusError, a.Status)
	assert.Equal(t, "connection refused", a.ErrorMessage)
}

func TestCloudAccount_Validate(t *testing.T) {
	tests := []struct {
		name    string
		account CloudAccount
		wantErr string
	}{
		{"有效账号", CloudAccount{Name: "test", Provider: "aliyun", AccessKeyID: "ak", AccessKeySecret: "sk", TenantID: 1}, ""},
		{"缺少名称", CloudAccount{Provider: "aliyun", AccessKeyID: "ak", AccessKeySecret: "sk", TenantID: 1}, "account name"},
		{"缺少Provider", CloudAccount{Name: "test", AccessKeyID: "ak", AccessKeySecret: "sk", TenantID: 1}, "provider"},
		{"缺少AK", CloudAccount{Name: "test", Provider: "aliyun", AccessKeySecret: "sk", TenantID: 1}, "access key id"},
		{"缺少SK", CloudAccount{Name: "test", Provider: "aliyun", AccessKeyID: "ak", TenantID: 1}, "access key secret"},
		{"缺少TenantID", CloudAccount{Name: "test", Provider: "aliyun", AccessKeyID: "ak", AccessKeySecret: "sk"}, "tenant id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.account.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
