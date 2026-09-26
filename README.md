# MiniCode-go

## 项目简介与定位

MiniCode-go 是一个使用 Go 从零实现的、以 CLI 为主要交互入口的轻量级 Coding Agent，可以理解为一个面向原理学习的“迷你版 Pi”。

它运行在本地工作区中，用户通过命令行与 Agent 进行多轮交互。Agent 通过大语言模型理解任务，并使用受控工具完成代码浏览、文件修改、命令执行和测试验证：

```text
理解任务 → 检查代码 → 调用工具 → 修改代码 → 运行验证 → 根据结果继续修复
```

项目定位为一个**简单但完整的教学型终端 Agent**：尽量减少框架依赖和非必要抽象，但保留一个 Coding Agent 的核心能力，包括模型调用、工具调用、Agent Loop、CLI 会话、Session 持久化、上下文压缩、执行前确认、流式输出和结果验证。

首版从单 Agent、单任务、单本地工作区开始，不提供 Web UI、IDE 插件、多 Agent 或长期记忆，重点是通过逐步实现一个可运行的最小闭环，理解模型、Agent、工具和宿主程序之间的协作方式。

## 开发进度

- ✅ Day 0：项目定位、功能范围和开发计划；
- ✅ Day 1：协议结构体 + 单次非流式模型调用；
- ✅ Day 2：工具 Schema 定义 + 工具调用响应解析；
- ✅ Day 3：Agent Loop；
- 🔄 Day 4：已接入 `bash`；`write_file` 和工具注册表待实现；
- ⬜ Day 5：系统 Prompt + CLI 输入循环；
- ⬜ Day 6：输出截断、轮数上限和执行前确认；
- ⬜ Day 7：token 统计和过程可视化；
- ⬜ Day 8：Ctrl+C 中断与 context 取消；
- ⬜ Day 9：Session 持久化与恢复；
- ⬜ Day 10：Provider 抽象层；
- ⬜ Day 11：SSE 流式解析；
- ⬜ Day 12：tool_calls 参数分片累积与打字机输出；
- ⬜ Day 13：上下文压缩触发与摘要；
- ⬜ Day 14：压缩切分点合法性处理。

## 项目文档

- [`docs/spec.md`](docs/spec.md)：项目定位、功能规格、开发计划和验收标准；
- [`docs/plan.md`](docs/plan.md)：按天拆分的开发任务和完成状态；
- [`docs/agent.md`](docs/agent.md)：Agent 操作规范（硬边界、已固化决策、已知陷阱），用于防止多 Day 实施中的细节漂移。

## 目录结构

```text
MiniCode-go/
├── README.md
├── docs/
│   ├── spec.md
│   ├── plan.md
│   └── agent.md                       # Agent 操作规范
└── apps/                              # Go module: github.com/MiniCode-go/minicode
    ├── go.mod
    ├── cmd/minicode/                  # CLI 参数、输入与展示
    │   ├── main.go
    │   └── output.go
    ├── internal/agent/               # 模型循环、消息历史与工具分发
    │   ├── agent.go
    │   └── tools.go
    ├── internal/terminal/markdown.go # 终端检测与 Markdown 渲染
    ├── internal/tools/bash.go        # bash 命令执行、输出和取消
    ├── internal/provider/             # 模型协议结构体 + OpenAI 兼容客户端(纯源码)
    │   ├── types.go
    │   └── openai.go
    └── test/                          # 测试文件单独目录(black-box)
        ├── cmd/minicode/main_test.go  # CLI 与 Agent Loop 端到端测试
        ├── agent/agent_test.go        # Agent 输出边界与错误回传测试
        ├── tools/bash_test.go         # bash 执行、输出和取消测试
        └── provider/
            ├── openai_test.go
            └── tool_calls_test.go
```

约定:
- Go module 根在 `apps/`。
- 测试文件一律放在 `apps/test/<src-path>/`,与源码同名目录;用 `package <name>_test`
  风格,只测导出 API,源码目录保持干净。
- 构建/测试命令:`go -C apps build ./...` / `go -C apps test -count=1 ./...`。

## 构建与运行（Day 3）

```bash
# 构建
go -C apps build -o ../bin/minicode ./cmd/minicode

# 准备配置(任选一种)
cp .env.example .env && $EDITOR .env && source .env   # 一次配置,反复使用
# 或者直接 export 三行(见 .env.example 里的常用值)
```

```bash
# 运行：使用任意 OpenAI 兼容服务(DeepSeek / MiniMax / OpenAI / Moonshot 等)
./bin/minicode "用一句话介绍 Go 的 goroutine"

# 或者通过 stdin 传入多行输入
echo "用一句话介绍 Go 的 goroutine" | ./bin/minicode

# 也可以完全用 flag 覆盖(flag 优先级最高)
./bin/minicode -api-key sk-... -base-url https://api.example.com/v1 -model x "..."
```

完整配置项与示例值见仓库根 [`.env.example`](.env.example)。`.env` 不进 git,放本地。

终端中的模型回复使用 [Glamour](https://github.com/charmbracelet/glamour) 渲染 Markdown，支持标题、加粗、列表和代码高亮，并按终端宽度换行。默认使用 `dracula` 主题，可通过 `GLAMOUR_STYLE` 覆盖，例如浅色终端可设置 `GLAMOUR_STYLE=light`。输出到管道或文件时保留 Markdown 原文；渲染失败时也会回退到原文。工具调用信息和命令输出继续原样显示。

CLI 支持普通文本回复和 bash 工具执行。模型请求工具时，会先打印调用信息，再执行命令、展示输出并把结果回传模型，继续请求直到得到最终回复，例如：

```text
tool: bash
arguments: {"command":"go test ./..."}
```

命令通过 `bash -c` 在启动 CLI 时的工作目录直接执行，标准输出和错误输出会合并显示。每次调用使用独立的非交互 shell，`cd` 不会影响下一次调用；当前工作目录不是文件系统沙箱。

每项任务最多请求模型 10 次，`-timeout` 默认 60 秒，覆盖模型请求和命令执行。Ctrl+C 或超时会终止当前命令进程组；单次输出最多保留 64 KiB，并标记截断。命令失败的输出和错误也会回传模型。

```bash
# 从项目根目录运行（已有环境变量配置）
go -C apps run ./cmd/minicode "请调用 bash 执行 date -u; date，然后说明两个时间的区别"

# 只运行测试目录，避免显示源码包的 [no test files]
go -C apps test -count=1 ./test/...
```

当前采用直接执行模式；交互确认、工具注册表和流式输出仍在后续计划中。
