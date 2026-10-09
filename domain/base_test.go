package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTenantResource_Validate(t *testing.T) {
	tr := &TenantResource{TenantID: 0}
	assert.ErrorIs(t, tr.Validate(), ErrTenantIDRequired)

	tr2 := &TenantResource{TenantID: 6}
	assert.NoError(t, tr2.Validate())
}

func TestTenantResource_BelongsToTenant(t *testing.T) {
	tr := &TenantResource{TenantID: 6}
	assert.True(t, tr.BelongsToTenant(6))
	assert.False(t, tr.BelongsToTenant(7))
}

func TestTimeStamps_UpdateTimestamp(t *testing.T) {
	ts := &TimeStamps{}
	ts.UpdateTimestamp()
	assert.NotZero(t, ts.UpdateTime)
	assert.NotZero(t, ts.UTime)
	assert.Zero(t, ts.CTime) // CreateTime not touched
}

func TestTimeStamps_InitTimestamp(t *testing.T) {
	ts := &TimeStamps{}
	ts.InitTimestamp()
	assert.NotZero(t, ts.CreateTime)
	assert.NotZero(t, ts.UpdateTime)
	assert.NotZero(t, ts.CTime)
	assert.NotZero(t, ts.UTime)
	assert.Equal(t, ts.CTime, ts.UTime)
}
