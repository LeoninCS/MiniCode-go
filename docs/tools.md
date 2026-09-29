# 工具体系

记录内置工具的组织方式、注册表职责与相关决策。
与 `agent.md`(操作规范)/ `plan.md`(按天做什么)的关系:本文件写"工具怎么组织 + 为什么这么定"。

## 1. 职责划分

```text
internal/tools/                       # 工具实现 + 注册机制,全部在本包
├── bash.go        # RunBash + executeBashTool + buildBashTool + commandOutput
├── read.go        # FileTools.ReadFile + executeReadTool + buildReadTool
├── write.go       # FileTools.WriteFile + executeWriteTool + buildWriteTool
├── edit.go        # FileTools.EditFile + executeEditTool + buildEditTool
├── filesystem.go  # FileTools、工作区句柄、原子写入与版本记录
└── registry.go    # Tool、ToolRegistry、NewBuiltinToolRegistry、decodeToolArguments
```

- `registry.go` 只负责注册与分发机制：类型定义、增删查、按序分发、参数公共校验
- 每个工具的**声明和执行跟随工具走**：`<tool>.go` 里同时放业务方法、`execute*Tool` 和 `build*Tool`
- 加工具只改三处：对应 `<tool>.go`、`NewBuiltinToolRegistry` 的一个 `Tool{}` 条目、一个名字常量
- 工具名常量集中在 `registry.go` 顶部，因为被多个文件共享

## 2. 内置工具

| 工具 | 声明 | 执行 | 参数校验 |
|------|------|------|----------|
| `read` | `buildReadTool` | `executeReadTool` | `path` 必填,`offset`/`limit` 可选 |
| `bash` | `buildBashTool` | `executeBashTool` | `command` 必填,仅支持单参数 |
| `edit` | `buildEditTool` | `executeEditTool` | `path`/`old_text`/`new_text` 必填,`replace_all` 可选 |
| `write` | `buildWriteTool` | `executeWriteTool` | `path`/`content` 必填 |

参数校验统一走 `decodeToolArguments`,在执行前完成：必填缺失、未知字段、`null` 值、类型错误。
字段名区分大小写;可选字段省略时保留零值。校验失败不产生输出、不改动文件。

## 3. 工具协议

- `Tool` 是 struct 而非 interface：`Definition provider.Tool` + `Execute` 函数值
- 声明携带 JSON Schema,调用参数在线格式保持 JSON 字符串
- `ToolRegistry` 按注册顺序返回 `Definitions()`,顺序即发送给模型的顺序
- `Execute` 保留失败时已产生的输出,并把原始错误交给调用方
- `stdout` 为 `nil` 时退化为 `io.Discard`,不影响返回值和错误

## 4. 工作区

- `FileTools` 用 `os.OpenRoot` 打开工作区,路径越界和符号链接由 `os.Root` 负责
- `Run` 以进程当前工作目录为工作区(`tools.NewFileTools(".")`),任务结束时 `Close`
- `bash` 仍用进程 cwd,**不提供文件系统沙箱**——与文件工具的行为边界不同,需注意
- `read` / `edit` 共享版本记录:同一路径须先读或成功写入,且内容此后未被外部修改
- 写入走同目录临时文件 + 原子替换,已有文件保留权限位

## 5. 目录约定

- 源码 `apps/internal/<name>/`,测试 `apps/test/<name>/`,同名目录
- 测试文件与被测源文件**同名对应**:`tools.go` → `tools_test.go`,`registry.go` → `registry_test.go`
- 同包内避免重复定义辅助函数(如 `assertFile` 已在 `filesystem_test.go`,迁移时直接复用)

## 6. 决策记录

### 2026-09-29：工具注册与执行

- **`Tool` 用 struct 而非 interface**：声明与执行函数绑在一起,注册时即可校验声明完整性;
  避免 interface 带来的隐式契约和 nil 陷阱
- **注册逻辑整体迁入 `internal/tools`**：工具的声明、执行、注册原本分散在 `internal/agent`,
  与实际实现分离。迁移后 `internal/agent` 只剩 `agent.go`(模型循环),职责更单一
- **声明与执行跟随工具文件**：`registry.go` 只留注册机制,`build*Tool` / `execute*Tool`
  放回各自 `<tool>.go`,避免新增工具时要跨文件查找
- **接入 Agent Loop**：`availableTools()` 和按名称 `switch` 的 `executeTool()` 已删除,
  分发统一由 `ToolRegistry.Execute` 承担。以后加工具只改一处,不再需要同步 switch 分支
- **错误前缀与工具名对齐**：`read_file:` / `write_file:` / `edit_file:` 统一为
  `read:` / `write:` / `edit:`,与工具注册名和 `bash:` 保持一致
- **`internal/tools` 依赖 `internal/provider`**：声明需要 `provider.JSONSchema`。
  依赖方向为 `agent → tools → provider`,无循环依赖

### 已知待办

- `Run` 硬编码工作区为当前目录,未支持 `-workspace` 参数。若要隔离需把注册表作为参数注入
- 无上下文压缩:达到 500 轮上限时直接生成最终总结,不做中途压缩。详见 `agent.md` 的轮数上限决策
- 无 `list_files` / `git_diff` 工具,仍依赖 `bash` 列举目录
