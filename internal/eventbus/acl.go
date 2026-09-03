// acl.go 落地 R-3 设计约束：各租户使用独立 NATS user/token + subject 级 ACL。
// 生成 nats-server.conf（authorization 段），operator 账号全权限，每租户账号仅能
// 发布/订阅本租户 subject（<prefix>.<tenant>.events.>），从平台层面强制租户隔离。
package eventbus

import (
	"fmt"
	"sort"
	"strings"
)

// TenantCredential 单个租户的 NATS 账号凭据
type TenantCredential struct {
	TenantID string
	User     string
	Password string
}

// ACLSpec ACL 生成输入
type ACLSpec struct {
	Prefix      string // subject 前缀，默认 agent-runtime
	OperatorUser string // operator/worker 全权限账号
	OperatorPass string
	Tenants     []TenantCredential
	// AllowJetStream 是否允许租户访问 JetStream 系统 subject（发布/订阅事件需）
	AllowJetStream bool
}

// BuildACLConfig 生成 nats-server.conf 的 authorization 段（含 users + permissions）。
func BuildACLConfig(spec ACLSpec) string {
	prefix := spec.Prefix
	if prefix == "" {
		prefix = "agent-runtime"
	}
	var b strings.Builder

	b.WriteString("authorization {\n")
	b.WriteString("  # agent-runtime 事件总线租户隔离（R-3）\n")
	b.WriteString("  # operator/worker 全权限；每租户仅限本租户 subject\n")
	b.WriteString("  users = [\n")

	// operator 全权限账号
	op := spec.OperatorUser
	if op == "" {
		op = "operator"
	}
	opp := spec.OperatorPass
	if opp == "" {
		opp = "operator-secret"
	}
	fmt.Fprintf(&b, "    { user: %q, password: %q, permissions: { publish: [\">\"], subscribe: [\">\"] } },\n", op, opp)

	// 每租户受限账号
	for _, t := range spec.Tenants {
		user := t.User
		if user == "" {
			user = "tenant-" + t.TenantID
		}
		pass := t.Password
		if pass == "" {
			pass = "secret-" + t.TenantID
		}
		fmt.Fprintf(&b, "    { user: %q, password: %q, permissions: {\n", user, pass)
		// 本租户事件 subject + 必要的 JetStream/INBOX 系统 subject
		pub := []string{fmt.Sprintf("%s.%s.events.>", prefix, t.TenantID), "_INBOX.>"}
		sub := []string{fmt.Sprintf("%s.%s.events.>", prefix, t.TenantID), "_INBOX.>"}
		if spec.AllowJetStream {
			pub = append(pub, "$JS.API.>")
			sub = append(sub, "$JS.API.>", "$JS.ACK.>")
		}
		fmt.Fprintf(&b, "      publish: [%s],\n", quoteList(pub))
		fmt.Fprintf(&b, "      subscribe: [%s]\n", quoteList(sub))
		b.WriteString("    } },\n")
	}
	b.WriteString("  ]\n")
	b.WriteString("}\n")
	return b.String()
}

// RenderACL 返回完整可用的 nats-server.conf（authorization + jetstream 开关）
func RenderACL(spec ACLSpec, enableJetStream bool) string {
	var b strings.Builder
	b.WriteString("port: 4222\n")
	b.WriteString("http: 8222\n")
	if enableJetStream {
		b.WriteString("jetstream { store_dir: /tmp/nats-js }\n")
	}
	b.WriteString("\n")
	b.WriteString(BuildACLConfig(spec))
	return b.String()
}

// ValidateTenantScope 校验某租户凭据能访问的 subject 集合（供客户端侧告警/自检）
// 返回该租户被允许前缀（不含通配）。客户端据此限定自身只发本租户事件。
func ValidateTenantScope(prefix, tenantID string) string {
	if prefix == "" {
		prefix = "agent-runtime"
	}
	return fmt.Sprintf("%s.%s.events.>", prefix, tenantID)
}

// quoteList 生成 ", "-分隔的引号列表
func quoteList(items []string) string {
	// 去重 + 排序保证确定性输出（便于测试断言）
	seen := map[string]bool{}
	var uniq []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			uniq = append(uniq, it)
		}
	}
	sort.Strings(uniq)
	q := make([]string, len(uniq))
	for i, u := range uniq {
		q[i] = fmt.Sprintf("%q", u)
	}
	return strings.Join(q, ", ")
}
