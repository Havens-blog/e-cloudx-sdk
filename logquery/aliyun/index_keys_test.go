package aliyun

import "testing"

// passthroughColumn 判定:dimColumnExpr 与 dimGroupExpr 映射列都不应走透传
// 字面索引校验(dimGroupExpr 的 cache_hit→hit_info 若按字面 "cache_hit" 查
// GetIndex 必误报"未开启分析索引")——回归见 index_keys.go passthroughColumn。
func TestPassthroughColumn_GroupMappedIsNotPassthrough(t *testing.T) {
	cases := []struct {
		name    string
		kind    mapperKind
		dim     string
		wantCol string
		wantOK  bool
	}{
		{"dcdn cache_hit 映射 dimGroupExpr", kindDCDN, "cache_hit", "", false},
		{"cdnoffline cache_hit 映射 dimGroupExpr", kindCDNOffline, "cache_hit", "", false},
		{"akamai cache_hit 映射 dimGroupExpr", kindAkamaiCDN, "cache_hit", "", false},
		{"dcdn host 映射 dimColumnExpr", kindDCDN, "host", "", false},
		{"dcdn status 映射 dimColumnExpr", kindDCDN, "status", "", false},
		{"dcdn url 映射 dimColumnExpr", kindDCDN, "url", "", false},
		{"akamai action 映射 dimGroupExpr", kindAkamaiWAF, "action", "", false},
		// 真透传列(不在两表)仍按字面透传,交给 indexHint 校验
		{"dcdn 未收录列 id 透传", kindDCDN, "id", "id", true},
		{"空维度", kindDCDN, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col, ok := passthroughColumn(tc.kind, tc.dim)
			if col != tc.wantCol || ok != tc.wantOK {
				t.Fatalf("passthroughColumn(%q, %q) = (%q, %v), want (%q, %v)",
					tc.kind, tc.dim, col, ok, tc.wantCol, tc.wantOK)
			}
		})
	}
}

// indexHint 仍对真透传列生效:已索引返回空,未索引给出可操作提示。
func TestIndexHint(t *testing.T) {
	if got := indexHint("id", []string{"id", "host"}); got != "" {
		t.Fatalf("indexHint(已索引) = %q, want 空", got)
	}
	got := indexHint("nosuch", []string{"id", "host"})
	if got == "" || !containsStr(got, "nosuch 未开启分析索引") {
		t.Fatalf("indexHint(未索引) = %q, 应含 'nosuch 未开启分析索引'", got)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}