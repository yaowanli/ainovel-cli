# ainovel-server

一个进程管理多本 AI 小说，对外提供 REST + SSE 控制面，附带一个零构建的单页前端。

它是 ainovel-cli 的**第三个入口**（前两个是 TUI 与 headless），复用同一套
`host.Host` 创作内核，不改动引擎逻辑。

## 为什么能一个进程跑多本书

`host.Host` 的三个关键性质让这件事不需要重写引擎：

| 性质 | 位置 | 对多项目的意义 |
| --- | --- | --- |
| 事实层是文件系统，`OutputDir` 是运行时字段 | `internal/bootstrap/config.go`（`OutputDir string json:"-"`） | 每本书一个绝对路径的 store，`internal/eval/runner.go` 已经在用它跑隔离用例 |
| 控制面是显式方法 | `StartPrepared` / `Resume` / `Steer` / `Continue` / `SetAdvanceMode` / `AdvanceOneChapter` / `Abort` / `Snapshot` | REST 端点一一对应，无需解析 TUI 事件反推意图 |
| 事件面是 channel | `Events()` / `Stream()` / `Done()` | 直接转成 SSE；`Event.ID` + `Running()` 天然支持"同一调用原地更新" |

### 项目 ≠ workspace 子目录

上游的"一本书"是 **`{cwd}/output/novel`**，不是某个固定根目录下的子目录。用 TUI
在任意目录起书时，那本书的项目目录就是那个 cwd。因此服务端不假定"项目 = workspace
子目录"，而是 `workspace 子目录 ∪ 挂载项`：挂载表落在
`workspace/.ainovel-server.json`（点目录，不会被当成项目扫描），重启后仍生效。
`POST /api/projects/mount` 会自动识别 `<dir>/output/novel` 与"dir 本身即 store 根"
两种布局。

`internal/host/book_lock.go` 用 `flock` 对小说目录加跨进程独占锁——**同一目录
不可能被两个实例同时打开**（TUI 开着的时候服务端会拿到 `ErrBookInUse`，这正是
期望行为），不同目录则完全隔离。

## 相对上游的两处改动

两处都是**加性**的，不改既有行为，方便后续 rebase 上游：

1. `internal/bootstrap/configfile.go`
   - `LoadConfigFor(projectDir)`：把"项目级配置"从进程 cwd 换成显式目录。
   - `LoadConfigForLayers(baseDir, projectDir)`：三层合并
     `~/.ainovel/config.json` → `<baseDir>/.ainovel/config.json` → `<projectDir>/.ainovel/config.json`。
     服务端把自己的配置目录当基底，所有项目共享凭证与默认模型。
2. `internal/host/options.go`
   - `WithUserRulesOptions(rules.LoadOptions)`：用户规则来源目录可注入。
     默认的 `rules.DefaultOptions()` 绑定进程 cwd，一个进程 N 本书时必须按项目目录注入。

## 运行

```bash
go build -o ../bin/ainovel-server ./cmd/ainovel-server

# 在本仓库根目录启动（这里的 ./.ainovel/config.json 是所有项目的共享基底）
./bin/ainovel-server -workspace workspace -base . -addr 127.0.0.1:8787
```

参数：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8787` | HTTP 监听地址 |
| `-workspace` | `workspace` | 项目根目录，每本书一个子目录 |
| `-base` | `.` | 共享配置基底目录（其 `.ainovel/config.json` 对所有项目生效） |
| `-openapi` | `true` | 允许跨域访问 API |
| `-v` | `false` | 调试日志 |

打开 <http://127.0.0.1:8787> 即是控制台。

## HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 存活与统计 |
| GET | `/api/projects` | 项目列表（附状态快照）= workspace 子目录 ∪ 挂载项 |
| POST | `/api/projects/mount` | 挂载已有书：`{id?, dir}`。`dir` 可为 TUI 工作目录（含 `output/novel`）或 store 根。不移动数据 |
| DELETE | `/api/projects/{id}/mount` | 取消挂载（不动磁盘） |
| POST | `/api/projects` | 新建项目：`{id?, prompt?, style?, model?, provider?, reasoning_effort?}`。`prompt` 非空则立即开写 |
| GET | `/api/projects/{id}` | 单项目状态快照 |
| POST | `/api/projects/{id}/start` | `{prompt}` 开新书并启动 |
| POST | `/api/projects/{id}/resume` | 从最近 checkpoint 恢复 |
| POST | `/api/projects/{id}/steer` | `{text}` 实时干预（运行中可用） |
| POST | `/api/projects/{id}/continue` | `{text}` 停机后继续 |
| POST | `/api/projects/{id}/abort` | 手动暂停引擎 |
| POST | `/api/projects/{id}/advance` | `{mode:"auto"\|"review"}` 切换推进模式 |
| POST | `/api/projects/{id}/next` | 逐章验收模式下放行一章 |
| POST | `/api/projects/{id}/model` | `{role,provider,model}` 或 `{role,thinking}` |
| GET | `/api/projects/{id}/events` | **SSE**：event / delta / clear / snapshot / error |
| GET | `/api/projects/{id}/chapters/{n}` | 章节正文 |
| GET | `/api/projects/{id}/sync-check` | 列出待 `/sync` 接纳的手工改动章节 |
| GET | `/api/events` | **SSE**：全部项目的快照变化（列表页只订阅这一路） |

所有控制类端点在引擎状态不允许时返回 `409` + 上游的原始中文错误文案——
门禁完全交给 `host.Host`，服务端不重复实现任何状态机。

## 事件泵与背压

`Host` 的 `events` 通道容量 100、`streamCh` 容量 256，且都是**丢最旧**语义
（`emitEvent` / `emitDelta` 里的 `default:` 分支）。所以每个项目有一个**专职泵
goroutine**（`Project.pump`）持续消费，再扇出给各 SSE 订阅者；每个订阅者独立
缓冲（512 条），满了丢自己的旧数据。慢浏览器不会反压到引擎。

## 占用检测（locked）

同一本书**不能**被两个实例同时驱动：上游 `internal/host/book_lock.go` 对 store 目录
持有跨进程 `flock` 独占锁，两个 Engine 会互相覆盖 checkpoint。服务端不去绕开这个
护栏，而是把它变成一个可观测状态：

- 快照里 `state=locked` + `holder: {kind, pid, name}`。`kind` 靠 `lsof` 反查持锁进程
  的可执行名得到（`server` / `cli` / `unknown`）——`flock` 本身不暴露持有者信息，
  `lsof` 不可用时退化为 `unknown`，但"被占用"这个事实不依赖它。
- 占用期间前端禁用全部控制按钮并显示占用者 PID；正文与进度照常可读（读的是磁盘事实）。
- 真正的接管仍然由上游裁决：这时任何控制端点都会拿到 `ErrBookInUse`。

注意 `kind=cli` 无法区分 TUI 与 headless——上游两者是同一个可执行文件。

## 已知取舍

- **不启用每本书的 `logs/*.log` 文件日志**。上游 `host.WithFileLog` 会替换
  **进程级** `slog` 默认 logger，多项目并发时后开的书会抢走先开的书的日志句柄，
  且 `Close()` 恢复的是过期 logger。服务端的可观测性由 SSE 事件流承担，
  slog 统一写 stderr。
- **`/simulate`、`/export` 未开 HTTP 端点**。上游 `Host.Simulate` 从进程 cwd 读
  `simulate/` 目录，与"一进程多本书"冲突，需要先把 SourceDir 参数化才能安全暴露。
- **`/import` 未开 HTTP 端点**。管线本身已经全异步（`<-chan imp.Event`），但它会
  长期占用 `host.exclusive`，暴露前需要补取消与进度端点。
- **重启即失忆**。服务端内存态（运行中的 Host）随进程退出消失，但**创作进度不会
  丢**——重启后对每个项目调 `/resume` 即可从 checkpoint 续跑。
