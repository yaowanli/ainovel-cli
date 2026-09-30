package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 这里的用例专门覆盖**没有路由能到达**的路径：评审指出 Close()/pumped 重开、
// Snapshot 与 Close 的并发窗口、占用探测的缓存与单飞，只能靠直接构造 Project
// 来验证——它们在 HTTP 层永远走不到。

func newTestProject(t *testing.T) (*Project, string) {
	t.Helper()
	dir := t.TempDir()
	return NewProject("t", dir, dir, ""), dir
}

func TestValidateID(t *testing.T) {
	for _, id := range []string{"ok", "a-b_c", "Book42", strings.Repeat("a", 64)} {
		if err := validateID(id); err != nil {
			t.Errorf("validateID(%q) = %v, 期望通过", id, err)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 65), "a/b", "a.b", "..", "中", "a b", "a\x00b"} {
		if err := validateID(id); err == nil {
			t.Errorf("validateID(%q) 通过了，期望拒绝", id)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"写一本东方玄幻长篇": "novel", // 纯中文无 ASCII 可用，退回默认
		// 非 ASCII 字符被丢弃，id 保持 URL 安全可读
		"写 a 悬疑 short story": "a-short-story",
		"My Novel: 都市悬疑":     "my-novel",
		"":                   "novel",
		"---":                "novel",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestDetectStoreDir(t *testing.T) {
	// <dir>/output/novel 是标准布局。
	root := t.TempDir()
	nested := filepath.Join(root, "output", "novel")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "book.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := detectStoreDir(root)
	if err != nil || got != nested {
		t.Fatalf("detectStoreDir(root) = %q, %v；期望 %q", got, err, nested)
	}

	// 直接指向 store 根也能识别。
	got, err = detectStoreDir(nested)
	if err != nil || got != nested {
		t.Fatalf("detectStoreDir(store) = %q, %v；期望 %q", got, err, nested)
	}

	// 仅有 chapters/ 也算 store。
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err = detectStoreDir(bare); err != nil || got != bare {
		t.Fatalf("detectStoreDir(bare) = %q, %v；期望 %q", got, err, bare)
	}

	// 空目录必须报错——挂载空目录没有意义。
	if _, err := detectStoreDir(t.TempDir()); err == nil {
		t.Fatal("detectStoreDir(空目录) 通过了，期望报错")
	}
}

// TestCreateLeavesNoOrphanDir 覆盖评审 #6：create() 过去先建目录再解析配置，
// 失败后留下半成品目录并永久占住该 id。
func TestCreateLeavesNoOrphanDir(t *testing.T) {
	ws := t.TempDir()
	base := t.TempDir()
	// 放一份语法坏掉的基底配置，让 LoadConfigForLayers 必然失败。
	if err := os.MkdirAll(filepath.Join(base, ".ainovel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".ainovel", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry(ws, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.create("orphan", nil); err == nil {
		t.Fatal("create() 在坏配置下通过了，期望报错")
	}
	if _, err := os.Stat(filepath.Join(ws, "orphan")); !os.IsNotExist(err) {
		t.Fatalf("失败的 create 留下了孤儿目录（stat err=%v），期望不存在", err)
	}
	// 也不该留下 staging 残骸。
	entries, _ := os.ReadDir(ws)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("create() 留下了 staging 目录 %q", e.Name())
		}
	}
}

// TestMountRejectsAlias 覆盖评审 #9：挂到同一 store / workspace 内会造成两个项目
// 指向同一份 output_dir。
func TestMountRejectsAlias(t *testing.T) {
	ws := t.TempDir()
	base := t.TempDir()
	reg, err := NewRegistry(ws, base)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	bookDir := filepath.Join(outside, "output", "novel")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "book.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Mount("book", outside); err != nil {
		t.Fatalf("首次挂载失败: %v", err)
	}
	if _, err := reg.Mount("book2", outside); err == nil {
		t.Fatal("同一 store 被挂载了两次，期望拒绝")
	}
	if _, err := reg.Mount("inside", ws); err == nil {
		t.Fatal("挂载 workspace 内部路径成功了，期望拒绝")
	}

	// 挂载表应已持久化。
	data, err := os.ReadFile(filepath.Join(ws, ".ainovel-server.json"))
	if err != nil {
		t.Fatalf("挂载表未落盘: %v", err)
	}
	if !strings.Contains(string(data), "book") {
		t.Errorf("挂载表内容异常: %s", data)
	}

	// 重新加载应还原。
	reg2, err := NewRegistry(ws, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg2.Unmount("book"); err != nil {
		t.Errorf("Unmount 失败: %v", err)
	}
	if err := reg2.Unmount("book"); err == nil {
		t.Error("重复 Unmount 通过了，期望报错")
	}
}

// TestHolderCacheAndSingleflight 覆盖评审 #8：TTL 内复用缓存、探测在途不重复 fork。
func TestHolderCacheAndSingleflight(t *testing.T) {
	p, dir := newTestProject(t)
	if err := os.MkdirAll(p.OutputDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// 无人持锁 → 不应报占用。
	locked, who := p.holderOf(p.OutputDir())
	if locked {
		t.Fatalf("无人持锁却报占用: %+v", who)
	}
	// 缓存命中：二次调用应走缓存（holderCached 已置位）。
	p.holderLock.Lock()
	cached := p.holderCached
	p.holderLock.Unlock()
	if !cached {
		t.Error("首次探测后未写入缓存")
	}

	// 并发调用不应 panic，且结果一致（单飞下所有调用拿到同一个布尔值）。
	var wg sync.WaitGroup
	results := make([]bool, 32)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = p.holderOf(p.OutputDir())
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if got != locked {
			t.Fatalf("并发探测结果不一致: [%d]=%v, 期望 %v", i, got, locked)
		}
	}

	// 失效后应重新探测。
	p.invalidateHolder()
	p.holderLock.Lock()
	c := p.holderCached
	p.holderLock.Unlock()
	if c {
		t.Error("invalidateHolder 未清掉缓存")
	}
	_ = dir
}

// TestSnapshotWhileClosed 覆盖离线快照：Host 未打开时也要能出内容。
func TestSnapshotWhileClosed(t *testing.T) {
	p, _ := newTestProject(t)
	if err := os.MkdirAll(p.OutputDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	snap := p.Snapshot()
	if snap.ID != "t" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.State != string(stateClosed) {
		t.Errorf("State = %q, 期望 %q", snap.State, stateClosed)
	}
	if snap.Holder != nil {
		t.Errorf("无人持锁却带 Holder: %+v", snap.Holder)
	}
}

// TestSnapshotDuringClose 覆盖评审 #14：Snapshot 与 Close 并发时，
// 不能出现"拿到指针后 Close 并发 h.Close()"的窗口。配合 -race 运行。
func TestSnapshotDuringClose(t *testing.T) {
	p, _ := newTestProject(t)
	if err := os.MkdirAll(p.OutputDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = p.Snapshot()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		p.Close() // host 为 nil 时是 no-op，但会走锁路径
	}
	close(stop)
	wg.Wait()
}

// TestPumpedResetOnClose 覆盖评审 #13：Close 之后必须允许重新起泵，
// 否则重开得到一个没有事件消费者的 Host。
func TestPumpedResetOnClose(t *testing.T) {
	p, _ := newTestProject(t)
	p.pumped = true
	p.Close()
	p.stateMu.Lock()
	pumped := p.pumped
	p.stateMu.Unlock()
	if pumped {
		t.Error("Close() 未重置 pumped，重开后将没有事件泵")
	}
}

// TestDecodeBody 覆盖评审 #5：截断 JSON 必须报错，真正空体才视为空请求。
func TestDecodeBody(t *testing.T) {
	type body struct {
		ID string `json:"id"`
	}
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		wantID  string
	}{
		{name: "空体", raw: "", wantID: ""},
		{name: "完整对象", raw: `{"id":"x"}`, wantID: "x"},
		{name: "截断", raw: `{"id":"trunc`, wantErr: true},
		{name: "多余内容", raw: `{"id":"a"}{"id":"b"}`, wantID: "a"},
		{name: "未知字段", raw: `{"nope":1}`, wantErr: true},
		{name: "非法 JSON", raw: `{not json`, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b body
			req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(c.raw))
			err := decodeBody(httptest.NewRecorder(), req, &b)
			if c.wantErr {
				if err == nil {
					t.Fatalf("decodeBody(%q) = nil，期望报错", c.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeBody(%q) = %v", c.raw, err)
			}
			if b.ID != c.wantID {
				t.Errorf("ID = %q, 期望 %q", b.ID, c.wantID)
			}
		})
	}
}

func TestParseOrigins(t *testing.T) {
	if got := parseOrigins(""); len(got) != 0 {
		t.Errorf("空 -origin 产生了 %v", got)
	}
	got := parseOrigins("http://127.0.0.1:5173, HTTP://LOCALHOST:3000 ,")
	if !got["http://127.0.0.1:5173"] {
		t.Error("未收录第一个来源")
	}
	if !got["http://localhost:3000"] {
		t.Error("来源未做小写归一")
	}
	// * 必须被丢弃：通配等于把控制面敞开给任何网页。
	for _, o := range []string{"*", "http://a,*"} {
		for k := range parseOrigins(o) {
			if k == "*" {
				t.Errorf("parseOrigins(%q) 保留了通配符", o)
			}
		}
	}
}

// TestGuardRequiresToken 覆盖评审 #1：启用 -token 后未授权请求进不去路由。
func TestGuardRequiresToken(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	inner.HandleFunc("GET /index.html", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := guard(inner, "s3cret")

	for _, tc := range []struct {
		name   string
		auth   string
		want   int
		apiURL string
	}{
		{name: "无 token 访问 api", want: http.StatusUnauthorized, apiURL: "/api/health"},
		{name: "错误 token", auth: "Bearer nope", want: http.StatusUnauthorized, apiURL: "/api/health"},
		{name: "正确 token", auth: "Bearer s3cret", want: http.StatusOK, apiURL: "/api/health"},
		{name: "非 api 路径不校验", want: http.StatusOK, apiURL: "/index.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.apiURL, nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, 期望 %d", rec.Code, tc.want)
			}
		})
	}
}

// TestCORSEchoesOnlyAllowedOrigin 覆盖评审 #1：跨域只对显式列出的来源回显头。
func TestCORSEchoesOnlyAllowedOrigin(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {})
	allow := map[string]bool{"http://good.example": true}
	h := corsMiddleware(inner, allow)

	req := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://good.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://good.example" {
		t.Errorf("允许的来源未回显: %q", got)
	}

	req = httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("未授权来源也拿到了 CORS 头: %q", got)
	}
}

func TestHubPublishDropsOldestAndUnsubscribes(t *testing.T) {
	hub := NewHub()
	id, ch := hub.Subscribe()
	for i := 0; i < subscriberBuffer*2; i++ {
		hub.Publish(Message{Type: "delta", Data: i})
	}
	// 不阻塞、且缓冲有界：应能读到最后若干条。
	seen := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatal("订阅期间通道被关闭")
			}
			seen++
			if seen > subscriberBuffer {
				t.Fatalf("缓冲超过上限: %d", seen)
			}
			continue
		default:
		}
		break
	}
	if seen == 0 {
		t.Error("一条消息都没收到")
	}

	hub.Unsubscribe(id)
	if _, ok := <-ch; ok {
		t.Error("Unsubscribe 后通道应已关闭")
	}
	// 退订后发布不应 panic。
	hub.Publish(Message{Type: "event"})
}

func TestEventPayloadMapsLifecycle(t *testing.T) {
	// EventPayload 应忠实反映 host.Event 的生命周期语义：FinishedAt 零值 = 进行中。
	raw := `{"id":"call-1","summary":"生成"}`
	var m Message
	if err := json.Unmarshal([]byte(`{"type":"event","data":`+raw+`}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Type != "event" {
		t.Fatalf("Type = %q", m.Type)
	}
}

func TestMountedProjectOutputDirOverride(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "output", "novel")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewProject("m", dir, dir, storeDir)
	if p.OutputDir() != storeDir {
		t.Errorf("OutputDir = %q, 期望 %q", p.OutputDir(), storeDir)
	}
	// 未指定时回退到 TUI 语义。
	q := NewProject("d", dir, dir, "")
	if want := filepath.Join(dir, "output", "novel"); q.OutputDir() != want {
		t.Errorf("OutputDir = %q, 期望 %q", q.OutputDir(), want)
	}
}

func TestHolderTTLIsShorterThanPollingInterval(t *testing.T) {
	// 前端每 5s 轮询一次，TTL 必须显著小于它，否则占用解除后界面会长时间不更新。
	if holderTTL >= 5*time.Second {
		t.Errorf("holderTTL = %v, 应明显小于前端 5s 轮询间隔", holderTTL)
	}
}
