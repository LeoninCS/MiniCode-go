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

MiniCode 需要一个兼容 OpenAI `/chat/completions` 接口的模型服务。复制配置模板并填写自己的 API Key、Base URL 和模型名：

```bash
cp .env.example .env
$EDITOR .env
source .env
```

例如使用 DeepSeek 时，`.env` 可以填写：

```bash
MINICODE_API_KEY=你的_API_Key
MINICODE_BASE_URL=https://api.deepseek.com/v1
MINICODE_MODEL=deepseek-chat
```

请勿将 `.env` 或真实 API Key 提交到 Git；本项目已在 `.gitignore` 中忽略 `.env`。

Windows PowerShell 可以直接设置当前终端会话的环境变量：

```powershell
$env:MINICODE_API_KEY="你的_API_Key"
$env:MINICODE_BASE_URL="https://api.deepseek.com/v1"
$env:MINICODE_MODEL="deepseek-chat"
```

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

输入 `/exit`、`/quit` 或发送 EOF 即可退出。当前版本不会在启动时询问模型配置，如果缺少配置会给出提示并退出。

## 开发进度

- ✅ Day 0：项目定位、功能范围和开发计划；
- ✅ Day 1：协议结构体 + 单次非流式模型调用；
- ✅ Day 2：工具 Schema 定义 + 工具调用响应解析；
- ✅ Day 3：Agent Loop；
- ✅ Day 4：`bash` / `read` / `write` / `edit` 四个工具与工具注册表；
- ✅ Day 5：系统 Prompt + 默认 CLI 输入循环 + 会话内跨轮记忆；
- 🔄 Day 6：已完成输出截断、500 轮上限和单轮超时；待实现执行前确认、模型单轮输出长度限制和基础危险命令限制；
- ⬜ Day 7：token 统计和过程可视化；
- 🔄 Day 8：已完成 Ctrl+C / SIGTERM 中断与 context 取消链路，待补用户取消与任务失败的中文提示区分；
- ⬜ Day 9：Session 持久化与恢复（当前仅进程内跨轮记忆）；
- ⬜ Day 10：Provider 抽象层；
- ⬜ Day 11：SSE 流式解析；
- ⬜ Day 12：tool_calls 参数分片累积与打字机输出；
- ⬜ Day 13：上下文压缩触发与摘要；
- ⬜ Day 14：压缩切分点合法性处理。

## 最近更新

### 2026-10-01

- 按代码实际状态核对并修正开发进度：Day 8 的取消链路已完成，剩余「区分用户取消与任务失败」和「输出任务中断状态」两项；
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
- [`docs/agent.md`](docs/agent.md)：Agent 操作规范（硬边界、已固化决策、已知陷阱），用于防止多 Day 实施中的细节漂移。
- [`docs/tools.md`](docs/tools.md)：工具体系（目录职责、内置工具、注册表机制与决策记录）。

## 目录结构

```text
MiniCode-go/
├── README.md
├── docs/
│   ├── spec.md
│   ├── plan.md
│   ├── agent.md                       # Agent 操作规范
│   └── tools.md                       # 工具体系
└── apps/                              # Go module: github.com/MiniCode-go/minicode
    ├── go.mod
    ├── cmd/minicode/                  # CLI 参数、输入与展示
    │   ├── main.go
    │   └── output.go
    ├── internal/agent/                 # Session、模型循环、消息历史与系统 Prompt
    │   ├── session.go                  # 交互会话与 Agent Loop
    │   ├── prompt.go                   # 系统 Prompt
    │   └── output.go                   # Agent 输出接口
    ├── internal/terminal/markdown.go  # 终端检测与 Markdown 渲染
    ├── internal/tools/                 # 工具实现 + 注册机制
    │   ├── registry.go                # Tool、ToolRegistry、参数公共校验
    │   ├── bash.go                    # bash 执行、输出截断与取消
    │   ├── read.go                    # read 工具
    │   ├── write.go                   # write 工具
    │   ├── edit.go                    # edit 工具
    │   └── filesystem.go              # 工作区句柄、原子写入与版本记录
    ├── internal/provider/              # 模型协议结构体 + OpenAI 兼容客户端(纯源码)
    │   ├── types.go
    │   └── openai.go
    └── test/                           # 测试文件单独目录(black-box)
        ├── cmd/minicode/main_test.go   # CLI 与 Agent Loop 端到端测试
        ├── agent/agent_test.go         # Agent 输出边界与错误回传测试
        ├── tools/                      # 文件工具、bash 与注册表测试
        │   ├── bash_test.go
        │   ├── read_test.go
        │   ├── write_test.go
        │   ├── edit_test.go
        │   ├── filesystem_test.go
        │   └── registry_test.go
        └── provider/
            ├── openai_test.go
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

# 准备配置(任选一种)
cp .env.example .env && $EDITOR .env && source .env   # 一次配置,反复使用
# 或者直接 export 三行(见 .env.example 里的常用值)
```

```bash
# 启动交互模式：每行提交一轮任务，历史在当前会话内持续保留
./bin/minicode

# 也可通过 stdin 批量输入；EOF 后退出
printf '分析这个项目\n运行测试\n' | ./bin/minicode

# /exit 和 /quit 均可结束交互会话

# 也可以完全用 flag 覆盖配置（flag 优先级最高）
./bin/minicode -api-key sk-... -base-url https://api.example.com/v1 -model x
```

完整配置项与示例值见仓库根 [`.env.example`](.env.example)。`.env` 不进 git,放本地。

每个 Session 启动时会注入系统 Prompt，其中包含当前工作区、文件路径边界、工具说明、先读后改、修改后验证等规则。同一进程中的用户消息、模型回复、工具调用和工具结果会持续累积，因此后续问题可以引用前面的内容；会话尚不会保存到磁盘，退出程序后不能恢复。

终端中的模型回复使用 [Glamour](https://github.com/charmbracelet/glamour) 渲染 Markdown，支持标题、加粗、列表和代码高亮，并按终端宽度换行。默认使用 `dracula` 主题，可通过 `GLAMOUR_STYLE` 覆盖，例如浅色终端可设置 `GLAMOUR_STYLE=light`。输出到管道或文件时保留 Markdown 原文；渲染失败时也会回退到原文。工具调用信息和命令输出继续原样显示。

CLI 支持普通文本回复和 `bash` / `read` / `write` / `edit` 四个工具。模型请求工具时，会先打印调用信息，再执行、展示输出并把结果回传模型，继续请求直到得到最终回复，例如：

```text
tool: bash
arguments: {"command":"go test ./..."}
```

命令通过 `bash -c` 在启动 CLI 时的工作目录直接执行，标准输出和错误输出会合并显示。每次调用使用独立的非交互 shell，`cd` 不会影响下一次调用；当前工作目录不是文件系统沙箱。

每项任务最多进行 500 轮可使用工具的模型请求。达到上限后，再请求一次不带工具的最终总结；若总结失败或为空，则输出停止说明。达到上限仍返回非零退出码。`-timeout` 默认 1 小时，覆盖模型请求、命令执行和最终总结。Ctrl+C 或超时会终止当前命令进程组；单次输出最多保留 64 KiB，并标记截断。命令失败的输出和错误也会回传模型。

```bash
# 从项目根目录启动（已有环境变量配置），然后在交互界面中输入任务
go -C apps run ./cmd/minicode

# 只运行测试目录，避免显示源码包的 [no test files]
go -C apps test -count=1 ./test/...
```

当前采用直接执行模式；交互确认、会话持久化、流式输出和上下文压缩仍在后续计划中。工具的组织方式与注册表机制见 [`docs/tools.md`](docs/tools.md)。
