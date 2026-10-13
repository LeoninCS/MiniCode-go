# MiniCode 项目记忆

## 项目定位

MiniCode-go 是使用 Go 实现的教学型 CLI Coding Agent，围绕模型调用、工具执行、多轮会话、持久化和上下文压缩构建本地开发闭环。工作区是启动 MiniCode 时所在的项目目录。

## 代码组织

- Go module 根目录是 `apps/`，版本要求见 `apps/go.mod`。
- `apps/cmd/minicode/main.go` 是启动入口。
- `apps/internal/cli/` 负责配置组装、输入、终端展示和审批交互。
- `apps/internal/agent/` 负责 Agent Loop、会话和压缩接入。
- `apps/internal/provider/` 负责模型协议、请求、流式响应和消息校验。
- `apps/internal/tools/` 负责工具注册和 `bash`、`read`、`write`、`edit`。
- `apps/internal/session/` 负责会话快照校验与文件存储。
- `apps/internal/compaction/` 负责上下文预算、滚动摘要和压缩检查点。
- `apps/internal/memory/` 负责读取工作区根目录的 `MiniCode.md`。
- `apps/internal/token/` 提供共享的本地 token 估算规则。

## 开发与验证

测试放在 `apps/test/<对应包名>/`，使用 `package <包名>_test`，通过导出 API 验证行为。源码目录不放测试文件。修改 Go 文件后运行 `gofmt`。

在仓库根目录执行：

```bash
go -C apps build ./...
go -C apps test -count=1 ./test/...
go -C apps test -race -count=1 -timeout=5m ./...
go -C apps vet ./...
```

模型配置固定读取工作区下的 `apps/config/config.toml`，示例是 `apps/config/config.example.toml`。配置中的密钥不写入项目记忆，也不提交到 Git。真实模型测试需要显式启用；普通回归使用 Fake Provider。

## 已确定的约定

- 文件工具使用工作区边界；修改已有文件前先读取，检测外部修改，避免覆盖未观察到的新内容。
- `bash`、`write`、`edit` 默认经过执行前审批，`read` 可直接执行；命令执行复用现有审批机制。
- 会话保留完整原始历史，压缩只更新独立摘要和近期消息起点；模型上下文由系统 Prompt、摘要和近期原文组成。
- 模型请求发送前检查消息是否合法，工具调用与结果必须完整配对；切分和压缩保留完整工具批次。
- 项目文档只在用户明确要求新增、修改或同步时更新。功能完成和测试通过不构成文档更新授权，具体规则见根目录 `AGENTS.md`。

## 记忆维护

本文件保存稳定的项目知识和用户明确要求长期保留的约定。用户要求记住、更新或删除时再修改；临时任务进度和运行结果由会话保存。记忆与当前代码或用户最新指令冲突时，先核对再使用。

记忆文件使用 UTF-8，大小上限为 64 KiB。加载时按调用方传入的 token 预算校验，默认预算常量为 4096 token；超限返回错误，保留原文，不自动截断。预算只覆盖原文，消息包装开销由注入方另计。文件不存在时视为没有项目记忆。
