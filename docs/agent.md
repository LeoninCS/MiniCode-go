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
├── internal/provider/               # 模型协议 + OpenAI 兼容客户端(纯源码,无 _test.go)
│   ├── types.go
│   └── openai.go
└── test/provider/                   # 测试单独目录,black-box
    └── openai_test.go               # package provider_test
```

- 后续 Day:源码 `apps/internal/<name>/`,测试 `apps/test/<name>/`,同名目录
- `internal/` 不依赖 `cmd/` 或非标准库
- go 命令全部 `go -C apps build/test/vet ./...`

## 3. 编码风格

- 中文 doc comment + 包注释
- 错误一律 `fmt.Errorf("context: %w", err)` 包装
- `main` 不直接 `os.Exit`,由 `run(args, stdin, stdout, stderr) int` 返回退出码,便于测试
- HTTP handler 阻塞 ctx 时用 `select { case <-ctx.Done(): case <-time.After(backup): }` 防 `srv.Close()` hang
- 测试覆盖正常 + 至少一个错误路径

轻量原则:写前先问 ① 必要抽象吗 ② dead field 吗 ③ 1 处常量要抽吗 ④ 中间层能拆吗 ⑤ 注释自明吗 ⑥ doc-only 导出真需要吗。
判断:新读者从 0 读这段,删掉是否更省力?是 → 删;否 → 留。

## 4. Day 完成前自检

1. `go -C apps build ./...` → 0
2. `go -C apps vet ./...` → 0 告警
3. `go -C apps test -count=1 ./...` → 全过
4. 端到端 smoke:mock OpenAI 端点,跑 happy + 401
5. `docs/plan.md` + `README.md` 该 Day 改 ✅
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
- **Day 2 不执行工具**：CLI 按计划声明并展示 `bash` 调用；进入 Day 4 前需统一 `bash{command}` 与 `spec.md` 中 `run_command{argv}` 的最终安全契约

## 6. 已知陷阱

1. bash session 不保留 cwd,需 `cd path && cmd` 或 `go -C path cmd`,不要假设 PWD 已被切过
2. Edit/Write 路径相对仓库根,不是 shell cwd
3. httptest handler 阻塞 `r.Context().Done()` 不会在客户端断开时立即返回,必须加 server-side 兜底超时
4. OpenAI 错误嵌套在 `error` 字段下,只用平铺解析会退化成原始 body
5. 用户偏好:微信端纯文本;commit 用 `conventional + 中文 subject`;能查文件/plan/spec 就查,不替用户瞎猜

任何与本规范冲突的改动,先改本文件,再改实现。
