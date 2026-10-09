package volcano

import (
	"testing"

	"github.com/volcengine/volcengine-go-sdk/service/waf"
)

func i32(v int32) *int32 { return &v }

func TestConvertToInstanceStatusAndTime(t *testing.T) {
	t.Run("Status=0 映射为 suspended", func(t *testing.T) {
		d := &waf.DataForListDomainOutput{
			Domain:      strPtrV("example.com"),
			Status:      i32(0),
			DefenceMode: i32(1),
		}
		a := &WAFAdapter{}
		inst := a.convertToInstance(d, "cn-beijing")

		if inst.Status != "suspended" {
			t.Fatalf("期望 suspended,实际 %s", inst.Status)
		}
	})

	t.Run("Status 非 0 保持 active", func(t *testing.T) {
		d := &waf.DataForListDomainOutput{
			Domain:      strPtrV("example.com"),
			Status:      i32(1),
			DefenceMode: i32(1),
		}
		a := &WAFAdapter{}
		inst := a.convertToInstance(d, "cn-beijing")

		if inst.Status != "active" {
			t.Fatalf("期望 active,实际 %s", inst.Status)
		}
	})

	t.Run("CreationTime 留空,UpdateTime 并入 Description", func(t *testing.T) {
		d := &waf.DataForListDomainOutput{
			Domain:      strPtrV("example.com"),
			DefenceMode: i32(1),
			UpdateTime:  strPtrV("2024-06-01 10:00:00"),
		}
		a := &WAFAdapter{}
		inst := a.convertToInstance(d, "cn-beijing")

		if inst.CreationTime != "" {
			t.Fatalf("CreationTime 应留空(列表接口无创建时间),实际 %s", inst.CreationTime)
		}
		if inst.Description != "updated=2024-06-01 10:00:00" {
			t.Fatalf("Description 应含更新时间,实际 %s", inst.Description)
		}
	})

	t.Run("无 UpdateTime 时 Description 为空", func(t *testing.T) {
		d := &waf.DataForListDomainOutput{
			Domain:      strPtrV("example.com"),
			DefenceMode: i32(1),
		}
		a := &WAFAdapter{}
		inst := a.convertToInstance(d, "cn-beijing")

		if inst.Description != "" {
			t.Fatalf("Description 应空,实际 %s", inst.Description)
		}
	})
}

func strPtrV(s string) *string { return &s }
