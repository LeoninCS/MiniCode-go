# MiniCode 项目记忆

## 项目定位

MiniCode-go 是使用 Go 实现的教学型 CLI Coding Agent，围绕模型调用、工具执行、多轮会话、持久化和上下文压缩构建本地开发闭环。本项目以当前目录（仓库的 `apps/`）作为完整工作区，从本目录启动 MiniCode；以下路径均相对于本目录。

## 常驻约定

- 当前目录是 Go module 根目录，Go 版本要求见 `go.mod`；测试放在 `test/<对应包名>/`，使用外部测试包，源码目录不放测试文件。修改 Go 文件后运行 `gofmt`。
- 文件工具使用工作区边界；修改已有文件前先读取，检测外部修改，避免覆盖未观察到的新内容。
- `bash`、`write`、`edit` 默认经过执行前审批，`read` 可直接执行。
- 保留完整原始会话历史，压缩只更新独立检查点；模型请求发送前校验消息，工具调用与结果必须完整配对，切分保留完整工具批次。
- 项目文档只在用户明确要求新增、修改或同步时更新；功能完成和测试通过不构成授权。
- 只有用户明确要求记住、更新或删除时才维护记忆；只保存稳定知识，临时进度归 Session，不保存密钥或敏感信息。与当前代码或最新要求冲突时先核对。
- 模型配置优先读取 `config/config.toml`，不存在时兼容旧布局 `apps/config/config.toml`；示例是 `config/config.example.toml`，密钥不提交到 Git。普通回归使用 Fake Provider，真实模型测试需要显式启用。

## 常用验证命令

在本目录执行：

```bash
go build ./...
go test -count=1 ./test/...
go test -race -count=1 -timeout=5m ./...
go vet ./...
```

## 按需记忆索引

以下路径均相对于工作区。按当前任务的适用场景用 `read` 读取相关专题，不预读全部详情；目录只说明资料位置，未读取正文时不据此推断结论。详情位于被 Git 忽略的本地目录，缺失或过时时核对当前代码。

- [代码组织](.minicode/memory/architecture.md)：定位模块职责、新增功能，或确定源码与测试位置时参考。
- [会话与上下文压缩](.minicode/memory/session-compaction.md)：修改 Session 保存与恢复、压缩边界、模型上下文或消息配对时参考。
- [项目记忆](.minicode/memory/project-memory.md)：修改记忆加载、预算、按需读取、写后刷新，或维护目录与详情时参考。
- [终端交互](.minicode/memory/terminal.md)：修改全屏界面、多行输入、历史滚动、流式展示或终端恢复时参考。
