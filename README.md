# MiniCode-go

## 项目简介与定位

MiniCode-go 是一个使用 Go 从零实现的、以 CLI 为主要交互入口的轻量级 Coding Agent，可以理解为一个面向原理学习的“迷你版 Pi”。

它运行在本地工作区中，用户通过命令行与 Agent 进行多轮交互。Agent 通过大语言模型理解任务，并使用受控工具完成代码浏览、文件修改、命令执行和测试验证：

```text
理解任务 → 检查代码 → 调用工具 → 修改代码 → 运行验证 → 根据结果继续修复
```

项目定位为一个**简单但完整的教学型终端 Agent**：尽量减少框架依赖和非必要抽象，但保留一个 Coding Agent 的核心能力，包括模型调用、工具调用、Agent Loop、CLI 会话、Session 持久化、上下文压缩、项目记忆、执行前确认、流式输出和结果验证。

当前从单 Agent、单任务、单本地工作区开始，不提供 Web UI、IDE 插件或多 Agent，重点是通过逐步实现一个可运行的最小闭环，理解模型、Agent、工具和宿主程序之间的协作方式。项目长期记忆使用工作区根目录的 `MiniCode.md`，保存常驻约定和按需详情目录。本项目以 `apps/` 为完整运行工作区，记忆入口是 [apps/MiniCode.md](apps/MiniCode.md)。

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

MiniCode 需要一个兼容 OpenAI `/chat/completions` 接口的模型服务。配置优先读取**启动目录下的** `config/config.toml`，仅当文件名确实不存在时兼容旧路径 `apps/config/config.toml`。首选文件存在但不可读、为目录、符号链接断开、TOML 错误或缺少必填字段时直接报错，不回退到旧配置。不读取 `.env`、`MINICODE_*` / `LANGFUSE_*` 环境变量，也不支持通过 CLI 参数覆盖模型配置。

本项目从 `apps/` 启动。在仓库根目录准备配置，下面的文件对应运行工作区内的 `config/config.toml`：

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

主对话和轮数上限后的总结自动使用 SSE 流式响应，无需额外配置，也不提供流式开关；模型服务需要支持 OpenAI 兼容的流式 Chat Completions。上下文摘要使用独立的非流式请求。这三类运行时请求固定设置 `max_tokens = 8192`，不提供输出 Token 上限配置。

当前没有思考强度配置项，请求不发送 `reasoning_effort`，由模型服务或网关采用默认设置。以 `gpt-5.6-sol` 为例，官方默认值为 `medium`；使用中转网关时，实际档位以网关设置为准。见 [OpenAI 模型说明](https://developers.openai.com/api/docs/models/gpt-5.6-sol)。

> MiniCode 的工作区就是启动命令时的目录。在其他项目中使用时，创建该项目的 `config/config.toml`，再从该目录启动；旧布局仍可使用 `apps/config/config.toml`，配置文件路径不可自定义。

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

开发本项目时，从仓库根目录进入 `apps/`，让文件工具、记忆和 Session 使用同一个完整工作区：

```bash
cd apps
../bin/minicode
```

启动后直接输入任务，例如：

```text
> 分析这个项目并告诉我如何运行测试
> 修复测试失败的问题
> /exit
```

当输入和输出都连接终端时，MiniCode 使用全屏会话界面，只绘制本次运行的会话内容，输入区固定在底部。支持多行编辑、自动折行，以及输入过长时随光标移动显示：

| 操作 | 行为 |
| --- | --- |
| `Enter` | 提交整段输入 |
| `Shift+Enter` / `Ctrl+J` | 在光标处插入真实换行 |
| `↑` / `↓` | 在当前输入的显示行之间移动光标，到首末行停止，不翻历史记录 |
| `←` / `→`、`Home` / `End` | 左右移动，或移到当前逻辑行的开头 / 末尾 |
| `Backspace` / `Delete` | 删除字符；删除换行符时合并相邻行 |
| 滚轮 / 触控板上下滑动 | 等待输入或审批时滚动上方会话记录，输入区保持固定 |
| `PageUp` / `PageDown` | 等待输入或审批时翻页查看会话记录 |
| `Ctrl+C` | 退出整个会话；运行中同时取消模型请求或工具执行 |

输入 `/exit`、`/quit` 或发送 EOF 也可退出。部分传统终端无法区分 `Shift+Enter` 与 `Enter`，此时请使用 `Ctrl+J`。通过管道输入时仍按行提交。当前版本不会在启动时询问模型配置，如果缺少配置会给出提示并退出。

模型生成回答或工具执行期间，输入区继续显示，但暂不处理滚动事件；生成中滚动尚未实现。退出时恢复原终端屏幕、光标、键盘和鼠标模式。

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
- ✅ Day 13：上下文预算、自动与手动压缩、固定保留近期 20k token、pi 滚动摘要、完整输入和分块处理；
- ✅ Day 14：合法切分、严格发送前校验、连续压缩与恢复测试，以及真实模型逐项语义验收和三个负例均已通过；
- 🔄 Day 15：已接入常驻记忆、Markdown 按需索引、专题读取、文件工具维护与写后刷新，并迁移到 `apps/` 工作区；宿主流程测试通过，真实模型的选择与维护验收待补。专用记忆命令不在实现范围内。

## 最近更新

### 2026-10-13

- 分离完整原始历史与模型上下文：Session v2 保存原始消息和独立压缩检查点，模型请求使用“系统 Prompt → 项目记忆（如有）→ 摘要（如有）→ 近期原文”；支持读取和迁移 v1 存档；
- 接入公共 `provider.ValidateMessages`，在 Agent、流式与非流式 Provider 请求入口、压缩前后及存档保存恢复时校验消息结构、参数 JSON 和工具调用结果配对；非法消息在本地报错并阻止请求；
- 加入三次连续压缩、保存恢复与 Agent 续接测试，覆盖“测试失败 → 修复后通过 → 再次修改尚未复测”；真实模型验收逐项检查目标、约束和进度，要求引用摘要原文，并设置遗漏目标、约束反转和过时状态的负例；
- 接入工作区根目录 `MiniCode.md`：检查 UTF-8、NUL、64 KiB 文件上限和 4,096 token 原文预算；文件不存在或为空时不注入，读取错误明确返回，不截断内容；
- 项目记忆独立于 Session 历史和摘要，每个用户轮次及手动 `/compact` 前重新读取；成功 `write` / `edit` 根目录记忆后，整批工具结果完成再刷新，下一次模型请求使用新内容；
- 复用 `read` / `write` / `edit` 和现有审批维护记忆；写入前校验完整候选内容。系统 Prompt 约定仅在用户明确要求时保存、更新或删除，临时进度留在 Session；
- 实现按需记忆：`MiniCode.md` 常驻关键约定和简短目录，专题正文放在 `.minicode/memory/*.md`，由模型按任务调用 `read`；详情共用 UTF-8、NUL 和 64 KiB 检查，不套用常驻 4,096 token 预算；
- 记忆入口迁为 `apps/MiniCode.md`，四个本地专题位于 `apps/.minicode/memory/`。从 `apps/` 启动时读取 `config/config.toml`，文件不存在时兼容旧配置路径；对应实现提交为 `9e9a857`；
- 接入会话级全屏界面：输入区固定底部，流式预览、最终回复、工具状态、审批、统计和错误统一显示在会话区，退出时恢复原终端；
- 支持等待输入和审批时通过滚轮、触控板或 PageUp/PageDown 查看消息；兼容 SGR 与 X10 鼠标事件，滚动和点击不会改动输入草稿，鼠标模式随输入读取开启和关闭；
- Day 14 真实模型语义验收已通过：使用配置模型 `gpt-5.6-sol`，三轮压缩分别通过 8、7、8 项事实检查，三个负例均被正确拒绝，测试耗时 54.46 秒；
- 本地回归、全量 race 测试、`go vet` 和编译已通过；滚动修复后的 CLI 全量 race 测试也已通过。早期两次真实模型请求的上游错误记录保留在开发计划中。

### 2026-10-12

- 完成 Day 13：固定使用 256k 上下文窗口、16k 压缩触发余量和 8k 输出上限；普通模型请求包含新增用户输入，并在完整工具结果之后检查下一次请求预算；
- 保留最近约 20,000 token 的原始消息，使用 pi 的首次摘要和更新摘要 Prompt 压缩更早历史；摘要输入不截断，超预算或服务端报上下文超限时分块处理；
- `/compact`、自动触发和超限恢复共用同一压缩函数；当时成功后替换内存历史、重置预算校准并保存有效会话，失败时保留原历史。当前已按 2026-10-13 更新分离原始历史与压缩检查点；
- 补充滚动摘要、完整工具结果、UTF-8 分块、失败与取消、用量统计、恢复不重放工具及手动压缩存档恢复测试；全量测试已通过；
- 补齐 Day 12 实现记录：模型生成过程中在备用屏幕渲染 Markdown，后续文本按 50 毫秒间隔合并刷新；
- 支持跨分片和未闭合的 Markdown 格式，长回复预览跟随末尾，窗口尺寸变化时自动重排；
- 复用 Markdown 主题与渲染器，渲染失败时回退到原文预览；当时每次预览结束、错误、超时和取消时恢复光标和主屏幕，当前已改为会话级全屏生命周期；
- 补充实时渲染、合并刷新、跨轮清理、失败回退、并发关闭及 PTY 显示与尺寸变化测试；全量测试、CLI race 检查、`go vet` 和编译已通过。

### 2026-10-11

- 补齐 Day 11 实现记录：普通启动自动使用 SSE 流式请求，无需配置开关；
- 当时终端在备用屏幕逐段预览模型文本，流结束后恢复主屏幕并渲染完整 Markdown；当前已改为会话级全屏生命周期。管道和文件输出保留完整回复，不写入增量预览；
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
- 当时运行过程使用终端备用屏幕，最终回答、失败或取消时恢复主屏幕；当前已改为会话级全屏生命周期；
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
- [`docs/tutorial.md`](docs/tutorial.md)：配置、启动、会话恢复、上下文压缩、项目记忆、审批和超时的使用教程。

## 目录结构

```text
MiniCode-go/
├── README.md
├── docs/
│   ├── spec.md
│   ├── plan.md
│   ├── agent.md                       # Agent 操作规范
│   └── tutorial.md                    # 使用教程
└── apps/                              # Go module 与本项目运行工作区
    ├── MiniCode.md                    # 常驻约定、常用命令与按需索引
    ├── .minicode/                    # 本地数据，Git 忽略
    │   ├── memory/                   # 按需专题详情
    │   └── sessions/                 # 当前工作区的 Session 存档
    ├── go.mod
    ├── cmd/minicode/main.go           # 启动入口
    ├── internal/cli/                  # CLI 参数、输入与展示
    │   ├── run.go                    # 配置、信号和会话组装
    │   ├── input.go                  # 多行编辑、按键、鼠标事件与终端模式
    │   ├── input_display.go          # 折行、光标定位与滚动显示
    │   ├── screen.go                 # 全屏会话、固定底部输入区与消息滚动
    │   ├── terminal_mode_unix.go     # 无回显模式、恢复与窗口变化通知
    │   ├── terminal_mode_other.go    # 其他平台的终端模式接入
    │   ├── approval.go               # 副作用工具审批与自动批准
    │   ├── output.go                 # 流式预览、运行状态与最终回答展示
    │   ├── stream_preview.go         # Markdown 实时预览、合并刷新与视口
    │   └── markdown.go               # 终端检测与 Markdown 渲染
    ├── internal/agent/                 # Agent Loop、模型调用与工具执行
    │   ├── run.go                      # Session、交互循环与持久化接入
    │   ├── prompt.go                   # 系统 Prompt
    │   ├── approval.go                 # 工具审批接口
    │   ├── stream.go                   # 自动流式调用与完整响应聚合
    │   ├── compact.go                  # 压缩入口、预算重置与超限恢复
    │   ├── memory.go                   # 项目记忆加载与写后刷新识别
    │   └── output.go                   # 最终输出、统计与流式预览接口
    ├── internal/config/                # TOML 配置读取与校验
    │   └── config.go
    ├── internal/session/               # Session 快照格式、校验和文件存储
    │   └── store.go                    # JSON 读取与原子写入
    ├── internal/compaction/            # 上下文预算与 LLM 摘要压缩
    │   ├── trigger.go                  # usage 校准与触发阈值
    │   ├── compact.go                  # 近期消息保留、完整序列化与分块摘要
    │   ├── state.go                    # 独立检查点、保留边界与模型上下文构造
    │   └── prompt.go                   # pi 首次和更新摘要 Prompt
    ├── internal/memory/                # 常驻加载与按需详情校验
    │   ├── load.go
    │   └── files.go                   # 详情路径识别与完整候选内容校验
    ├── internal/token/                 # 记忆与上下文共用的 token 粗估
    │   └── estimate.go
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
    │   ├── messages.go                 # 公共消息序列与工具配对校验
    │   ├── openai.go                   # 非流式 HTTP 调用与共用请求校验
    │   └── openai_stream.go            # SSE 请求、事件解析、取消与关闭
    ├── internal/telemetry/             # OpenTelemetry / Langfuse 追踪配置
    │   └── langfuse.go
    └── test/                           # 测试文件单独目录(black-box)
        ├── cli/                       # CLI、输入、终端显示与退出测试
        │   ├── config_test.go          # 工作区配置、旧路径兼容及 apps 记忆读取
        │   ├── stream_preview_test.go  # 实时渲染、合并刷新、回退与关闭
        │   ├── screen_terminal_test.go # 全屏、输入区、鼠标滚动、缩放、审批与退出恢复
        │   └── stream_preview_terminal_test.go # PTY 中的排版、视口与尺寸变化
        ├── agent/                     # Agent、Fake Provider 与持久化测试
        │   ├── agent_test.go
        │   ├── run_test.go
        │   ├── session_store_test.go
        │   ├── compact_test.go         # 自动压缩、超限恢复与统计
        │   ├── memory_test.go          # 加载、注入、预算、压缩与恢复
        │   ├── memory_mutation_test.go # 写入、更新、删除、审批与写后刷新
        │   ├── memory_detail_test.go   # 按需读取、目录维护、压缩与恢复
        │   └── stream_test.go          # 自动流式调用、聚合与失败回滚
        ├── memory/
        │   ├── load_test.go            # UTF-8、大小、预算、工作区隔离与读取错误
        │   └── files_test.go           # 按需详情路径范围
        ├── compaction/                 # 触发阈值、保留边界与完整摘要输入
        │   ├── trigger_test.go
        │   ├── compact_test.go
        │   ├── semantic_test.go         # 三次压缩、事实输入、存档恢复与续接
        │   └── semantic_live_test.go    # 显式启用的真实模型语义验收
        ├── tools/                      # 文件工具、bash 与注册表测试
        │   ├── bash_test.go
        │   ├── read_test.go
        │   ├── write_test.go
        │   ├── edit_test.go
        │   ├── filesystem_test.go
        │   ├── memory_test.go          # 记忆候选内容校验与删除
        │   └── registry_test.go
        └── provider/
            ├── provider_test.go        # 配置工厂与流式能力边界
            ├── openai_test.go
            ├── openai_stream_test.go   # SSE 协议、错误、取消与追踪
            ├── messages_test.go        # 消息序列校验与非法请求零发送
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

# 在仓库根目录准备配置，供 apps 工作区读取 config/config.toml
cp apps/config/config.example.toml apps/config/config.toml
$EDITOR apps/config/config.toml
```

```bash
# 从仓库根目录进入完整运行工作区
cd apps

# 启动交互模式：Enter 提交整段输入
../bin/minicode

# 也可通过 stdin 批量输入；EOF 后退出
printf '分析这个项目\n运行测试\n' | ../bin/minicode

# /exit 和 /quit 均可结束交互会话
```

运行工作区的首选配置是 `config/config.toml`，仅缺失时兼容 `apps/config/config.toml`；两种仓库内配置位置都已被 Git 忽略。模型配置不能通过环境变量或 CLI flag 覆盖。可选的 `[langfuse]` 配置及其敏感数据范围见上文“配置模型服务”。

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

### 项目长期记忆：`MiniCode.md`

在启动目录的根目录维护 UTF-8 文本文件 `MiniCode.md`，常驻保存关键约定、常用命令和按需资料的简短目录。同一工作区的新会话会读取它，无需恢复旧 Session；文件不存在时按没有项目记忆处理，不自动创建。本项目入口位于 `apps/MiniCode.md`，详情位于 `apps/.minicode/memory/`，从 `apps/` 启动后路径均相对于该目录。

可以直接编辑文件，也可以在对话中明确要求维护记忆，例如：

```text
> 记住：本项目使用 go test ./... 运行测试。
> 更新项目记忆：默认测试命令改为 go test -race ./...。
> 把会话恢复的设计结论保存为按需记忆，并更新目录。
> 忘记项目记忆中的默认测试命令。
> 清空全部项目记忆。
```

模型通过现有 `read` / `write` / `edit` 工具执行，写入和编辑沿用审批；`--yes` 自动批准。系统 Prompt 要求先读再改、保留无关内容、避免重复和冲突，仅在用户明确要求时维护记忆。记忆查询和维护统一使用普通对话，按用户约定不提供 `/memory`、`/forget` 专用命令。

根目录文件最多 64 KiB，原文预算最多 4,096 个本地估算 token；正文和目录合计需要满足预算。详情文件同样要求合法 UTF-8、不含 NUL、最多 64 KiB，不套用常驻 token 预算。写入和编辑校验完整候选内容，失败不替换原文件；损坏的根目录记忆阻止启动或该次任务，详情错误在显式读取时作为工具错误返回。

宿主只自动加载整个 `MiniCode.md`，每个用户轮次和手动 `/compact` 前重新读取；根目录成功写入后，在整批工具结束时刷新。模型根据目录中的名称、适用场景和路径选择 `.minicode/memory/*.md` 并调用 `read`，不会由宿主预读整库。读过的正文进入普通会话历史并参与压缩；新会话仍只先加载常驻部分和目录。

保存专题时，提示模型先成功写入详情再更新目录；忘记专题时先清空详情再移除目录项，清空全部记忆时先清空目录列出的详情再清空根文件。目录选择、同步及语义去重由模型遵循提示完成，多文件修改不是一个原子事务；任一步失败应如实报告。详情属于被 Git 忽略的本地资料，删除记忆不清除旧会话消息或摘要。示例与操作流程见 [使用教程](docs/tutorial.md#按需记忆的读取与维护)。

### 上下文压缩：`/compact`

长任务会在每次模型请求前检查上下文预算，包含本次新用户输入、项目记忆及其包装、已有历史和工具声明；工具执行期间等待整批结果写入历史后再检查。当前固定按 `256 * 1024` token 的窗口计算，剩余空间严格小于 `16 * 1024` token 时自动调用压缩。

也可以在输入循环中主动执行：

```text
> /compact
```

压缩固定保留最近约 `20,000 token` 的原始消息，使用当前模型和 pi 的摘要模板总结更早内容；再次压缩时更新已有摘要。这些预算均为代码常量，不提供配置项。保留边界按完整消息和工具批次选择，最新整体很大时，近期原文可能超过 20k。

摘要输入完整发送，超预算时按 UTF-8 边界分块更新摘要，不额外截断工具结果。工具执行阶段原有的输出大小限制继续生效。没有可压缩旧内容时跳过模型调用；摘要失败、为空、被输出长度限制截断、返回工具调用或被取消时，保留旧检查点和原始历史。

成功后，仅更新独立检查点中的摘要和近期消息起点，模型读取“系统 Prompt → 项目记忆（如有）→ 新摘要 → 近期原文”。项目记忆块不作为历史送入摘要，连续压缩后仍由当前文件重新构造。完整原始消息继续保留在内存和 Session v2 存档中；恢复时重新生成系统 Prompt、读取最新项目记忆，并按检查点选择模型上下文。v1 存档会迁移为 v2，旧版已经丢弃的原文无法恢复。手动压缩立即保存，自动压缩和超限恢复在本轮结束时保存。摘要请求的用量计入统计，主对话预算在下一次正常回复后重新校准。

压缩前、候选上下文提交前和模型请求发送前共用严格消息校验：角色字段、完整参数 JSON、同批次调用 ID 唯一性及调用与结果配对都要通过。缺少、重复或无对应调用的工具结果会在本地报错，停止该次模型请求。完整工具批次的结果可按任意顺序返回；摘要与最新输入形成连续 user 消息也可正常发送。

模型明确返回上下文超限时，程序调用同一压缩函数，仅重试当前主对话请求一次，已完成的工具不会重新执行。代码未自动识别实际模型窗口；最新输入或工具批次本身过大时，压缩历史仍可能无法使请求放入窗口。详细流程见 [使用教程](docs/tutorial.md#5-上下文压缩compact) 和 [规格说明](docs/spec.md#43-上下文压缩)。

### 终端输出与工具执行

终端中的完整模型回复使用 [Glamour](https://github.com/charmbracelet/glamour) 渲染 Markdown，支持标题、加粗、列表和代码高亮，并按终端宽度换行。默认使用 `dracula` 主题，可通过 `GLAMOUR_STYLE` 覆盖，例如浅色终端可设置 `GLAMOUR_STYLE=light`。输出到管道或文件时保留 Markdown 原文；渲染失败时也会回退到原文。工具审批信息按原文展示，工具结果回传模型并保存在会话历史中。

流式接收和实时 Markdown 渲染自动启用。当标准输入和标准输出都连接终端时，CLI 在整个会话中共用一个备用屏幕，底部固定输入区，上方显示会话记录。模型文本边生成边渲染标题、列表、加粗和代码块，后续增量按 50 毫秒间隔合并刷新；未闭合格式会随新文本重新排版，渲染失败时回退原文。

生成时自动跟随最新内容；窗口尺寸变化时重新排版，快速缩放也会触发重绘。每次模型流结束清除临时预览，再在会话区显示完整回复；工具状态、审批、统计和错误也在该区域。普通任务失败或超时后可继续输入，退出会话或 Ctrl+C 时才恢复原终端屏幕和模式。

等待输入或审批时，可用滚轮、触控板上下滑动及 PageUp/PageDown 查看会话记录，输入区固定在底部，已有草稿和光标位置不受影响。当前鼠标模式只在读取输入期间开启；模型生成和工具执行期间尚不支持滚动。详细操作见 [使用教程](docs/tutorial.md#全屏会话与滚动)。

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

每项任务最多进行 500 轮可使用工具的模型请求。达到上限后，再请求一次不带工具的最终总结；若总结失败或为空，则输出停止说明。达到上限仍返回非零退出码。普通模型请求、最终总结和上下文摘要的输出上限均固定为 8,192 token；输出达到上限不等同于上下文超限，不因此自动增加上限。`-timeout` 默认 1 小时，覆盖模型请求、工具执行、上下文摘要和最终总结，也用于手动 `/compact`。Ctrl+C 或超时会终止当前命令进程组；单次工具输出最多保留 64 KiB，并标记截断。命令失败的输出和错误也会回传模型。

```bash
# 以下命令从仓库根目录执行；go -C apps run 使用 apps 作为工作区
go -C apps run ./cmd/minicode

# 只运行测试目录，避免显示源码包的 [no test files]
go -C apps test -count=1 ./test/...

# 检查全屏布局、滚动、流式预览及终端 Ctrl+C 恢复
go -C apps test -race ./test/cli -run 'TestMiniCode_FullScreen|TestOutput|TestMiniCode_TerminalCtrlC' -count=1
```

真实模型语义验收需显式启用，以独立模型请求逐项判断摘要是否保留目标、约束和最新进度；普通 `go test` 默认跳过该测试。测试先切换到仓库根目录，优先读取 `config/config.toml`，不存在时读取本项目通常使用的 `apps/config/config.toml`。该验收仅在测试阶段运行。

```bash
MINICODE_TEST_LLM=1 go -C apps test ./test/compaction -run '^TestCompact_LiveSemanticAcceptance$' -count=1 -v -timeout=10m
```

2026-10-13 使用 `gpt-5.6-sol` 运行上述测试，三轮连续压缩与保存恢复后的事实检查、三个负例全部通过，Day 14 已完成。该结果针对测试中的固定场景与当次模型配置；摘要仍是有损归纳，精确信息需结合工作区验证。Day 15 的按需记忆与工作区迁移已接入，真实模型的专题选择和维护验收待补；专用记忆命令不在范围内。生成中滚动仍属于后续工作，完整记录见 [开发计划](docs/plan.md)。
