package aliyun

import "testing"

// TestFormatGroupName 资源组名称合成：代码 + 中文备注；边界（中文缺失/相同/代码为空）退化。
func TestFormatGroupName(t *testing.T) {
	cases := []struct {
		name        string
		displayName string
		want        string
	}{
		{"JLC-3DP", "3D打印", "JLC-3DP（3D打印）"},
		{"JLC-3DP", "", "JLC-3DP"},         // 无中文名退化为代码
		{"JLC-3DP", "JLC-3DP", "JLC-3DP"},  // 中文与代码相同退化为代码
		{"", "3D打印", "3D打印"},             // 代码为空降级中文名
		{"", "", ""},                       // 都为空返回空
	}

	for _, c := range cases {
		if got := formatGroupName(c.name, c.displayName); got != c.want {
			t.Errorf("formatGroupName(%q, %q) = %q, want %q", c.name, c.displayName, got, c.want)
		}
	}
}