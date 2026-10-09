package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// CloudUser Tests
// ============================================================================

func TestCloudUser_IsActive(t *testing.T) {
	assert.True(t, (&CloudUser{Status: CloudUserStatusActive}).IsActive())
	assert.False(t, (&CloudUser{Status: CloudUserStatusInactive}).IsActive())
	assert.False(t, (&CloudUser{Status: CloudUserStatusDeleted}).IsActive())
}

func TestCloudUser_Validate(t *testing.T) {
	tests := []struct {
		name    string
		user    CloudUser
		wantErr string
	}{
		{"有效用户", CloudUser{Username: "admin", UserType: CloudUserTypeRAMUser, CloudAccountID: 1, Provider: CloudProviderAliyun, TenantID: 1}, ""},
		{"缺少用户名", CloudUser{UserType: CloudUserTypeRAMUser, CloudAccountID: 1, Provider: CloudProviderAliyun, TenantID: 1}, "username"},
		{"缺少类型", CloudUser{Username: "admin", CloudAccountID: 1, Provider: CloudProviderAliyun, TenantID: 1}, "user type"},
		{"缺少账号ID", CloudUser{Username: "admin", UserType: CloudUserTypeRAMUser, Provider: CloudProviderAliyun, TenantID: 1}, "cloud account id"},
		{"缺少Provider", CloudUser{Username: "admin", UserType: CloudUserTypeRAMUser, CloudAccountID: 1, TenantID: 1}, "provider"},
		{"缺少TenantID", CloudUser{Username: "admin", UserType: CloudUserTypeRAMUser, CloudAccountID: 1, Provider: CloudProviderAliyun}, "tenant id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.user.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCloudUser_UpdateMetadata(t *testing.T) {
	u := &CloudUser{}
	meta := CloudUserMetadata{AccessKeyCount: 2, MFAEnabled: true}
	u.UpdateMetadata(meta)
	assert.Equal(t, 2, u.Metadata.AccessKeyCount)
	assert.True(t, u.Metadata.MFAEnabled)
	assert.NotZero(t, u.UTime)
}

// ============================================================================
// UserGroup Tests
// ============================================================================

func TestUserGroup_Validate(t *testing.T) {
	tests := []struct {
		name    string
		group   UserGroup
		wantErr string
	}{
		{"有效组", UserGroup{Name: "admins", CloudPlatforms: []CloudProvider{CloudProviderAliyun}, TenantID: 1}, ""},
		{"缺少名称", UserGroup{CloudPlatforms: []CloudProvider{CloudProviderAliyun}, TenantID: 1}, "group name"},
		{"缺少平台", UserGroup{Name: "admins", TenantID: 1}, "cloud platforms"},
		{"缺少TenantID", UserGroup{Name: "admins", CloudPlatforms: []CloudProvider{CloudProviderAliyun}}, "tenant id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.group.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestUserGroup_HasPolicy(t *testing.T) {
	g := &UserGroup{
		Policies: []PermissionPolicy{
			{PolicyID: "p1", Provider: CloudProviderAliyun},
			{PolicyID: "p2", Provider: CloudProviderAWS},
		},
	}
	assert.True(t, g.HasPolicy("p1", CloudProviderAliyun))
	assert.True(t, g.HasPolicy("p2", CloudProviderAWS))
	assert.False(t, g.HasPolicy("p1", CloudProviderAWS))
	assert.False(t, g.HasPolicy("p3", CloudProviderAliyun))
}

func TestUserGroup_AddPolicy(t *testing.T) {
	g := &UserGroup{}
	p := PermissionPolicy{PolicyID: "p1", Provider: CloudProviderAliyun}
	g.AddPolicy(p)
	assert.Len(t, g.Policies, 1)

	// 重复添加不会增加
	g.AddPolicy(p)
	assert.Len(t, g.Policies, 1)

	// 不同策略可以添加
	g.AddPolicy(PermissionPolicy{PolicyID: "p2", Provider: CloudProviderAWS})
	assert.Len(t, g.Policies, 2)
}

func TestUserGroup_RemovePolicy(t *testing.T) {
	g := &UserGroup{
		Policies: []PermissionPolicy{
			{PolicyID: "p1", Provider: CloudProviderAliyun},
			{PolicyID: "p2", Provider: CloudProviderAWS},
		},
	}
	g.RemovePolicy("p1", CloudProviderAliyun)
	assert.Len(t, g.Policies, 1)
	assert.Equal(t, "p2", g.Policies[0].PolicyID)

	// 移除不存在的不影响
	g.RemovePolicy("p999", CloudProviderAliyun)
	assert.Len(t, g.Policies, 1)
}

// ============================================================================
// SyncTask Tests
// ============================================================================

func TestSyncTask_Validate(t *testing.T) {
	valid := SyncTask{TaskType: SyncTaskTypeUserSync, TargetType: SyncTargetTypeUser, TargetID: 1, CloudAccountID: 1, Provider: CloudProviderAliyun}
	assert.NoError(t, valid.Validate())

	assert.Error(t, (&SyncTask{TargetType: SyncTargetTypeUser, TargetID: 1, CloudAccountID: 1, Provider: CloudProviderAliyun}).Validate())
	assert.Error(t, (&SyncTask{TaskType: SyncTaskTypeUserSync, TargetID: 1, CloudAccountID: 1, Provider: CloudProviderAliyun}).Validate())
	assert.Error(t, (&SyncTask{TaskType: SyncTaskTypeUserSync, TargetType: SyncTargetTypeUser, CloudAccountID: 1, Provider: CloudProviderAliyun}).Validate())
	assert.Error(t, (&SyncTask{TaskType: SyncTaskTypeUserSync, TargetType: SyncTargetTypeUser, TargetID: 1, Provider: CloudProviderAliyun}).Validate())
	assert.Error(t, (&SyncTask{TaskType: SyncTaskTypeUserSync, TargetType: SyncTargetTypeUser, TargetID: 1, CloudAccountID: 1}).Validate())
}

func TestSyncTask_StatusChecks(t *testing.T) {
	pending := &SyncTask{Status: SyncTaskStatusPending}
	assert.True(t, pending.IsPending())
	assert.False(t, pending.IsRunning())
	assert.False(t, pending.IsCompleted())

	running := &SyncTask{Status: SyncTaskStatusRunning}
	assert.True(t, running.IsRunning())
	assert.False(t, running.IsPending())

	success := &SyncTask{Status: SyncTaskStatusSuccess}
	assert.True(t, success.IsCompleted())

	failed := &SyncTask{Status: SyncTaskStatusFailed}
	assert.True(t, failed.IsCompleted())
}

func TestSyncTask_CanRetry(t *testing.T) {
	// 失败且未达最大重试
	task := &SyncTask{Status: SyncTaskStatusFailed, RetryCount: 1, MaxRetries: 3}
	assert.True(t, task.CanRetry())

	// 已达最大重试
	task2 := &SyncTask{Status: SyncTaskStatusFailed, RetryCount: 3, MaxRetries: 3}
	assert.False(t, task2.CanRetry())

	// 非失败状态
	task3 := &SyncTask{Status: SyncTaskStatusRunning, RetryCount: 0, MaxRetries: 3}
	assert.False(t, task3.CanRetry())
}

func TestSyncTask_MarkAsRunning(t *testing.T) {
	task := &SyncTask{Status: SyncTaskStatusPending}
	task.MarkAsRunning()
	assert.Equal(t, SyncTaskStatusRunning, task.Status)
	assert.NotNil(t, task.StartTime)
}

func TestSyncTask_MarkAsSuccess(t *testing.T) {
	task := &SyncTask{Status: SyncTaskStatusRunning}
	task.MarkAsSuccess()
	assert.Equal(t, SyncTaskStatusSuccess, task.Status)
	assert.Equal(t, 100, task.Progress)
	assert.NotNil(t, task.EndTime)
}

func TestSyncTask_MarkAsFailed(t *testing.T) {
	task := &SyncTask{Status: SyncTaskStatusRunning}
	task.MarkAsFailed("timeout")
	assert.Equal(t, SyncTaskStatusFailed, task.Status)
	assert.Equal(t, "timeout", task.ErrorMessage)
	assert.NotNil(t, task.EndTime)
}

func TestSyncTask_IncrementRetry(t *testing.T) {
	task := &SyncTask{RetryCount: 0}
	task.IncrementRetry()
	assert.Equal(t, 1, task.RetryCount)
	assert.Equal(t, SyncTaskStatusRetrying, task.Status)
}

func TestSyncTask_UpdateProgress(t *testing.T) {
	task := &SyncTask{}
	task.UpdateProgress(50)
	assert.Equal(t, 50, task.Progress)

	task.UpdateProgress(-10)
	assert.Equal(t, 0, task.Progress)

	task.UpdateProgress(200)
	assert.Equal(t, 100, task.Progress)
}

// ============================================================================
// PolicyTemplate Tests
// ============================================================================

func TestPolicyTemplate_Validate(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    PolicyTemplate
		wantErr string
	}{
		{"有效自定义模板", PolicyTemplate{Name: "test", Category: TemplateCategoryCustom, CloudPlatforms: []CloudProvider{CloudProviderAliyun}, TenantID: 1}, ""},
		{"有效内置模板", PolicyTemplate{Name: "test", Category: TemplateCategoryAdmin, CloudPlatforms: []CloudProvider{CloudProviderAliyun}, IsBuiltIn: true}, ""},
		{"缺少名称", PolicyTemplate{Category: TemplateCategoryCustom, CloudPlatforms: []CloudProvider{CloudProviderAliyun}, TenantID: 1}, "template name"},
		{"缺少分类", PolicyTemplate{Name: "test", CloudPlatforms: []CloudProvider{CloudProviderAliyun}, TenantID: 1}, "category"},
		{"缺少平台", PolicyTemplate{Name: "test", Category: TemplateCategoryCustom, TenantID: 1}, "cloud platforms"},
		{"自定义缺少TenantID", PolicyTemplate{Name: "test", Category: TemplateCategoryCustom, CloudPlatforms: []CloudProvider{CloudProviderAliyun}}, "tenant id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tmpl.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestPolicyTemplate_IsEditable(t *testing.T) {
	assert.True(t, (&PolicyTemplate{IsBuiltIn: false}).IsEditable())
	assert.False(t, (&PolicyTemplate{IsBuiltIn: true}).IsEditable())
}
