# CLI 交互与终端实现总结

更新日期：2026-10-08。对应实现提交：`ef0b643`（集中 CLI 实现）、`49c507d`（多行编辑和 Ctrl+C 修复）与 `f5261f0`（任务状态提示区分）；当前工作区另已补充 Day 8 任务统计。

本次完成终端多行编辑、运行输出隔离，以及输入和任务执行期间的 Ctrl+C 退出修复。命令行位置参数、管道逐行输入和同一 Session 内的对话上下文继续沿用原有流程。

## 1. 当前交互行为

只有标准输入和标准输出都连接终端时，才启用交互编辑和备用屏幕。非交互输入使用 `bufio.Scanner`，每一行作为一轮任务；空白输入不会发送给模型。

| 操作 | 行为 |
| --- | --- |
| `Enter` | 提交整段输入，包括其中的换行 |
| `Shift+Enter` / `Ctrl+J` | 在光标处插入真实换行，不再使用 `↵` 代替换行 |
| `↑` / `↓` | 在显示行之间移动，包括自动折行产生的行；到首末行停止，不浏览历史 |
| `←` / `→` | 在当前输入内左右移动 |
| `Home` / `End` | 移到当前逻辑行的开头 / 末尾，逻辑行由真实换行分隔 |
| `Backspace` / `Delete` | 删除字符，删除换行符时合并相邻行 |
| `Ctrl+C` | 输入时丢弃未提交草稿并退出；运行中取消任务并退出整个会话 |
| `/exit`、`/quit`、EOF | 结束会话 |

上下移动会保留目标显示列，经过短行或空行后可以回到原列。光标位置按终端显示宽度计算，覆盖中文和普通 emoji；内容超过窗口高度时，显示区域随光标滚动。提交后完整输入留在终端记录中。

部分终端不能区分 `Shift+Enter` 和 `Enter`，可使用 `Ctrl+J` 换行。对话上下文仍在 Session 中跨轮保留，与上下键是否浏览输入历史无关。

## 2. 代码职责

| 位置 | 职责 |
| --- | --- |
| [`apps/cmd/minicode/main.go`](../apps/cmd/minicode/main.go) | 调用 `cli.Run`，将返回值作为进程退出码 |
| [`apps/internal/cli/run.go`](../apps/internal/cli/run.go) | 解析配置、检测交互模式、注册信号并组装输入、输出和 Session |
| [`apps/internal/cli/input.go`](../apps/internal/cli/input.go) | 选择终端或管道输入，转换按键，处理多行编辑与终端模式生命周期 |
| [`apps/internal/cli/input_display.go`](../apps/internal/cli/input_display.go) | 统一计算显示行、折行、光标位置和滚动区域 |
| [`apps/internal/cli/output.go`](../apps/internal/cli/output.go) | 展示工具调用和回复，管理备用屏幕 |
| [`apps/internal/cli/markdown.go`](../apps/internal/cli/markdown.go) | 检测终端，按宽度和主题渲染 Markdown |
| [`apps/internal/agent/session.go`](../apps/internal/agent/session.go) | 通过 `Input` / `Output` 接口驱动会话、模型和工具，不依赖具体终端实现 |

终端输入复用 `github.com/ergochat/readline` 的按键解析、缓冲区编辑和 raw 模式管理。真实换行叠加自动折行时，readline 的内置重绘会出现定位偏差，因此多行展示由 `inputDisplay` 统一处理，垂直光标移动也使用同一套显示行计算。

`Session.Run` 每次需要输入时启动一次读取，通过单元素缓冲 channel 接收结果，同时等待根 context 取消。运行中的任务结束后，若根 context 已取消，直接退出，不再启动下一轮输入。

## 3. Ctrl+C 问题与修复

### 问题原因

为区分 `Shift+Enter` 和 `Enter`，输入框启用了 Kitty 扩展键盘协议。该协议也会把 Ctrl+C 编码为 CSI-u 序列；原来的转换层只处理换行相关按键，readline 无法识别编码后的 Ctrl+C。

键盘协议此前在创建输入对象时开启、关闭对象时才恢复，覆盖了模型和工具执行阶段。此时 Ctrl+C 可能以转义序列进入标准输入，无法按原有方式触发 SIGINT。

终端退出测试还发现：运行中的任务被取消后，会话循环会先启动下一次输入再处理取消，可能在退出前重新进入 raw 模式。

### 修复方式

- `terminalKeyReader` 将 Kitty / CSI-u 的 `ESC[99;5u` 和 xterm modifyOtherKeys 的 `ESC[27;5;99~` 转换为 readline 的中断字符，保留传统 Ctrl+C 字节的处理。
- 扩展键盘协议随 raw 模式开启和恢复；提交、中断、EOF 或关闭输入时恢复原协议，模型和工具执行期间使用普通键盘模式。
- 输入阶段的中断由 `Input.Readline` 统一转换为 `context.Canceled`；运行阶段的 SIGINT / SIGTERM 取消根 context，沿现有链路取消 HTTP 请求和命令进程组。
- `Output.ToolError` 根据错误链区分任务中断、任务超时和普通失败：分别显示「任务已中断」「任务执行超时」或具体错误，避免泄漏 `context canceled` 等底层提示。
- 任务返回后检查根 context，已取消时立即退出，避免重新打开输入框；等待审批期间取消也沿同一链路结束。

当前 Ctrl+C 退出码为 `1`，输入、等待模型和等待审批时均输出一次「任务已中断」。当前行为是取消后退出整个会话，不是“仅取消本轮、保留会话”。

## 4. 运行输出与终端恢复

交互模式下，`BeginRunning` 进入备用屏幕展示阶段性模型文本、工具调用和实时输出。收到最终回答时先调用 `ClearRunning` 恢复主屏幕，再展示最终回答。发生模型错误、超时或取消时，通过延迟清理恢复主屏幕。

使用备用屏幕后，即使工具输出超过终端高度并触发滚屏，也不会把中间步骤写入主屏幕的滚动历史。非交互模式不启用备用屏幕，也不输出交互专用的模型轮次和工具状态事件；最终回答写入标准输出，任务统计和错误写入标准错误。屏幕清理只影响展示，不改变回传模型的工具结果和 Session 消息历史。

模型回复继续使用 Glamour 渲染 Markdown；默认主题为 `dracula`，支持 `GLAMOUR_STYLE` 覆盖。重定向输出或渲染失败时保留原文。

### 任务统计

每项任务结束时，CLI 输出一行任务级统计：

```text
[统计] 2.3 秒 ｜ 模型 2 次 ｜ 工具 1 次 ｜ Token 3,740（输入 3,200 / 输出 540）
```

- `模型` 统计本任务发起的模型请求，包括达到轮数上限后的最终总结请求；
- `工具` 统计进入执行流程的工具调用；
- Token 是本任务所有带 `usage` 响应的累计值，不跨任务累计；
- 所有响应都不带 `usage` 时显示 `Token 不可用`；只有部分响应带 `usage` 时追加 `（部分统计）`；
- 服务返回 `total_tokens = 0` 但输入或输出 token 非零时，以输入与输出之和补全总数。

交互模式将统计写入标准输出，并用颜色区分统计标题、耗时、调用次数和 Token 状态；`部分统计` 或不可用的 Token 使用黄色提示。数字使用千位分隔符，各数据项之间使用全角 `｜` 分隔。非交互模式将最终回答写入标准输出、无 ANSI 控制字符的统计写入标准错误，防止回答被状态信息污染。统计通过 `defer` 发送，因此成功、普通失败、取消和超时都会展示。

## 5. 验证记录

对应实现已通过以下检查；命令均从仓库根目录执行：

```bash
go -C apps test ./...
go -C apps vet ./...
go -C apps test -race ./test/cli -run 'TestMiniCode_TerminalCtrlC|TestInput' -count=1 -timeout=60s
go -C apps build -o ../bin/minicode ./cmd/minicode
./bin/minicode -h
```

| 测试文件 | 主要覆盖 |
| --- | --- |
| [`input_test.go`](../apps/test/cli/input_test.go) | 管道输入、换行编码、中文和 emoji 编辑、跨行移动、行合并、上下键不调用历史、Ctrl+C 与键盘协议生命周期 |
| [`input_terminal_test.go`](../apps/test/cli/input_terminal_test.go) | PTY 子进程中的多行显示、实际光标位置、软折行、滚动和中文显示列 |
| [`run_terminal_test.go`](../apps/test/cli/run_terminal_test.go) | 空输入、多行编辑、等待模型、等待审批及位置参数启动任务时的 Ctrl+C 退出；HTTP 取消、明确中断提示、退出码、草稿不提交及终端控制序列恢复 |
| [`output_test.go`](../apps/test/cli/output_test.go) | 备用屏幕切换、重复清理、交互与非交互任务统计，以及中断、超时和普通失败的提示分类 |
| [`run_test.go`](../apps/test/cli/run_test.go) | CLI 配置、模型与工具调用、管道和多轮会话兼容性、任务统计累计，以及任务超时提示 |

PTY 测试带有 `darwin || linux` 构建约束，本次验证不包含 Windows 终端。显示测试中的 vt10x 不支持 Kitty 键盘协议开关，会将其误解为光标恢复，因此显示模拟器忽略这两个序列；协议开关的配对和退出恢复由独立输入测试与真实 CLI 子进程测试检查。

## 6. 后续范围

扩展按键序列转换目前覆盖换行和 Ctrl+C；Ctrl+D 等其他组合键的扩展编码兼容尚未补齐。

本次没有改变流式输出、执行前确认、Session 持久化和上下文压缩的完成状态。token 统计目前依赖模型服务返回的 `usage`，不会在本地分词估算；统计只覆盖当前任务，不持久化也不跨 Session 汇总。后续计划继续以 [`plan.md`](plan.md) 为准。
