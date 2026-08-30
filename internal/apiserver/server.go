// Package apiserver 提供 Agent Runtime 平台 REST API（对接开放 SDK）。
// 通过 HTTP 暴露租户/Agent/Sandbox/Workflow 的管理操作，供外部系统集成。
package apiserver

import (
	"encoding/json"
	"net/http"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/audit"
	"github.com/example/agent-runtime-operator/sdk"
)

// Server REST API 服务器
type Server struct {
	sdk   *sdk.Client
	audit audit.Store
}

// New 创建 API Server
func New(c *sdk.Client) *Server {
	return &Server{sdk: c, audit: audit.NoopStore{}}
}

// WithAuditStore 设置审计存储（DLP 审计查询，P1-1）
func (s *Server) WithAuditStore(st audit.Store) *Server {
	if st != nil {
		s.audit = st
	}
	return s
}

// Handler 返回 HTTP 处理器（Go 1.22 method+path 路由）
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Tenant
	mux.HandleFunc("GET /api/v1/tenants", s.listTenants)
	mux.HandleFunc("GET /api/v1/tenants/{name}", s.getTenant)
	mux.HandleFunc("POST /api/v1/tenants", s.createTenant)

	// Agent
	mux.HandleFunc("GET /api/v1/tenants/{tenant}/agents", s.listAgents)
	mux.HandleFunc("POST /api/v1/tenants/{tenant}/agents", s.createAgent)

	// Sandbox
	mux.HandleFunc("GET /api/v1/tenants/{tenant}/sandboxes/{name}", s.getSandbox)
	mux.HandleFunc("POST /api/v1/tenants/{tenant}/sandboxes/{name}/suspend", s.suspendSandbox)
	mux.HandleFunc("POST /api/v1/tenants/{tenant}/sandboxes/{name}/resume", s.resumeSandbox)

	// Workflow
	mux.HandleFunc("POST /api/v1/tenants/{tenant}/workflows", s.createWorkflow)
	mux.HandleFunc("GET /api/v1/tenants/{tenant}/workflowruns/{name}", s.getWorkflowRun)

	// Audit（DLP 审计查询）
	mux.HandleFunc("GET /api/v1/audit", s.queryAudit)

	// 健康检查
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return withRecovery(mux)
}

// ===================== Tenant =====================

func (s *Server) listTenants(w http.ResponseWriter, r *http.Request) {
	list, err := s.sdk.ListTenants(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getTenant(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := s.sdk.GetTenant(r.Context(), name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	var t v1.Tenant
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if t.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant name required"})
		return
	}
	if err := s.sdk.CreateTenant(r.Context(), &t); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ===================== Agent =====================

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	list, err := s.sdk.ListAgents(r.Context(), tenant)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	var a v1.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if a.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent name required"})
		return
	}
	if err := s.sdk.CreateAgent(r.Context(), tenant, &a); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// ===================== Sandbox =====================

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	sb, err := s.sdk.GetSandbox(r.Context(), tenant, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sb)
}

func (s *Server) suspendSandbox(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	if err := s.sdk.SuspendSandbox(r.Context(), tenant, name); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "suspend requested", "sandbox": name})
}

func (s *Server) resumeSandbox(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	if err := s.sdk.ResumeSandbox(r.Context(), tenant, name); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resume requested", "sandbox": name})
}

// ===================== Workflow =====================

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	var wf v1.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if wf.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workflow name required"})
		return
	}
	if err := s.sdk.CreateWorkflow(r.Context(), tenant, &wf); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wf)
}

func (s *Server) getWorkflowRun(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	run, err := s.sdk.GetWorkflowRun(r.Context(), tenant, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// ===================== Audit =====================

// queryAudit 查询 DLP 审计记录（按租户/Agent/动作/资源过滤）
// GET /api/v1/audit?tenant=tenant-a&agent=reviewer&action=tool_call&resource=db.query&limit=50
func (s *Server) queryAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := audit.Filter{
		TenantID: q.Get("tenant"),
		AgentID:  q.Get("agent"),
		Action:   q.Get("action"),
		Resource: q.Get("resource"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	records, err := s.audit.Query(r.Context(), filter)
	if err != nil {
		writeError(w, err)
		return
	}
	if records == nil {
		records = []*audit.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"records": records})
}

// ===================== 辅助 =====================

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if apierrors.IsNotFound(err) {
		status = http.StatusNotFound
	}
	if apierrors.IsAlreadyExists(err) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// withRecovery panic 恢复中间件
func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
