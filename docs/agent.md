# Agent 操作规范

避免多 Day 实施中的细节漂移(目录乱跑、协议换栈、风格走样)。
与 `spec.md`(做什么)/ `plan.md`(按天做什么)的关系:本文件写"不要做错什么 + 已定下的硬决策"。

## 1. 硬边界

- 工作区:只写到 `YOUR WORKSPACE DIRECTORY`,不碰 Desktop / Downloads / /tmp 根
- git 写操作:不替用户执行 commit / push / reset / `checkout --`,只生成提交信息措辞
- 顶层目录:不擅自新增 / 改名 / 移动 cmd/ internal/ apps/ docs/,发现被外部工具改动先停下报告
- 依赖:不引入第三方 Go module,除非 plan.md 显式允许或与用户对齐
- Provider 协议:Day 1 已选 OpenAI 兼容,Day 10 抽象前不要切到 Anthropic / Gemini 专用
- 退出码:成功 0;运行时错误 1;配置/输入错误 2

## 2. 项目布局

```text
apps/                                # Go module 根(不是仓库根)
├── cmd/minicode/main.go             # CLI 入口
├── internal/cli/                    # CLI 参数、输入、终端展示和 Markdown 渲染
│   ├── run.go                       # 配置、信号与会话组装
│   ├── input.go / input_display.go  # 多行编辑、按键与显示
│   └── output.go / markdown.go      # 备用屏幕、输出与 Markdown
├── internal/agent/                  # 模型循环与消息历史
├── internal/tools/                  # 工具实现 + 注册机制
│   ├── registry.go                  # Tool、ToolRegistry、参数公共校验
│   ├── bash.go                      # RunBash + execute*Tool + build*Tool
│   ├── read.go / write.go / edit.go # 同上,按工具分文件
│   └── filesystem.go                # FileTools、工作区句柄与原子写入
├── internal/provider/               # 模型协议 + OpenAI 兼容客户端(纯源码,无 _test.go)
│   ├── types.go
│   └── openai.go
└── test/                            # 测试单独目录,black-box
    ├── cli/                        # CLI、输入、终端显示与退出
    ├── agent/                      # Agent 输出边界与错误回传
    ├── tools/                      # 工具行为与取消
    └── provider/                   # 模型协议与客户端
```

- 后续 Day:源码 `apps/internal/<name>/`,测试 `apps/test/<name>/`,同名目录
- 测试文件名与被测源文件同名对应;工具体系细节见 `tools.md`
- `internal/` 不依赖 `cmd/`；第三方渲染与终端依赖集中在 `internal/cli/`，`provider` 和 `tools` 继续只依赖标准库
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

- **Provider 协议 = OpenAI 兼容**(`/chat/completions`、Bearer、application/json)。理由:DeepSeek / MiniMax / Moonshot / 智谱 / 硅基流动 / OpenAI 都兼容,Day 10 抽象时切换成本最低
- **API 错误双格式兼容**:OpenAI 嵌套 `{"error": {...}}` 与平铺,非 JSON 退化为带状态码的通用错误
- **环境变量**:`MINICODE_API_KEY` / `MINICODE_BASE_URL` / `MINICODE_MODEL`;flag 优先
- **配置模板**:仓库根 `.env.example`(不进 git,本地 `.env`),列出 env var + 常用服务的 BaseURL/Model 示例值;不引入第三方配置库
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
- **直接执行**：按用户当前要求展示并执行模型生成的命令，实时输出并把执行结果回传模型；本阶段不增加逐次确认交互，后续权限机制仍按 Day 6 推进。
- **执行边界**：最多 500 轮可使用工具的模型请求；达到上限后额外请求一次纯文本总结，不再提供或执行工具。总结沿用整项任务的 timeout，失败时输出停止说明；达到上限仍返回非零退出码。取消时终止命令进程组，单次命令输出最多保留 64 KiB。
- **实现范围**：简单循环和 bash 执行函数，不提前引入工具注册表、交互会话和持久化。
- **职责划分（按当前实现更新）**：`internal/agent` 管理模型循环、消息历史、轮数和工具分发；工具协议适配与注册表位于 `internal/tools`。`internal/cli` 负责参数、输入、任务取消、展示和退出码，`cmd/minicode` 只保留启动入口。
- **展示边界**：Agent 通过 `Output` 接口交付模型文本、工具调用、实时输出、结果和工具错误；不依赖终端渲染，不打印 CLI 前缀。终止任务的错误由 `Run` 返回。

### CLI Markdown 渲染

- **职责划分（2026-10-02 调整）**：终端检测、宽度读取和 Glamour 渲染集中到 `internal/cli/markdown.go`；`cmd/minicode` 只调用 `cli.Run`。
- **输出规则**：终端中的模型回复渲染 Markdown，按终端宽度换行；管道、文件和渲染失败时输出原文。工具输出和回传模型的消息保持原样。
- **主题**：默认使用 `dracula`，通过 `GLAMOUR_STYLE` 覆盖。

### CLI 多行编辑与终端恢复（2026-10-02）

- **输入边界**：Agent 通过 `Input` 接口读取完整任务；终端支持多行编辑，管道继续逐行读取。上下键只移动当前输入光标，不浏览历史；`Shift+Enter` / `Ctrl+J` 换行，`Enter` 提交。
- **显示一致性**：复用 readline 的缓冲区编辑和 raw 模式，由 `inputDisplay` 统一计算真实换行、软折行、显示列和滚动区域；垂直移动使用同一套显示行布局。
- **键盘协议生命周期**：只在编辑输入时启用扩展键盘协议，转换编码后的 Ctrl+C，并在离开 raw 模式时恢复。模型和工具执行期间不得遗留该协议。
- **取消语义**：Ctrl+C 退出整个会话；任务返回后根 context 已取消时，不再启动下一次输入 goroutine，避免退出时重新进入 raw 模式。
- **输出生命周期**：运行过程放在备用屏幕，最终回答前及错误、超时、取消路径均恢复主屏幕；非交互模式保留完整输出。
- **验证边界**：除提交内容外，还需覆盖 PTY 中的实际显示与光标，以及进程退出后的键盘协议和屏幕恢复；实现总结和测试入口见 [cli.md](cli.md)。

## 6. 已知陷阱

1. bash session 不保留 cwd,需 `cd path && cmd` 或 `go -C path cmd`,不要假设 PWD 已被切过
2. Edit/Write 路径相对仓库根,不是 shell cwd
3. httptest handler 阻塞 `r.Context().Done()` 不会在客户端断开时立即返回,必须加 server-side 兜底超时
4. OpenAI 错误嵌套在 `error` 字段下,只用平铺解析会退化成原始 body
5. 用户偏好:微信端纯文本;commit 用 `conventional + 中文 subject`;能查文件/plan/spec 就查,不替用户瞎猜
6. `agent.Session.Run` 按轮启动 `Input.Readline`，任务执行期间不预读下一轮输入；后续执行前确认应协调输入适配器和终端模式，避免并发读取 stdin。管道模式的确认策略尚未定义，不要把它视为已经实现
7. 终端模拟器 vt10x 会把 Kitty 协议开关误解为光标恢复；显示测试需忽略这两个控制序列，协议配对和退出恢复必须另由输入及 CLI 子进程测试检查

任何与本规范冲突的改动,先改本文件,再改实现。
