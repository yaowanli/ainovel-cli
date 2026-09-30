package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/voocel/ainovel-cli/internal/webui"
)

// Handler 组装 HTTP 路由：REST 控制面 + SSE 事件流 + 内嵌单页前端。
func (r *Registry) Handler(cors bool) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"workspace": r.Workspace(),
			"projects":  len(r.list()),
			"watchers":  r.hub.Count(),
		})
	})

	// 全局事件流：所有项目的 snapshot 变化都从这里推，前端列表页只订阅一路。
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, req *http.Request) {
		serveSSE(w, req, r.hub, func() []Message {
			ps := r.list()
			out := make([]Message, 0, len(ps))
			for _, p := range ps {
				out = append(out, Message{Type: "snapshot", Data: p.Snapshot()})
			}
			return out
		})
	})

	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, req *http.Request) {
		ps := r.list()
		out := make([]SnapshotDTO, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.Snapshot())
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			ID              string         `json:"id"`
			Prompt          string         `json:"prompt"`
			Style           string         `json:"style"`
			Model           string         `json:"model"`
			Provider        string         `json:"provider"`
			ReasoningEffort string         `json:"reasoning_effort"`
			ContextWindow   int            `json:"context_window"`
			Extra           map[string]any `json:"extra"`
		}
		if err := decodeBody(req, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if body.ID == "" {
			body.ID = slugify(body.Prompt)
		}
		overrides := map[string]any{}
		if body.Style != "" {
			overrides["style"] = body.Style
		}
		if body.Model != "" {
			overrides["model"] = body.Model
		}
		if body.Provider != "" {
			overrides["provider"] = body.Provider
		}
		if body.ReasoningEffort != "" {
			overrides["reasoning_effort"] = body.ReasoningEffort
		}
		if body.ContextWindow > 0 {
			overrides["context_window"] = body.ContextWindow
		}
		for k, v := range body.Extra {
			overrides[k] = v
		}
		p, err := r.create(body.ID, overrides)
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		if strings.TrimSpace(body.Prompt) != "" {
			if err := p.Start(body.Prompt); err != nil {
				writeJSON(w, http.StatusAccepted, map[string]any{
					"id":       p.ID,
					"started":  false,
					"error":    err.Error(),
					"snapshot": p.Snapshot(),
				})
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": p.ID, "started": strings.TrimSpace(body.Prompt) != "", "snapshot": p.Snapshot(),
		})
	})

	mux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, p.Snapshot())
	})

	mux.HandleFunc("POST /api/projects/{id}/start", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := decodeBody(req, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		respond(w, p, p.Start(body.Prompt))
	})

	mux.HandleFunc("POST /api/projects/{id}/resume", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		respond(w, p, p.Resume())
	})

	// steer：运行中实时干预；continue：停机后继续。
	for _, action := range []string{"steer", "continue"} {
		a := action
		mux.HandleFunc("POST /api/projects/{id}/"+a, func(w http.ResponseWriter, req *http.Request) {
			p, err := r.get(req.PathValue("id"))
			if err != nil {
				writeErr(w, http.StatusNotFound, err)
				return
			}
			var body struct {
				Text string `json:"text"`
			}
			if err := decodeBody(req, &body); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			if a == "steer" {
				respond(w, p, p.Steer(body.Text))
			} else {
				respond(w, p, p.Continue(body.Text))
			}
		})
	}

	mux.HandleFunc("POST /api/projects/{id}/abort", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		respond(w, p, p.Abort())
	})

	mux.HandleFunc("POST /api/projects/{id}/advance", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if err := decodeBody(req, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		respond(w, p, p.SetAdvanceMode(body.Mode == "review"))
	})

	mux.HandleFunc("POST /api/projects/{id}/next", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		respond(w, p, p.Next())
	})

	mux.HandleFunc("POST /api/projects/{id}/model", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		var body struct {
			Role     string `json:"role"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Thinking string `json:"thinking"`
		}
		if err := decodeBody(req, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		role := body.Role
		if role == "" {
			role = "default"
		}
		if body.Model != "" {
			respond(w, p, p.SetModel(role, body.Provider, body.Model))
			return
		}
		respond(w, p, p.SetThinking(role, body.Thinking))
	})

	mux.HandleFunc("GET /api/projects/{id}/events", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		serveSSE(w, req, p.Hub(), func() []Message {
			return []Message{{Type: "snapshot", Data: p.Snapshot()}}
		})
	})

	// 章节正文按需拉取，不进 SSE：正文体积大且只在阅读时才需要。
	mux.HandleFunc("GET /api/projects/{id}/chapters/{n}", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		n, err := strconv.Atoi(req.PathValue("n"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("章节号非法: %s", req.PathValue("n")))
			return
		}
		content, err := p.Chapter(n)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chapter": n, "content": content})
	})

	mux.HandleFunc("GET /api/projects/{id}/sync-check", func(w http.ResponseWriter, req *http.Request) {
		p, err := r.get(req.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		chapters, err := p.SyncCheck()
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chapters": chapters})
	})

	mux.Handle("GET /", http.StripPrefix("/", webui.Handler()))

	if !cors {
		return mux
	}
	return corsMiddleware(mux)
}

func respond(w http.ResponseWriter, p *Project, err error) {
	if err != nil {
		p.setErr(err)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    err.Error(),
			"snapshot": p.Snapshot(),
		})
		return
	}
	p.setErr(nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "snapshot": p.Snapshot()})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// slugify 从创作需求派生一个可用的项目 id。
func slugify(prompt string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(prompt)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			// 中文等非 ASCII 字符跳过，避免 id 不可读
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		out = "novel"
	}
	return out
}
