package iam

import "errors"

// ErrCloudUserNotFound 表示目标用户已不存在于云端（例如 RAM 用户已被删除）。
// 调用方可据此跳过该用户的权限查询，而不是当成同步故障上报。
var ErrCloudUserNotFound = errors.New("cloud user not found")
