package domain

import (
	"regexp"
	"testing"
)

func TestIsAttachmentAssetType(t *testing.T) {
	attachments := []string{"eni", "vswitch", "security_group", "disk", "snapshot", "image"}
	for _, at := range attachments {
		if !IsAttachmentAssetType(at) {
			t.Errorf("%s 应为附属资源", at)
		}
	}
	entities := []string{"ecs", "rds", "vpc", "eip", "lb", "cdn", "waf", "oss", "nas", "kafka", "elasticsearch", "redis", "mongodb", "dns"}
	for _, at := range entities {
		if IsAttachmentAssetType(at) {
			t.Errorf("%s 不应为附属资源", at)
		}
	}
}

func TestAttachmentModelUIDPattern(t *testing.T) {
	re := regexp.MustCompile(AttachmentModelUIDPattern())

	match := []string{
		"aliyun_eni", "volcengine_eni", "huawei_eni",
		"aliyun_security_group", "tencent_security_group",
		"volcengine_vswitch", "aliyun_disk", "aws_snapshot", "huawei_image",
	}
	for _, uid := range match {
		if !re.MatchString(uid) {
			t.Errorf("%s 应命中附属资源正则", uid)
		}
	}

	noMatch := []string{
		"aliyun_ecs", "volcano_ecs", "aws_rds", "aliyun_oss",
		"aliyun_elasticsearch", "volcengine_mongodb", "cloud_vm", "ecs",
	}
	for _, uid := range noMatch {
		if re.MatchString(uid) {
			t.Errorf("%s 不应命中附属资源正则", uid)
		}
	}
}
