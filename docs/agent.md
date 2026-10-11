# Agent 操作规范

避免多 Day 实施中的细节漂移(目录乱跑、协议换栈、风格走样)。
与 `spec.md`(做什么)/ `plan.md`(按天做什么)的关系:本文件写"不要做错什么 + 已定下的硬决策"。

## 1. 硬边界

- 工作区:只写到 `YOUR WORKSPACE DIRECTORY`,不碰 Desktop / Downloads / /tmp 根
- git 写操作:不替用户执行 commit / push / reset / `checkout --`,只生成提交信息措辞
- 顶层目录:不擅自新增 / 改名 / 移动 cmd/ internal/ apps/ docs/,发现被外部工具改动先停下报告
- 依赖:不引入第三方 Go module,除非 plan.md 显式允许或与用户对齐
- Provider 边界:Agent 依赖统一 `Provider` 接口；当前适配器使用 OpenAI 兼容协议，新增协议在 Provider 层实现并接入配置工厂
- 退出码:成功 0;运行时错误 1;配置/输入错误 2

## 2. 项目布局

```text
apps/                                # Go module 根(不是仓库根)
├── cmd/minicode/main.go             # CLI 入口
├── internal/cli/                    # CLI 参数、输入、终端展示和 Markdown 渲染
│   ├── run.go                       # 配置、信号与会话组装
│   ├── input.go / input_display.go  # 多行编辑、按键与显示
│   ├── approval.go                  # 工具审批与自动批准
│   └── output.go / markdown.go      # 流式预览、输出与 Markdown
├── internal/agent/                  # 模型循环、消息历史与会话恢复
│   ├── run.go / prompt.go           # 会话、任务循环与系统 Prompt
│   ├── stream.go                    # 自动选择流式能力、聚合文本与工具参数
│   └── output.go / approval.go      # 展示、统计、流式预览与审批接口
├── internal/config/                 # 固定路径 TOML 配置读取与校验
├── internal/session/                # 会话快照校验、读取与原子保存
├── internal/tools/                  # 工具实现 + 注册机制
│   ├── registry.go                  # Tool、ToolRegistry、参数公共校验
│   ├── bash.go                      # RunBash + execute*Tool + build*Tool
│   ├── read.go / write.go / edit.go # 同上,按工具分文件
│   └── filesystem.go                # FileTools、工作区句柄与原子写入
├── internal/provider/               # Provider 抽象 + OpenAI 兼容客户端(纯源码,无 _test.go)
│   ├── provider.go                  # 接口、流式契约与配置工厂
│   ├── types.go                     # 公共消息、响应、工具调用与错误
│   ├── openai.go                    # 非流式调用与共用请求校验
│   └── openai_stream.go             # SSE 请求、解析、取消与关闭
├── internal/telemetry/              # OpenTelemetry / Langfuse 追踪配置
└── test/                            # 测试单独目录,black-box
    ├── cli/                        # CLI、输入、终端显示与退出
    ├── agent/                      # Agent、流式聚合、错误回传与持久化
    ├── tools/                      # 工具行为与取消
    └── provider/                   # 模型协议、SSE、取消与追踪
```

- 后续 Day:源码 `apps/internal/<name>/`,测试 `apps/test/<name>/`,同名目录
- 测试文件名与被测源文件同名对应；工具实现与注册机制位于 `apps/internal/tools/`
- `internal/` 不依赖 `cmd/`；第三方渲染与终端依赖集中在 `internal/cli/`，`provider` 和 `agent` 使用 OpenTelemetry 进行追踪
- go 命令全部 `go -C apps build/test/vet ./...`

## 3. 编码风格

- 中文 doc comment + 包注释
- 固定配置常量统一放在文件顶部（import 之后），不在函数内部声明 const
- 工具名称等业务标识使用命名常量，在声明和分发中复用，避免魔法值
- 错误一律 `fmt.Errorf("context: %w", err)` 包装
- `main` 只调用 `os.Exit(cli.Run(...))`；配置、输入、展示和退出码决策放在 `internal/cli`，通过 `Run(args, stdin, stdout, stderr) int` 测试
- HTTP handler 阻塞 ctx 时用 `select { case <-ctx.Done(): case <-time.After(backup): }` 防 `srv.Close()` hang
- 测试覆盖正常 + 至少一个错误路径

轻量原则:写前先问 ① 必要抽象吗 ② dead field 吗 ③ 1 处常量要抽吗 ④ 中间层能拆吗 ⑤ 注释自明吗 ⑥ doc-only 导出真需要吗。
判断:新读者从 0 读这段,删掉是否更省力?是 → 删;否 → 留。

## 4. Day 完成前自检

1. `go -C apps build ./...` → 0
2. `go -C apps vet ./...` → 0 告警
3. `go -C apps test -count=1 ./...` → 全过
4. 端到端 smoke:mock OpenAI 端点,跑 happy + 401
5. 文档只在用户明确要求时更新(见 `AGENTS.md`);未获授权不要主动改 `docs/` 和 `README.md`
6. `git status` 复核

## 5. 决策记录

### Day 1(2026-08-15)

- **Provider 协议 = OpenAI 兼容**(`/chat/completions`、Bearer、application/json)。兼容该协议的模型服务可复用同一客户端，通过地址、密钥和模型名切换。
- **API 错误格式兼容**:OpenAI 嵌套 `{"error": {...}}` 与平铺均可解析，非 JSON HTTP 错误也统一返回带状态码的 `APIError`
- **当前配置（2026-10-10 调整）**：CLI 只读取启动目录下固定路径 `apps/config/config.toml`；模型字段 `api_key`、`base_url`、`name` 必填，`provider` 可选且默认为 `"openai"`，Langfuse 配置可选。旧 `.env`、配置环境变量和模型配置 flag 已移除，配置示例见 `README.md` 和 `tutorial.md`
- **配置校验**：使用严格 TOML 解析，拒绝未知字段；解析错误不回显可能含密钥的原始配置行
- **目录布局 = `apps/`**:monorepo-friendly。本次实施观察到 `go mod tidy` 后目录被外部自动化从根 cmd/ internal/ 重组为 apps/,agent 接受,后续发现再改动先停下报告

### Day 2(2026-08-29)

- **工具协议 = Chat Completions `tools` / `tool_calls`**：函数声明携带 JSON Schema，调用参数在线格式保持 JSON 字符串
- **参数校验边界**：Provider 校验调用 ID、类型、名称和顶层 JSON 对象；required、字段类型与未知字段由具体工具在执行前校验
- **`strict` 默认不发送**：协议结构保留可选字段，但为兼容不同 OpenAI 风格服务不强制开启，宿主侧参数校验不能省略
- **消息 content 可空**：`Message.Content` 使用 `*string` 且不设 `omitempty`，保留 assistant 工具调用的 `null`，空工具结果仍回传 `""`
- **工具结果结构**：`role: tool` + `tool_call_id` + `content`，一个调用对应一条结果消息
- **Day 2 范围**：最初仅声明并展示工具调用；后续按用户要求接入真实 bash 执行和最小 Agent Loop。

### bash 执行与最小 Agent Loop

- **保留现有协议**：使用 `bash{command}`，通过 `bash -c` 在启动 CLI 的工作目录执行，每次调用使用独立 shell。
- **审批执行**：`bash`、`write`、`edit` 默认逐次展示调用并等待用户批准；拒绝时不执行，并把拒绝结果回传模型。`--yes` 可自动批准。命令内容不做黑名单检查或危险命令硬拦截。
- **执行边界**：最多 500 轮可使用工具的模型请求；达到上限后额外请求一次纯文本总结，不再提供或执行工具。总结沿用整项任务的 timeout，失败时输出停止说明；达到上限仍返回非零退出码。取消时终止命令进程组，单次命令输出最多保留 64 KiB。
- **模型输出策略（2026-10-10 调整）**：普通 Agent 请求和达到轮数上限后的总结不设置 `max_tokens`，使用模型服务的默认输出长度。底层 `ChatRequest.MaxTokens` 保留为显式请求字段，值为 0 时不发送，客户端不自动填充或覆盖。
- **实现范围**：简单循环和 bash 执行函数，不提前引入工具注册表、交互会话和持久化。
- **职责划分（按当前实现更新）**：`internal/agent` 管理模型循环、消息历史、轮数和工具分发；工具协议适配与注册表位于 `internal/tools`。`internal/cli` 负责参数、输入、任务取消、展示和退出码，`cmd/minicode` 只保留启动入口。
- **展示边界**：Agent 通过 `Output` 交付最终文本、模型与工具状态及任务统计，通过可选的 `StreamOutput` 交付增量预览；不依赖终端渲染。工具原始结果回传模型并进入历史，审批信息由 CLI 展示；终止任务的错误由 `Turn` 返回，`Run` 通过 `ToolError` 展示并决定退出码。

### CLI Markdown 渲染

- **职责划分（2026-10-02 调整）**：终端检测、宽度读取和 Glamour 渲染集中到 `internal/cli/markdown.go`；`cmd/minicode` 只调用 `cli.Run`。
- **输出规则**：终端中的模型回复渲染 Markdown，按终端宽度换行；管道、文件和渲染失败时输出原文。工具输出和回传模型的消息保持原样。
- **主题**：默认使用 `dracula`，通过 `GLAMOUR_STYLE` 覆盖。

### CLI 多行编辑与终端恢复（2026-10-02）

- **输入边界**：Agent 通过 `Input` 接口读取完整任务；终端支持多行编辑，管道继续逐行读取。上下键只移动当前输入光标，不浏览历史；`Shift+Enter` / `Ctrl+J` 换行，`Enter` 提交。
- **显示一致性**：复用 readline 的缓冲区编辑和 raw 模式，由 `inputDisplay` 统一计算真实换行、软折行、显示列和滚动区域；垂直移动使用同一套显示行布局。
- **键盘协议生命周期**：只在编辑输入时启用扩展键盘协议，转换编码后的 Ctrl+C，并在离开 raw 模式时恢复。模型和工具执行期间不得遗留该协议。
- **取消语义**：Ctrl+C 退出整个会话；任务返回后根 context 已取消时，不再启动下一次输入 goroutine，避免退出时重新进入 raw 模式。
- **当前输出生命周期（Day 11 调整）**：模型文本增量放在备用屏幕，每次流结束或发生错误、超时、取消时恢复主屏幕；完整最终回答、工具状态和审批信息留在主屏幕。非交互模式不展示流式预览或过程状态。
- **验证边界**：除提交内容外，还需覆盖 PTY 中的实际显示与光标，以及进程退出后的键盘协议和屏幕恢复；测试入口位于 `apps/test/cli/`。

### Day 8 任务统计（2026-10-08）

- **统计范围**：每次 `Session.Turn` 独立累计耗时、模型调用次数、进入处理流程的工具调用次数，以及模型响应中存在的 `usage`；达到轮数上限后的总结请求也计入模型调用和 token。
- **缺失语义**：`ChatResponse.Usage` 可空；全部缺失时显示 `Token 不可用`，部分缺失时只累计已有数据并标记 `（部分统计）`，不在本地估算未知 token。
- **总数兼容**：若服务返回输入或输出 token、但 `total_tokens` 为零，则使用输入与输出之和补全该响应的总数。
- **输出边界**：交互模式把统计写入 stdout；非交互模式写入 stderr，保证 stdout 仍可作为纯模型回答进入管道。成功、失败、取消和超时路径都必须发送任务统计。

### Day 9 Session 持久化（2026-10-09）

- **默认保存**：新会话生成标准小写 UUID，立即创建 `.minicode/sessions/<id>.json`，并在每轮结束后原子更新。
- **恢复入口**：CLI 输出当前会话 ID；`--session <id>` 只恢复当前工作区中已经存在的存档，不接受文件路径或空 ID。仅运行这条命令即可进入普通输入循环，无需附加提示词。
- **恢复边界**：存档保留用户、assistant 和 tool 协议消息及上一轮状态；系统 Prompt 按当前实现重新生成，不恢复进程、审批状态或文件读取缓存。
- **校验与权限**：校验 UUID、版本、工作区和工具调用配对；临时文件权限为 `0600`，`.minicode/` 不应提交到 Git。

### Day 10 Provider 抽象（2026-10-10）

- **调用边界**：Agent 的 Session 和最终总结依赖 `Provider` 抽象，不依赖具体客户端；Day 11 已接入可选的 `StreamingProvider`。消息历史、工具审批和执行仍由 Agent 管理。
- **协议选择**：CLI 通过 `provider.New` 创建实现，空值或 `"openai"` 选择 OpenAI 兼容客户端，未知协议报错。新增专用协议需要实现 `Provider` 并接入工厂，不能仅添加 Schema。
- **统一数据与错误**：复用 `ChatRequest`、`ChatResponse`、`Message`、工具调用和可选 `Usage`；服务 HTTP 错误转换为 `APIError`，网络与取消错误保留原有错误链。
- **流式契约**：`StreamingProvider` 是可选能力，定义 `ChatStream`、`Recv/Close` 和文本、工具调用参数的增量事件；Day 11 已实现 OpenAI 兼容客户端的 SSE 请求与解析。
- **测试替身**：Fake Provider 只需实现 `Chat`，按测试预设返回文本、工具调用、usage 或错误，无需 API Key 或真实 HTTP 请求；测试覆盖真实 read 工具、跨轮历史、错误恢复及轮数上限后的总结。
- **追踪可选**：Provider 接口不要求实现 `Tracer()`；实现提供它时 Agent 复用 tracer，否则使用 no-op tracer。

### Day 11 SSE 流式调用（2026-10-11 同步）

- **自动启用**：普通任务和轮数上限后的总结自动使用 Provider 的流式能力，不增加 CLI、配置文件或 Session 开关。不支持流式的 Provider 可使用 `Chat`；API、网络与解析错误不能自动重发为非流式请求。
- **请求协议**：发送 `stream: true`、`stream_options.include_usage: true` 和 `Accept: text/event-stream`；与非流式调用共用参数校验及默认模型选择。
- **事件解析**：按空行分隔，解析 `event` 和 `data`，支持多行数据、LF / CRLF / CR、首行 BOM、注释及心跳；单个 SSE 事件限制为 4 MiB，只接受索引为 0 的单候选回复。
- **结束与用量**：正常结束必须收到 `[DONE]`；`finish_reason` 不关闭流，后面可能还有独立 `usage` 事件。提前 EOF 是错误，不能当作完整回复。
- **执行边界**：按工具索引累积名称和参数，要求索引连续、ID 和类型稳定；完整结束后复用协议校验，再进入审批及执行。参数片段和临时文本不写入消息历史，流失败时不执行该次回复中的工具。
- **展示边界**：`StreamOutput` 仅负责实时预览与清理。终端在备用屏幕展示原文增量，最终完整回复在主屏幕渲染 Markdown；管道不展示预览。当前没有逐字符打字机效果，Day 12 尚未全部完成。
- **资源与追踪**：每条流都需要关闭，context 取消或并发 `Close` 必须中断读取；成功、错误、超时和取消均清理预览并结束 generation span。最终用量计入任务统计，完整事件数据及错误继续上报 Langfuse。
- **测试覆盖**：Provider 验证真实 HTTP 分片、事件分隔、长文本、用量、异常、取消与追踪；Agent 验证交错工具分片、完整结束前不执行、失败后可继续和最终总结；CLI 验证自动流式请求、存档、统计、管道输出及 PTY 中断恢复。HTTP 测试服务直接返回 SSE，不依赖关闭流式的参数。

## 6. 已知陷阱

1. bash session 不保留 cwd,需 `cd path && cmd` 或 `go -C path cmd`,不要假设 PWD 已被切过
2. Edit/Write 路径相对仓库根,不是 shell cwd
3. httptest handler 阻塞 `r.Context().Done()` 不会在客户端断开时立即返回,必须加 server-side 兜底超时
4. OpenAI 错误嵌套在 `error` 字段下,只用平铺解析会退化成原始 body
5. 用户偏好:微信端纯文本;commit 用 `conventional + 中文 subject`;能查文件/plan/spec 就查,不替用户瞎猜
6. `agent.Session.Run` 按轮启动 `Input.Readline`，任务执行期间不预读下一轮输入；执行前确认复用同一个输入适配器，必须避免并发读取 stdin。管道模式会把审批答案作为输入消费，自动化场景可显式使用 `--yes`
7. 终端模拟器 vt10x 会把 Kitty 协议开关误解为光标恢复；显示测试需忽略这两个控制序列，协议配对和退出恢复必须另由输入及 CLI 子进程测试检查
8. 流式 `finish_reason` 后可能还有 `usage`，不能提前关闭；缺少 `[DONE]` 或工具参数尚未收齐时不能执行工具、输出最终回复或保存半截 assistant 消息

任何与本规范冲突的改动,先改本文件,再改实现。
