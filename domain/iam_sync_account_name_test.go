package domain

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// 契约守卫：前端 `src/api/types/iam.ts` 的 SyncTask 依赖 `cloud_account_name` 这个 JSON 键
// （列表「云账号」列直接渲染它）。字段一旦丢标签/改名，前端会静默显示空白，
// 且不会有任何编译或运行期报错 —— 这种静默退化必须在 SDK 侧钉死。
func TestSyncTaskJSONIncludesCloudAccountName(t *testing.T) {
	task := SyncTask{
		ID:               7,
		CloudAccountID:   1,
		CloudAccountName: "集团-阿里云",
	}

	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal SyncTask: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal SyncTask: %v", err)
	}

	name, ok := got["cloud_account_name"]
	if !ok {
		t.Fatalf("响应缺少 cloud_account_name 键（前端「云账号」列会显示空白），实际=%s", raw)
	}
	if name != "集团-阿里云" {
		t.Fatalf("cloud_account_name = %v, want 集团-阿里云", name)
	}
}

// 派生字段不得落库：它由服务层回填，写进 Mongo 只会产生与账号表不一致的陈旧副本。
func TestSyncTaskCloudAccountNameIsNotPersisted(t *testing.T) {
	task := SyncTask{ID: 7, CloudAccountName: "集团-阿里云"}

	raw, err := bson.Marshal(task)
	if err != nil {
		t.Fatalf("bson marshal SyncTask: %v", err)
	}

	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("bson unmarshal SyncTask: %v", err)
	}

	for _, key := range []string{"cloud_account_name", "cloudaccountname"} {
		if _, exists := doc[key]; exists {
			t.Fatalf("派生字段不应落库，却写入了 %q（doc=%v）", key, doc)
		}
	}
}
