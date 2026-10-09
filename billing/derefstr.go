package billing

// DerefStr 安全获取字符串指针的值（nil → ""，否则返回 *s）
func DerefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
