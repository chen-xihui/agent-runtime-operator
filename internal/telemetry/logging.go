package telemetry

import (
	"regexp"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// 敏感字段名（日志脱敏，P1-1 DLP）
var sensitiveFields = []string{
	"token", "secret", "password", "api_key", "apikey", "access_key", "auth", "credential",
}

// redactRe 匹配敏感字段键（含单复数）
var redactRe = regexp.MustCompile(`(?i)^(tokens?|secrets?|passwords?|api_keys?|apikeys?|access_keys?|auths?|credentials?|credential)([._-].*)?$`)

// RedactMap 对日志字段脱敏（敏感键值替换为 [REDACTED]）
func RedactMap(fields map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		if redactRe.MatchString(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}

// RedactString 对字符串脱敏（敏感键:值 替换）
func RedactString(s string) string {
	if s == "" {
		return s
	}
	result := s
	for _, field := range sensitiveFields {
		// 匹配 key=value 或 key: value 形式
		re := regexp.MustCompile(`(?i)('?` + regexp.QuoteMeta(field) + `'?\s*[=:]\s*)([^\s,;&]+)`)
		result = re.ReplaceAllString(result, "${1}[REDACTED]")
	}
	return result
}

// WithTenant 为 logger 注入租户/Agent 结构化字段（租户维度索引，M5）
func WithTenant(tenantID, agentID string) logr.Logger {
	logger := log.Log.WithValues("tenant", tenantID)
	if agentID != "" {
		logger = logger.WithValues("agent", agentID)
	}
	return logger
}

// IsSensitiveKey 判断键是否敏感
func IsSensitiveKey(key string) bool {
	return redactRe.MatchString(key)
}
