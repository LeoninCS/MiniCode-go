# MiniCode-go

## 项目简介与定位

MiniCode-go 是一个使用 Go 从零实现的、以 CLI 为主要交互入口的轻量级 Coding Agent，可以理解为一个面向原理学习的“迷你版 Pi”。

它运行在本地工作区中，用户通过命令行与 Agent 进行多轮交互。Agent 通过大语言模型理解任务，并使用受控工具完成代码浏览、文件修改、命令执行和测试验证：

```text
理解任务 → 检查代码 → 调用工具 → 修改代码 → 运行验证 → 根据结果继续修复
```

项目定位为一个**简单但完整的教学型终端 Agent**：尽量减少框架依赖和非必要抽象，但保留一个 Coding Agent 的核心能力，包括模型调用、工具调用、Agent Loop、CLI 会话、Session 持久化、上下文压缩、执行前确认、流式输出和结果验证。

首版从单 Agent、单任务、单本地工作区开始，不提供 Web UI、IDE 插件、多 Agent 或长期记忆，重点是通过逐步实现一个可运行的最小闭环，理解模型、Agent、工具和宿主程序之间的协作方式。

## 🚀 快速使用（从源码构建）

> MiniCode 在你启动命令时所在的目录中工作。请在希望它读取和修改的项目目录里启动它。

### 1. 获取源码并构建

需要先安装 [Go](https://go.dev/dl/)（版本要求见 [`apps/go.mod`](apps/go.mod)）。

```bash
git clone https://github.com/LeoninCS/MiniCode-go.git
cd MiniCode-go
go -C apps build -o ../bin/minicode ./cmd/minicode
```

Windows PowerShell 使用：

```powershell
git clone https://github.com/LeoninCS/MiniCode-go.git
cd MiniCode-go
go -C apps build -o ../bin/minicode.exe ./cmd/minicode
```

### 2. 安装到 PATH（之后可直接调用 `minicode`）

构建完成后，可以把 MiniCode 安装到系统的 `PATH`。这一步只需执行一次，之后便能在任意项目目录中直接运行 `minicode`，无需一直输入可执行文件的完整路径。

macOS / Linux：

```bash
sudo install -m 755 bin/minicode /usr/local/bin/minicode
```

确认安装成功：

```bash
minicode -h
```

Windows 用户可以：

1. 创建目录 `C:\Tools\MiniCode`；
2. 将 `bin\minicode.exe` 复制到该目录；
3. 在 Windows 的“环境变量”设置中，把 `C:\Tools\MiniCode` 添加到用户 `Path`；
4. 重新打开 PowerShell，然后验证：

```powershell
minicode -h
```

> 你使用的是本项目作者实现的 MiniCode；MiniCode 负责调用模型和操作本地工作区。每位用户需要配置自己的模型服务和 API Key，密钥及模型调用费用由用户自己管理。

### 3. 配置模型服务

MiniCode 需要一个兼容 OpenAI `/chat/completions` 接口的模型服务。配置只从**启动目录下的固定路径** `apps/config/config.toml` 读取，不再读取 `.env`、`MINICODE_*` / `LANGFUSE_*` 环境变量，也不支持通过 CLI 参数覆盖模型配置。

在本仓库根目录运行时，可复制模板：

```bash
cp apps/config/config.example.toml apps/config/config.toml
$EDITOR apps/config/config.toml
```

使用 DeepSeek 的最小配置如下：

```toml
[model]
provider = "openai" # 可选，省略时使用 OpenAI 兼容协议
api_key = "你的_API_Key"
base_url = "https://api.deepseek.com/v1"
name = "deepseek-chat"
```

`model.api_key`、`model.base_url` 和 `model.name` 均为必填项。`config.toml` 包含密钥，已被 `.gitignore` 忽略，请勿提交到 Git。

`model.provider` 选择调用协议，当前仅支持 `"openai"`，省略或留空时也使用该协议。这里的 `openai` 表示 OpenAI 兼容的 Chat Completions 接口；使用其他兼容服务时，填写对应的地址、密钥和模型名即可。Anthropic / Gemini 专用协议尚未实现，填写其他 `provider` 值会报错。

模型调用自动使用 SSE 流式响应，无需额外配置，也不提供流式开关；模型服务需要支持 OpenAI 兼容的流式 Chat Completions。默认请求不设置 `max_tokens`，模型输出长度使用服务端默认值。

> MiniCode 的工作区就是启动命令时的目录。因此在其他项目中运行时，需要在该项目中创建 `apps/config/config.toml`；配置文件路径目前不可自定义。

可选地接入 Langfuse：

```toml
[langfuse]
public_key = "pk-lf-..."
secret_key = "sk-lf-..."
host = "https://cloud.langfuse.com"
```

`public_key` 和 `secret_key` 同时留空或省略整个 `[langfuse]` 表时关闭追踪；启用时必须同时填写两项。`host` 留空时使用 Langfuse Cloud，自部署用户填写实例根地址。

启用后，MiniCode 会通过 OTLP/HTTP 上传完整的用户输入、模型请求与响应、工具参数与结果、token 用量、耗时和错误。数据可能包含源代码、命令输出和其他敏感信息，请仅连接可信的 Langfuse 项目。配置字段和使用示例见本节。

### 4. 在自己的项目中启动

安装到 `PATH` 后，进入希望 MiniCode 操作的项目目录并直接执行 `minicode`。

macOS / Linux：

```bash
cd /path/to/your-project
minicode
```

Windows PowerShell：

```powershell
cd C:\path\to\your-project
minicode
```

如果跳过了上面的 PATH 安装步骤，也可以使用可执行文件的完整路径启动。

启动后直接输入任务，例如：

```text
> 分析这个项目并告诉我如何运行测试
> 修复测试失败的问题
> /exit
```

交互终端支持多行编辑、自动折行，以及内容超出窗口时随光标滚动：

| 操作 | 行为 |
| --- | --- |
| `Enter` | 提交整段输入 |
| `Shift+Enter` / `Ctrl+J` | 在光标处插入真实换行 |
| `↑` / `↓` | 在当前输入的显示行之间移动光标，到首末行停止，不翻历史记录 |
| `←` / `→`、`Home` / `End` | 左右移动，或移到当前逻辑行的开头 / 末尾 |
| `Backspace` / `Delete` | 删除字符；删除换行符时合并相邻行 |
| `Ctrl+C` | 退出整个会话；运行中同时取消模型请求或工具执行 |

输入 `/exit`、`/quit` 或发送 EOF 也可退出。部分传统终端无法区分 `Shift+Enter` 与 `Enter`，此时请使用 `Ctrl+J`。通过管道输入时仍按行提交。当前版本不会在启动时询问模型配置，如果缺少配置会给出提示并退出。

## 开发进度

- ✅ Day 0：项目定位、功能范围和开发计划；
- ✅ Day 1：协议结构体 + 单次非流式模型调用；
- ✅ Day 2：工具 Schema 定义 + 工具调用响应解析；
- ✅ Day 3：Agent Loop；
- ✅ Day 4：`bash` / `read` / `write` / `edit` 四个工具与工具注册表；
- ✅ Day 5：系统 Prompt + 默认 CLI 输入循环 + 会话内跨轮记忆；
- ✅ Day 6：已完成工具输出截断、500 轮上限、单轮超时和副作用工具执行前审批；不提供危险命令黑名单或硬拦截；
- ✅ Day 7：已完成 Ctrl+C / SIGTERM 中断、context 取消链路，以及中断、超时和普通失败的提示区分；
- ✅ Day 8：已完成 token 统计和过程可视化；
- ✅ Day 9：新会话默认按 UUID 自动保存，并支持通过 `--session <id>` 跨进程恢复；
- ✅ Day 10：Provider 接口、配置工厂、统一响应与错误，以及 Fake Provider 测试；
- ✅ Day 11：SSE 请求与事件解析、终端增量预览、用量事件、错误与中断处理；
- ✅ Day 12：tool_calls 参数分片累积与校验、终端流式 Markdown 渲染、合并刷新、长文本视口与窗口尺寸变化处理；
- ⬜ Day 13：上下文压缩触发与摘要；
- ⬜ Day 14：压缩切分点合法性处理。

## 最近更新

### 2026-10-12

- 补齐 Day 12 实现记录：模型生成过程中在备用屏幕渲染 Markdown，后续文本按 50 毫秒间隔合并刷新；
- 支持跨分片和未闭合的 Markdown 格式，长回复预览跟随末尾，窗口尺寸变化时自动重排；
- 复用 Markdown 主题与渲染器，渲染失败时回退到原文预览；结束、错误、超时和取消时停止刷新，恢复光标和主屏幕；
- 补充实时渲染、合并刷新、跨轮清理、失败回退、并发关闭及 PTY 显示与尺寸变化测试；全量测试、CLI race 检查、`go vet` 和编译已通过。

### 2026-10-11

- 补齐 Day 11 实现记录：普通启动自动使用 SSE 流式请求，无需配置开关；
- 终端在备用屏幕逐段预览模型文本，流结束后恢复主屏幕并渲染完整 Markdown；管道和文件输出保留完整回复，不写入增量预览；
- 按工具索引累积名称与参数，完整结束并校验通过后才执行工具；提前断线不写入半截 assistant 消息，也不执行该次回复中的工具；
- 读取到 `[DONE]` 后结束，保留文本结束后的独立 `usage` 事件；流式调用继续接入任务统计和 Langfuse 追踪；
- 补充 SSE 分隔、长事件、工具分片、错误、断线、超时、取消、会话保存和 PTY 终端恢复测试。

### 2026-10-10

- 完成 Day 10 Provider 抽象：Agent 通过 `Provider.Chat` 调用模型，CLI 通过 `provider.New` 按配置创建实现；
- 当前实际适配器支持 OpenAI 兼容 Chat Completions 协议，可复用它调用兼容服务；其他专用协议仍需新增适配器；
- 定义可选的 `StreamingProvider`、`Stream` 和增量事件，为 Day 11 的 SSE 实现提供接口；
- 使用 Fake Provider 验证工具调用、历史、错误恢复、用量统计及轮数上限后的总结，无需真实模型服务；
- 移除模型输出 Token 上限配置和默认值，普通请求与最终总结均使用模型服务的默认输出长度。

### 2026-10-09

- 完成 Day 9 Session 持久化：启动时自动创建 UUID，会话默认保存到 `.minicode/sessions/<id>.json`，并可通过 `--session <id>` 恢复；
- 保存用户消息、模型回复、工具调用、工具结果、工作区和上一轮状态，系统 Prompt 在恢复时重新生成；
- 使用 `0600` 临时文件和原子重命名写入，严格拒绝非法 ID、损坏文件、不兼容版本、工作区不匹配及不完整工具消息；
- CLI 启动后输出 `[会话] <id>`；该 ID 只在创建它的工作区中有效。

### 2026-10-08

- 完成 Day 8 可观测性增强：逐任务汇总模型调用、工具调用、输入/输出/总 token 与累计耗时；
- 交互模式在标准输出展示统计，管道模式改写标准错误，避免污染最终回答；模型未返回 `usage` 时明确显示不可用，部分响应缺失时标记为 `partial`；
- 配置入口统一为启动目录下的 `apps/config/config.toml`，移除旧 `.env`、配置环境变量和模型配置 flag；
- 新增可选的 Langfuse OTLP/HTTP 链路追踪，覆盖用户轮次、模型调用、工具调用、token 用量、耗时和错误；
- 增加 TOML 模型配置和 Langfuse 追踪说明。

### 2026-10-06

- 完成任务状态提示区分：用户取消显示「任务已中断」，达到时限显示「任务执行超时」，普通失败保留原始错误；
- 输入阶段的 Ctrl+C 统一转换为 context 取消，等待模型、等待审批和编辑输入时均能输出一次明确的中断提示并以退出码 `1` 结束；
- 调整开发顺序：原 Day 8（Ctrl+C 中断与 context 取消）前移为 Day 7，原 Day 7（token 统计和过程可视化）后移为 Day 8。

### 2026-10-02

- CLI 参数、输入、终端展示和 Markdown 渲染集中到 `apps/internal/cli/`，`cmd/minicode/main.go` 只保留启动入口；
- 支持真实多行输入、自动折行、上下光标移动和滚动显示；上下键不再浏览历史记录；
- 修复扩展键盘协议下 Ctrl+C 无效的问题，键盘协议只在编辑输入时启用；取消运行中的任务后直接退出，避免再次打开输入框；
- 运行过程使用终端备用屏幕，最终回答、失败或取消时恢复主屏幕，避免中间输出污染对话记录；
- 补充输入编辑、终端显示和 Ctrl+C 退出回归测试。

### 2026-10-01

- 按当时代码状态核对并修正开发进度：原 Day 8 的取消链路已完成，尚缺「区分用户取消与任务失败」和「输出任务中断状态」两项（这两项已于 2026-10-06 完成，阶段现调整为 Day 7）；
- 修正功能完成情况里「CLI 交互」未完成的旧记录，并补上资源控制、token 统计、执行前确认三项的真实状态。

### 2026-09-30

- 默认进入交互循环，可持续从标准输入接收任务；
- 同一进程内复用 Session，系统 Prompt、用户消息、模型回复和工具结果会跨轮保留；
- 增加 MiniCode 系统 Prompt，明确工作区边界、工具契约、执行策略和回复规范；
- 支持 `/exit`、`/quit` 或输入 EOF 结束会话，空行不会发送给模型；
- CLI 单轮任务和默认 HTTP 客户端的超时时间由 60 秒延长为 1 小时。

### 2026-09-29

- Agent Loop 最大模型轮数调整为 500，达到上限后停止工具调用并请求最终总结；
- 接入 `read`、`write`、`edit` 文件工具，并将四个内置工具统一迁移到工具注册表；
- `edit` 支持 `replace_all`，文件编辑继续执行“先读后改”和外部改动检测。

## 项目文档

- [`docs/spec.md`](docs/spec.md)：项目定位、功能规格、开发计划和验收标准；
- [`docs/plan.md`](docs/plan.md)：按天拆分的开发任务和完成状态；
- [`docs/agent.md`](docs/agent.md)：Agent 操作规范（硬边界、已固化决策、已知陷阱），用于防止多 Day 实施中的细节漂移；
- [`docs/tutorial.md`](docs/tutorial.md)：配置、启动、会话恢复、审批和超时的使用教程。

## 目录结构

```text
MiniCode-go/
├── README.md
├── docs/
│   ├── spec.md
│   ├── plan.md
│   ├── agent.md                       # Agent 操作规范
│   └── tutorial.md                    # 使用教程
└── apps/                              # Go module: github.com/MiniCode-go/minicode
    ├── go.mod
    ├── cmd/minicode/main.go           # 启动入口
    ├── internal/cli/                  # CLI 参数、输入与展示
    │   ├── run.go                    # 配置、信号和会话组装
    │   ├── input.go                  # 多行编辑、按键与终端模式
    │   ├── input_display.go          # 折行、光标定位与滚动显示
    │   ├── approval.go               # 副作用工具审批与自动批准
    │   ├── output.go                 # 流式预览、运行状态与最终回答展示
    │   ├── stream_preview.go         # Markdown 实时预览、合并刷新与视口
    │   └── markdown.go               # 终端检测与 Markdown 渲染
    ├── internal/agent/                 # Agent Loop、模型调用与工具执行
    │   ├── run.go                      # Session、交互循环与持久化接入
    │   ├── prompt.go                   # 系统 Prompt
    │   ├── approval.go                 # 工具审批接口
    │   ├── stream.go                   # 自动流式调用与完整响应聚合
    │   └── output.go                   # 最终输出、统计与流式预览接口
    ├── internal/config/                # TOML 配置读取与校验
    │   └── config.go
    ├── internal/session/               # Session 快照格式、校验和文件存储
    │   └── store.go                    # JSON 读取与原子写入
    ├── internal/tools/                 # 工具实现 + 注册机制
    │   ├── registry.go                # Tool、ToolRegistry、参数公共校验
    │   ├── bash.go                    # bash 执行、输出截断与取消
    │   ├── read.go                    # read 工具
    │   ├── write.go                   # write 工具
    │   ├── edit.go                    # edit 工具
    │   └── filesystem.go              # 工作区句柄、原子写入与版本记录
    ├── internal/provider/              # Provider 抽象 + OpenAI 兼容客户端(纯源码)
    │   ├── provider.go                 # 接口、流式契约与配置工厂
    │   ├── types.go                    # 公共消息、响应、工具调用与错误
    │   ├── openai.go                   # 非流式 HTTP 调用与共用请求校验
    │   └── openai_stream.go            # SSE 请求、事件解析、取消与关闭
    ├── internal/telemetry/             # OpenTelemetry / Langfuse 追踪配置
    │   └── langfuse.go
    └── test/                           # 测试文件单独目录(black-box)
        ├── cli/                       # CLI、输入、终端显示与退出测试
        │   ├── stream_preview_test.go  # 实时渲染、合并刷新、回退与关闭
        │   └── stream_preview_terminal_test.go # PTY 中的排版、视口与尺寸变化
        ├── agent/                     # Agent、Fake Provider 与持久化测试
        │   ├── agent_test.go
        │   ├── run_test.go
        │   ├── session_store_test.go
        │   └── stream_test.go          # 自动流式调用、聚合与失败回滚
        ├── tools/                      # 文件工具、bash 与注册表测试
        │   ├── bash_test.go
        │   ├── read_test.go
        │   ├── write_test.go
        │   ├── edit_test.go
        │   ├── filesystem_test.go
        │   └── registry_test.go
        └── provider/
            ├── provider_test.go        # 配置工厂与流式能力边界
            ├── openai_test.go
            ├── openai_stream_test.go   # SSE 协议、错误、取消与追踪
            └── tool_calls_test.go
```

约定:
- Go module 根在 `apps/`。
- 测试文件一律放在 `apps/test/<src-path>/`,与源码同名目录;用 `package <name>_test`
  风格,只测导出 API,源码目录保持干净。
- 构建/测试命令:`go -C apps build ./...` / `go -C apps test -count=1 ./...`。

## 构建与运行

```bash
# 构建
go -C apps build -o ../bin/minicode ./cmd/minicode

# 准备配置：固定从启动目录的 apps/config/config.toml 读取
cp apps/config/config.example.toml apps/config/config.toml
$EDITOR apps/config/config.toml
```

```bash
# 启动交互模式：Enter 提交整段输入，对话上下文在当前会话内持续保留
./bin/minicode

# 也可通过 stdin 批量输入；EOF 后退出
printf '分析这个项目\n运行测试\n' | ./bin/minicode

# /exit 和 /quit 均可结束交互会话
```

模型配置不能通过环境变量或 CLI flag 覆盖；`apps/config/config.toml` 含有密钥且已被 Git 忽略。可选的 `[langfuse]` 配置及其敏感数据范围见上文“配置模型服务”。

每个 Session 启动时会注入系统 Prompt，其中包含当前工作区、文件路径边界、工具说明、先读后改、修改后验证等规则。新会话无需额外参数：MiniCode 会生成标准小写 UUID，立即在当前工作区创建 `.minicode/sessions/<id>.json`，并在每轮结束后自动更新。启动时会输出会话 ID，例如：

```text
[会话] 550e8400-e29b-41d4-a716-446655440000
```

记下该 ID 后，可在**创建它的同一工作区**跨进程继续会话：

```bash
minicode --session 550e8400-e29b-41d4-a716-446655440000
```

恢复后与正常启动一样进入输入循环，可以直接输入下一项任务，无需在命令后附加提示词。

`--session` 接收会话 ID，不接收文件路径；ID 必须是标准小写 UUID，且对应存档必须已经存在。恢复时会重新生成系统 Prompt，并恢复已完成的协议消息；不会恢复运行中的进程、审批状态或文件工具的读取缓存。Session 文件可能包含源码、命令输出和其他敏感信息，请勿提交到 Git；`.minicode/` 已被本仓库忽略。

终端中的完整模型回复使用 [Glamour](https://github.com/charmbracelet/glamour) 渲染 Markdown，支持标题、加粗、列表和代码高亮，并按终端宽度换行。默认使用 `dracula` 主题，可通过 `GLAMOUR_STYLE` 覆盖，例如浅色终端可设置 `GLAMOUR_STYLE=light`。输出到管道或文件时保留 Markdown 原文；渲染失败时也会回退到原文。工具审批信息按原文展示，工具结果回传模型并保存在会话历史中。

流式接收和实时 Markdown 渲染自动启用。当标准输入和标准输出都连接终端时，CLI 累积模型文本，在备用屏幕渲染标题、列表、加粗和代码块，并按 50 毫秒间隔合并后续更新。尚未闭合的 Markdown 格式会随新文本重新排版；渲染失败时回退到原文预览。

长回复预览只显示末尾可见行，自动跟随最新内容；窗口尺寸变化时会重新排版。每次模型流结束、报错、超时或取消时，CLI 停止刷新并恢复光标和主屏幕。完整最终回答随后在主屏幕渲染 Markdown，工具状态和审批信息也在主屏幕展示。

工具调用的名称和 JSON 参数按索引累积，只有整条响应正常结束且所有调用校验通过后，才进入审批和执行。提前断线会报告任务失败，该次未完成的 assistant 回复和工具调用不会写入历史；此前已完成的步骤仍保留。

每项任务结束后会显示一行累计统计，例如：

```text
[统计] 2.3 秒 ｜ 模型 2 次 ｜ 工具 1 次 ｜ Token 3,740（输入 3,200 / 输出 540）
```

统计包括任务耗时、模型调用次数、工具调用次数，以及模型响应中 `usage` 的输入、输出和总 token 之和。流式请求会请求用量信息，并接收文本结束后单独返回的 `usage` 事件。模型完全不返回 `usage` 时显示 `Token 不可用`；只有部分模型响应携带 `usage` 时在统计末尾显示 `（部分统计）`。若服务返回的 `total_tokens` 为 `0`，但输入或输出 token 非零，MiniCode 会用两者之和补全本次总数。

非交互模式不展示流式预览和过程状态，完整回答写入标准输出，会话标识、任务统计和错误写入标准错误。执行审批的说明和参数仍按下文规则展示。可以将回答保存到文件：

```bash
minicode "分析这个项目" >answer.md 2>run.log
```

CLI 支持普通文本回复和 `bash` / `read` / `write` / `edit` 四个工具。`bash`、`write`、`edit` 执行前展示调用信息并默认等待用户批准，`read` 可直接执行。批准后执行工具并把结果回传模型；拒绝时不执行工具，并把拒绝结果回传模型。使用 `--yes` 可以自动批准所有副作用工具调用，仍会展示审批说明和参数，例如：

```text
tool: bash
arguments: {"command":"go test ./..."}
```

命令通过 `bash -c` 在启动 CLI 时的工作目录执行，标准输出和错误输出合并为工具结果回传模型。每次调用使用独立的非交互 shell，`cd` 不会影响下一次调用；当前工作目录不是文件系统沙箱。项目不检查命令内容，也不提供危险命令黑名单或硬拦截；命令安全边界是默认开启的逐次用户审批。`--yes` 会跳过审批，因此只应在用户信任模型生成操作的场景使用。

每项任务最多进行 500 轮可使用工具的模型请求。达到上限后，再请求一次不带工具的最终总结；若总结失败或为空，则输出停止说明。达到上限仍返回非零退出码。普通模型请求和最终总结均使用服务端默认的输出长度。`-timeout` 默认 1 小时，覆盖模型请求、命令执行和最终总结。Ctrl+C 或超时会终止当前命令进程组；单次工具输出最多保留 64 KiB，并标记截断。命令失败的输出和错误也会回传模型。

```bash
# 从项目根目录启动（已准备 apps/config/config.toml），然后在交互界面中输入任务
go -C apps run ./cmd/minicode

# 只运行测试目录，避免显示源码包的 [no test files]
go -C apps test -count=1 ./test/...

# 检查实时预览、并发关闭及终端 Ctrl+C 恢复
go -C apps test -race ./test/cli -run 'TestOutput|TestMiniCode_TerminalCtrlC' -count=1
```

执行前审批、会话持久化、SSE 流式输出和实时 Markdown 渲染已经接入；下一阶段为 Day 13～14 的上下文压缩。工具实现与注册机制见 `apps/internal/tools/`。
