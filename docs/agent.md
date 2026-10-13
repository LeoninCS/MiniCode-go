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
apps/                                # Go module 根与本项目完整运行工作区
├── MiniCode.md                      # 常驻约定、常用命令与按需目录
├── .minicode/                       # 本地数据，Git 忽略
│   ├── memory/                      # 模型按任务 read 的专题详情
│   └── sessions/                    # 本工作区的会话存档
├── cmd/minicode/main.go             # CLI 入口
├── internal/cli/                    # CLI 参数、输入、终端展示和 Markdown 渲染
│   ├── run.go                       # 配置、信号与会话组装
│   ├── input.go / input_display.go  # 多行编辑、按键、鼠标事件与显示
│   ├── approval.go                  # 工具审批与自动批准
│   ├── output.go / markdown.go      # 展示接口、终端检测与 Markdown
│   ├── screen.go                    # 会话全屏、底部输入、历史视口与重排
│   ├── terminal_mode_unix.go        # Unix 回显恢复与窗口变化信号
│   ├── terminal_mode_other.go       # 其他平台的终端模式实现
│   └── stream_preview.go            # 实时 Markdown、合并刷新与视口
├── internal/agent/                  # 模型循环、消息历史与会话恢复
│   ├── run.go / prompt.go           # 会话、任务循环与系统 Prompt
│   ├── stream.go                    # 自动选择流式能力、聚合文本与工具参数
│   ├── compact.go                   # 三个压缩入口、预算重置与超限恢复
│   ├── memory.go                    # 项目记忆重新加载与写后刷新识别
│   └── output.go / approval.go      # 展示、统计、流式预览与审批接口
├── internal/config/                 # 工作区 TOML 配置与旧路径兼容
├── internal/session/                # 会话快照校验、读取与原子保存
├── internal/compaction/             # 固定上下文预算、近期原文保留与 LLM 摘要
│   └── trigger.go / compact.go / state.go / prompt.go
├── internal/memory/                 # 常驻加载与按需详情校验
│   └── load.go / files.go
├── internal/token/                  # 记忆与上下文共用的 token 粗估
│   └── estimate.go
├── internal/tools/                  # 工具实现 + 注册机制
│   ├── registry.go                  # Tool、ToolRegistry、参数公共校验
│   ├── bash.go                      # RunBash + execute*Tool + build*Tool
│   ├── read.go / write.go / edit.go # 同上,按工具分文件
│   └── filesystem.go                # FileTools、工作区句柄与原子写入
├── internal/provider/               # Provider 抽象 + OpenAI 兼容客户端(纯源码,无 _test.go)
│   ├── provider.go                  # 接口、流式契约与配置工厂
│   ├── types.go                     # 公共消息、响应、工具调用与错误
│   ├── messages.go                  # 公共消息序列与工具配对校验
│   ├── openai.go                    # 非流式调用与共用请求校验
│   └── openai_stream.go             # SSE 请求、解析、取消与关闭
├── internal/telemetry/              # OpenTelemetry / Langfuse 追踪配置
└── test/                            # 测试单独目录,black-box
    ├── cli/                        # CLI、输入、全屏滚动、终端显示与退出
    ├── agent/                      # Agent、流式聚合、持久化、记忆加载与维护
    ├── compaction/                 # 预算、分块、保留边界、连续压缩与语义验收
    ├── memory/                     # UTF-8、文件大小、预算、隔离与读取错误
    ├── tools/                      # 工具行为与取消
    └── provider/                   # 模型协议、SSE、取消与追踪
```

- 后续 Day:源码 `apps/internal/<name>/`,测试 `apps/test/<name>/`,同名目录
- 测试文件名与被测源文件同名对应；工具实现与注册机制位于 `apps/internal/tools/`
- `internal/` 不依赖 `cmd/`；第三方渲染与终端依赖集中在 `internal/cli/`，`provider` 和 `agent` 使用 OpenTelemetry 进行追踪
- 仓库根目录执行 `go -C apps build/test/vet ./...`；进入 `apps/` 后使用普通 `go build/test/vet ./...`。运行本项目 CLI 时以 `apps/` 为工作区，源码位置的 `apps/` 前缀用于仓库相对路径。

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
- **当前配置（2026-10-13 调整）**：CLI 优先读取启动目录内 `config/config.toml`，仅文件名确实不存在时兼容旧路径 `apps/config/config.toml`；首选文件不可读、是目录、符号链接断开、解析失败或缺少必填字段时不回退。模型字段 `api_key`、`base_url`、`name` 必填，`provider` 可选且默认为 `"openai"`，Langfuse 配置可选。旧 `.env`、配置环境变量和模型配置 flag 已移除，配置示例见 `README.md` 和 `tutorial.md`。
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
- **模型输出策略（2026-10-12 调整）**：普通 Agent 请求、达到轮数上限后的总结和上下文摘要均固定使用 `max_tokens = 8192`，不提供配置项。底层客户端仍保留显式请求字段，值为 0 时不发送，客户端不自动填充或覆盖。
- **实现范围**：简单循环和 bash 执行函数，不提前引入工具注册表、交互会话和持久化。
- **职责划分（按当前实现更新）**：`internal/agent` 管理模型循环、消息历史、轮数和工具分发；工具协议适配与注册表位于 `internal/tools`。`internal/cli` 负责参数、输入、任务取消、展示和退出码，`cmd/minicode` 只保留启动入口。
- **展示边界**：Agent 通过 `Output` 交付最终文本、模型与工具状态及任务统计，通过可选的 `StreamOutput` 交付增量预览；不依赖终端渲染。工具原始结果回传模型并进入历史，审批信息由 CLI 展示；终止任务的错误由 `Turn` 返回，`Run` 通过 `ToolError` 展示并决定退出码。

### CLI Markdown 渲染

- **职责划分（2026-10-13 同步）**：终端检测、尺寸读取和共用 Glamour 渲染器工厂位于 `internal/cli/markdown.go`；交互 CLI 的会话界面、实时预览与历史视口由 `screen.go` 管理，未附加会话屏幕的独立预览仍由 `stream_preview.go` 管理。`output.go` 接收 Agent 的展示事件；`cmd/minicode` 只调用 `cli.Run`。
- **输出规则**：终端中的模型回复渲染 Markdown，按终端宽度换行；管道、文件和渲染失败时输出原文。工具输出和回传模型的消息保持原样。
- **主题**：默认使用 `dracula`，通过 `GLAMOUR_STYLE` 覆盖。

### CLI 多行编辑与终端恢复（2026-10-02）

- **输入边界**：Agent 通过 `Input` 接口读取完整任务；终端支持多行编辑，管道继续逐行读取。上下键只移动当前输入光标，不浏览历史；`Shift+Enter` / `Ctrl+J` 换行，`Enter` 提交。
- **显示一致性**：复用 readline 的缓冲区编辑和 raw 模式，由 `inputDisplay` 统一计算真实换行、软折行、显示列和滚动区域；垂直移动使用同一套显示行布局。
- **键盘协议生命周期**：只在编辑输入时启用扩展键盘协议，转换编码后的 Ctrl+C，并在离开 raw 模式时恢复。模型和工具执行期间不得遗留该协议。
- **取消语义**：Ctrl+C 退出整个会话；任务返回后根 context 已取消时，不再启动下一次输入 goroutine，避免退出时重新进入 raw 模式。
- **当前输出生命周期（2026-10-13 同步）**：交互 CLI 在整段会话共用的备用屏幕实时渲染 Markdown；每次流结束或发生错误、超时、取消时清理当次预览，完整回答、工具状态和审批信息留在会话界面。仅退出会话时恢复光标、主屏幕及终端模式。非交互模式不展示流式预览或过程状态。
- **验证边界**：除提交内容外，还需覆盖 PTY 中的实际显示与光标，以及进程退出后的键盘协议和屏幕恢复；测试入口位于 `apps/test/cli/`。

### Day 8 任务统计（2026-10-08）

- **统计范围**：每次 `Session.Turn` 独立累计耗时、模型调用次数、进入处理流程的工具调用次数，以及模型响应中存在的 `usage`；达到轮数上限后的总结请求也计入模型调用和 token。
- **缺失语义**：`ChatResponse.Usage` 可空；全部缺失时显示 `Token 不可用`，部分缺失时只累计已有数据并标记 `（部分统计）`，不在本地估算未知 token。
- **总数兼容**：若服务返回输入或输出 token、但 `total_tokens` 为零，则使用输入与输出之和补全该响应的总数。
- **输出边界**：交互模式把统计写入 stdout；非交互模式写入 stderr，保证 stdout 仍可作为纯模型回答进入管道。成功、失败、取消和超时路径都必须发送任务统计。

### Day 9 Session 持久化（2026-10-09）

- **默认保存**：新会话生成标准小写 UUID，立即创建 `.minicode/sessions/<id>.json`，并在每轮结束后原子更新。
- **恢复入口**：CLI 输出当前会话 ID；`--session <id>` 只恢复当前工作区中已经存在的存档，不接受文件路径或空 ID。仅运行这条命令即可进入普通输入循环，无需附加提示词。
- **恢复边界**：v2 存档保留完整用户、assistant 和 tool 协议消息、独立压缩检查点及上一轮状态；系统 Prompt 按当前实现重新生成，模型上下文按检查点选择摘要与近期原文，不恢复进程、审批状态或文件读取缓存。v1 读取时迁移；已被旧版丢弃的原文无法恢复。
- **校验与权限**：校验 UUID、版本、工作区和工具调用配对；临时文件权限为 `0600`，`.minicode/` 不应提交到 Git。

### Day 10 Provider 抽象（2026-10-10）

- **调用边界**：Agent 的 Session 和最终总结依赖 `Provider` 抽象，不依赖具体客户端；Day 11 已接入可选的 `StreamingProvider`。消息历史、工具审批和执行仍由 Agent 管理。
- **协议选择**：CLI 通过 `provider.New` 创建实现，空值或 `"openai"` 选择 OpenAI 兼容客户端，未知协议报错。新增专用协议需要实现 `Provider` 并接入工厂，不能仅添加 Schema。
- **思考强度（2026-10-13 确认）**：当前配置与 `ChatRequest` 无思考强度字段，流式和非流式请求均省略 `reasoning_effort`，由服务或网关决定默认值。配置解析拒绝未知字段，不能直接在 TOML 中添加该字段。[GPT-5.6 Sol 官方模型说明](https://developers.openai.com/api/docs/models/gpt-5.6-sol) 的默认值为 `medium`；兼容网关可能覆盖，当前实际强度未得到确认，不能将使用该模型的验收记录为已显式设置 `medium`。
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
- **展示边界**：`StreamOutput` 仅负责实时预览与清理。当前 CLI 累计增量并在会话全屏界面实时渲染 Markdown，最终完整回复加入同一界面的展示记录；管道不展示预览。
- **资源与追踪**：每条流都需要关闭，context 取消或并发 `Close` 必须中断读取；成功、错误、超时和取消均清理预览并结束 generation span。最终用量计入任务统计，完整事件数据及错误继续上报 Langfuse。
- **测试覆盖**：Provider 验证真实 HTTP 分片、事件分隔、长文本、用量、异常、取消与追踪；Agent 验证交错工具分片、完整结束前不执行、失败后可继续和最终总结；CLI 验证自动流式请求、存档、统计、管道输出及 PTY 中断恢复。HTTP 测试服务直接返回 SSE，不依赖关闭流式的参数。

### Day 12 流式 Markdown 渲染（2026-10-12 同步）

- **渲染策略**：每次模型调用独立累计文本，工作协程渲染首帧后按 50 毫秒间隔检查变化；同宽度下复用渲染器，相同帧不重复写入。Markdown 标记跨分片或尚未闭合时渲染当前内容，后续分片到来后重新排版。
- **视口与尺寸**：预览保留末尾可见行，自动跟随新内容；没有新分片时也检测窗口尺寸变化并重排。按显示宽度折行，逐行清理旧内容及剩余行，并通过同步更新序列减少重排闪烁。
- **回退**：渲染器创建或渲染失败时展示原文预览；源文本中的终端控制序列不参与预览的光标和屏幕管理。
- **清理边界（2026-10-13 调整）**：CLI 附加会话屏幕时，`StreamDone` 只清空当次预览，下一次调用从空文本开始；会话退出才关闭绘制协程并恢复主屏幕。未附加会话屏幕的独立预览仍在关闭时停止并等待协程退出，再恢复光标和主屏幕，重复或并发关闭只恢复一次。
- **验证入口**：`apps/test/cli/stream_preview_test.go` 覆盖中间渲染、合并刷新、失败回退、跨轮清理和并发关闭；`stream_preview_terminal_test.go` 验证 PTY 中的未闭合格式、表格重排、长文本与尺寸变化；`run_terminal_test.go` 验证实际 CLI 的 Ctrl+C 恢复。

### 全屏会话与滚动（2026-10-13）

- **屏幕生命周期**：`cli.Run` 仅在 stdin 与 stdout 都是终端时创建一个 `sessionScreen`，供 `Output` 和输入显示共同使用；整个会话只进入、退出备用屏幕一次。本次运行的展示记录独立于模型消息历史，不自动重放恢复 Session 的旧显示内容。
- **布局与渲染**：上方保存用户提交、回答、工具状态、审批、统计及运行错误，底部固定输入区。输入区上限取六行与窗口高度三分之一的较小值，至少保留一行；多行草稿视口跟随光标。按宽度缓存历史条目的渲染结果，窗口尺寸轮询及 Unix `SIGWINCH` 触发重排；流式更新沿用 50 毫秒合并间隔。
- **滚动与编辑**：上下键编辑当前草稿，`PageUp` / `PageDown` 按会话可见高度翻页；滚轮或触控板每次移动三行。滚动在等待任务输入或工具审批时可用，鼠标点击、释放、移动和水平滚轮事件只消费，不混入草稿或审批，也不触发提交。
- **鼠标协议**：仅在附加会话屏幕且进入 raw 输入模式时启用 `1000` / `1006` 鼠标报告，离开 raw 模式前关闭。按键读取器识别 SGR 与 X10，X10 坐标按原始字节读取，控制序列限制为 64 字节，避免把鼠标报告当作用户文本。
- **模式恢复**：模型和工具执行期间退出 raw 输入，关闭鼠标及扩展键盘报告；Unix 实现关闭终端回显并保留信号处理，底部显示被动输入区。`/exit`、`/quit`、EOF 和 Ctrl+C 退出时关闭界面并恢复原始终端状态。
- **后续边界**：回答生成及工具执行期间的滚动尚未实现。后续需要统一持续读取终端事件，协调输入编辑与运行状态，并明确新输出到来时的视口跟随规则；不能同时启动两个 stdin 读取者。当前新输出会回到末尾。
- **验证**：`apps/test/cli/screen_terminal_test.go` 覆盖长输入、流式输出、窗口变化、滚轮、审批与错误；`run_terminal_test.go` 验证 Ctrl+C 及模式恢复。全屏改造后的全量 race 测试已通过，滚轮修复后的 CLI race 回归用时 63.859 秒，`go vet` 和编译通过。

### Day 13 上下文预算与 LLM 摘要（2026-10-13 同步）

- **固定预算**：`ContextWindow = 256 * 1024`、`ReservedTokens = 16 * 1024`、`KeepRecentTokens = 20000`；均为代码常量。剩余空间严格小于余量时触发，窗口大小尚未按实际模型自动识别。
- **检查时机**：新输入已追加到历史后、每次主对话请求前检查，计入工具声明。工具批次全部完成后再检查下一次请求。最近一次主对话 usage 用于校准当前占用，不累加不同请求的用量作为上下文大小。
- **职责**：`compaction.Compact(ctx, provider, history, previous)` 接收不含系统 Prompt 的完整历史与旧检查点，集中处理校验、保留边界、完整序列化、摘要模板与分块，返回候选 `State`。`Session.compact` 累计统计并提交成功结果；手动 `/compact`、自动触发和超限恢复共用该调用。
- **近期保留**：`recentCut` 从后向前选择接近 20k 的完整消息边界，assistant 工具调用与全部结果作为一个整体。始终至少保留最新的一个整体，其本身过大时允许超过近期预算；系统 Prompt 原样保留。
- **摘要请求**：复用当前 Provider 的非流式 `Chat`，不提供工具、不通过主对话的 `s.chat` 校准预算或展示流式预览。首次使用 pi 的摘要 Prompt，有旧摘要时使用更新模板；源码注明 pi 提交和 MIT 许可。
- **完整输入**：序列化用户文本、assistant 文本、工具名称、完整参数、调用 ID 和结果，不额外按字符数截断。工具执行阶段既有的输出大小限制不属于摘要截断。
- **分块**：按摘要指令、旧摘要和固定余量计算输入预算，超预算时在 UTF-8 边界拆分文本，后续块继续更新前一块摘要。分块可跨序列化消息或参数文本，不省略内容；服务端上下文超限时拆小当前块继续，普通错误直接返回。
- **提交与恢复**：仅接受非空、无工具、结束完整且未取消的摘要；`length` 等不完整回复不提交。候选检查点及其消息序列校验通过后更新 `s.compaction`，旧预算校准清零；`s.messages` 始终保留完整原始历史，失败时保留旧检查点。
- **存档边界**：Snapshot v2 保存完整原始 `messages` 与独立 `compaction`，包括 `Summary` 和 `FirstKeptMessageIndex`。手动成功后立即保存，自动压缩和超限恢复在本轮结束时保存。恢复时重新生成系统 Prompt，并按检查点构造摘要与近期原文；v1 的旧摘要迁移入检查点，现存原文继续保留。
- **统计与重试**：摘要模型调用与已返回的 usage 计入任务统计，失败也计入已产生的调用；摘要用量不能校准主对话。主对话超限后只重试当前请求一次，保留已完成工具结果及临时指令，不重放工具。
- **阶段边界**：Day 13、Day 14 已完成。修改文件、测试状态等信息仍由 LLM 归纳，没有独立字段；Day 14 已接入严格发送前校验，并通过固定场景的连续压缩语义验收，具体范围见下节。
- **验证入口**：`apps/test/compaction/trigger_test.go`、`compact_test.go`，以及 `apps/test/agent/compact_test.go`、`apps/test/cli/compact_test.go`；全量 `go -C apps test ./...` 已通过。

### Day 14 消息合法性与语义验收（2026-10-13）

- **统一校验**：`provider.ValidateMessages` 校验角色字段、调用 ID、类型、工具名称、完整 JSON 对象参数，以及 assistant 调用与 tool 结果配对。ID 在同批次内唯一，结果顺序不限；一个批次完整结束后才能追加其他角色消息。连续 user 消息和批次之外的 system 指令合法。
- **请求入口**：`Session.contextMessages() ([]provider.Message, error)` 在切片前校验检查点，再校验模型上下文；`s.chat` 在选择 Provider 能力前校验。`prepareRequest` 同时覆盖 `Chat` 和 `ChatStream`。非法消息在本地返回错误，包含消息索引及相关调用 ID。
- **压缩与存档**：压缩前验证完整原始历史，候选检查点和其模型上下文通过校验后才提交；Session 保存和恢复复用公共协议校验，单独保留存档禁止 system 消息的规则。
- **流程回归**：`semantic_test.go` 连续三次实际压缩并保存恢复，覆盖测试失败、修复后通过、再次修改尚未复测。Fake Provider 验证事实输入、旧摘要传递和最终检查点用于 Agent 续接；路径、命令和当前错误做准确原文检查。
- **语义验收**：`semantic_live_test.go` 使用真实模型生成摘要，并用同一配置模型的独立请求逐项验收目标、约束和最新进度。肯定结论必须引用摘要原文；加入遗漏目标、约束反转和过时状态的负例。本地测试拒绝缺项、重复 ID、无依据证据、无效 JSON 及截断回复。
- **启用方式**：普通 `go test` 默认跳过真实模型验收，显式设置 `MINICODE_TEST_LLM=1` 后，测试先切换到仓库根目录，按首选与旧路径规则加载配置；该环境变量只用于测试。命令见 `docs/spec.md` 的连续压缩语义验收小节。
- **验证状态**：本地回归、全量 race 测试与 `go vet` 已通过；2026-10-13 使用配置模型 `gpt-5.6-sol` 完成三轮连续压缩，分别通过 8、7、8 项事实验收，三个语义负例均被正确拒绝，验收耗时 54.46 秒，Day 14 已完成。请求未显式设置思考强度。固定场景通过不等于任意摘要都能完整保留事实，真实任务仍应按需重读文件。
- **源码与测试**：`apps/internal/provider/messages.go`、`apps/internal/agent/compact.go`、`stream.go`、`apps/internal/compaction/compact.go`，以及 `apps/test/provider/messages_test.go`、`apps/test/compaction/semantic_test.go`、`semantic_live_test.go` 和现有 Agent、Session 回归。

### Day 15 项目长期记忆（2026-10-13）

- **文件来源**：读取工作区根目录的 `MiniCode.md`，不扫描父目录、子目录或全局文件。本项目入口为 `apps/MiniCode.md`，运行工作区为 `apps/`；记忆加载器保持通用，不硬编码 apps。仅文件名确实不存在时按无记忆处理，不自动创建；已有空文件不注入。不使用 `.minicode/memory.json`、条目 ID 或新存储框架。
- **内容校验**：`memory.Load` 负责工作区、普通文件和有界读取，`memory.NewDocument` 共用 UTF-8、NUL、64 KiB 大小及 4,096 token 原文预算检查。超限或格式错误返回失败，不截断；`internal/token.EstimateText` 提供共享粗估，消息包装另计入整体预算。
- **加载时机**：Session 新建与恢复时、每个用户轮次追加输入前、手动 `/compact` 前重新读取。重新加载先清除旧缓存，已有文件不可读或非法时返回错误，不能继续使用过时记忆。
- **上下文边界**：整文件作为带 `<project-memory>` 标记的独立 user 参考消息，位于系统 Prompt 后、摘要及近期原文前；说明系统规则和本次明确要求的优先级。缓存独立于原始历史与检查点，不新增 Session v2 字段，不将自动注入块送入摘要，每次请求最多构造一份。
- **维护授权**：系统 Prompt 要求仅在用户明确请求时写入、更新或删除，只保存稳定知识，先读取完整文件、保留无关内容、避免重复和冲突，删除后未经重新授权不从历史写回。临时进度归 Session，不保存敏感信息；上述语义约定由模型遵循，不宣称已有宿主意图识别或逐项语义校验。
- **按需索引**：根目录常驻关键约定、常用命令和名称/适用场景/路径组成的 Markdown 目录；正文保存在 `.minicode/memory/*.md`。模型依据任务用 `read` 读取相关详情，宿主不解析目录、扫描整库或自动检索。已读正文是普通工具结果，参与历史、存档和压缩；恢复后需要精确信息时重新读取当前文件。
- **最小工具改动**：复用 `read` / `write` / `edit` 和审批。`memory.ValidateFile` 对根目录委托 `NewDocument` 检查完整候选与常驻预算，对详情检查 UTF-8、NUL 和 64 KiB 上限，不套用常驻 4,096 token 限制。`read` 仅对显式选择的详情增加检查；相邻目录和非 Markdown 文件沿用普通规则。写入继续原子替换，`edit` 保留先读或先写、唯一匹配和外部变更检查；`write` 保留直接完整覆盖语义，先读再写是 Prompt 要求。
- **目录维护**：提示模型先成功保存详情再更新根目录项，关键约束保留常驻；忘记专题先清空正文再移除目录项。各文件分别原子写入，不提供多文件事务；选择、同步、去重和失败报告由模型遵循提示执行，不宣称宿主已实现确定性语义判断。
- **写后刷新**：成功的根目录 `write` / `edit` 标记需要刷新，整批工具结果完整配对后重新读取，再发送下一次请求。拒绝或失败不触发刷新；刷新失败保留已完成的工具历史并返回错误。没有为 shell 修改、符号链接别名或其他间接路径新增检测，下个用户轮次仍会重新加载。
- **删除范围**：删除指定内容使用 `edit` 空替换；清空全部记忆先清空目录列出的详情，再清空根目录文件，留下空文件。原始对话、工具结果及旧摘要可以继续包含历史内容，不把清空记忆解释为清除会话存档。
- **工作区与本地数据**：四个专题已放入 `apps/.minicode/memory/`，根目录与 apps 下的 `.minicode/` 均被 Git 忽略。记忆迁移不搬迁旧 Session，也不自动切换工作区；从 apps 启动只读该目录内记忆与存档。
- **范围**：按用户约定不提供 `/memory`、`/forget`，记忆查询和维护统一走普通对话与现有工具。没有自动扫描、关键词或向量索引；Day 15 核心流程已接入，仍待真实模型验收。
- **验证**：`apps/test/memory/load_test.go`、`files_test.go`、`apps/test/agent/memory_test.go`、`memory_mutation_test.go`、`memory_detail_test.go`、`apps/test/tools/memory_test.go` 和 `apps/test/cli/config_test.go` 覆盖常驻/详情校验、按需读取、维护、压缩恢复、apps 启动及配置优先级与错误不回退；全量 race 测试、`go vet` 和编译通过。Fake Provider 验证宿主流程，真实模型的专题选择、去重、冲突更新和删除仍待验证。

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
