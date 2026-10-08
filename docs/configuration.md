# 配置与 Langfuse 追踪

MiniCode 使用一个固定路径的 TOML 文件配置模型服务和可选的 Langfuse 链路追踪。本文档对应当前实现，不适用于旧版 `.env` 配置方式。

## 1. 配置文件位置

MiniCode 只读取启动目录下的：

```text
apps/config/config.toml
```

程序不会搜索用户主目录或可执行文件所在目录，也不支持 `-config` 指定其他文件。旧版的以下配置来源均已停用：

- `.env` 文件；
- `MINICODE_API_KEY`、`MINICODE_BASE_URL`、`MINICODE_MODEL` 环境变量；
- `LANGFUSE_PUBLIC_KEY`、`LANGFUSE_SECRET_KEY`、`LANGFUSE_HOST` 环境变量；
- `-api-key`、`-base-url`、`-model` 等 CLI 参数。

在仓库根目录运行时，可直接复制模板：

```bash
cp apps/config/config.example.toml apps/config/config.toml
$EDITOR apps/config/config.toml
```

如果从另一个项目目录启动已安装的 `minicode`，则应在那个项目中创建同一路径：

```text
your-project/
└── apps/
    └── config/
        └── config.toml
```

配置文件可能包含 API Key，建议权限仅对当前用户开放：

```bash
chmod 600 apps/config/config.toml
```

本仓库已经在 `.gitignore` 中忽略 `/apps/config/config.toml`。在其他项目中使用时，也应将该路径加入项目自己的忽略规则。

## 2. 模型配置

`[model]` 下的三个字段均为必填项：

```toml
[model]
api_key = "sk-..."
base_url = "https://api.deepseek.com/v1"
name = "deepseek-chat"
```

| 字段 | 是否必填 | 说明 |
| --- | --- | --- |
| `model.api_key` | 是 | 模型服务的 API Key，用作 Bearer Token |
| `model.base_url` | 是 | OpenAI 兼容服务的根地址，末尾 `/` 会自动去除 |
| `model.name` | 是 | 请求中的模型名 |

MiniCode 会向以下地址发起非流式请求：

```text
{base_url}/chat/completions
```

例如 `base_url = "https://api.deepseek.com/v1"` 时，请求地址为 `https://api.deepseek.com/v1/chat/completions`。

## 3. Langfuse 配置

Langfuse 是可选功能。不需要追踪时，可以省略整个 `[langfuse]` 表，或同时留空公钥和私钥：

```toml
[langfuse]
public_key = ""
secret_key = ""
host = "https://cloud.langfuse.com"
```

启用 Langfuse Cloud：

```toml
[langfuse]
public_key = "pk-lf-..."
secret_key = "sk-lf-..."
host = "https://cloud.langfuse.com"
```

使用自部署实例时，将 `host` 改为实例根地址：

```toml
[langfuse]
public_key = "pk-lf-..."
secret_key = "sk-lf-..."
host = "https://langfuse.example.com"
```

| 字段 | 是否必填 | 说明 |
| --- | --- | --- |
| `langfuse.public_key` | 启用时必填 | Langfuse 项目公钥 |
| `langfuse.secret_key` | 启用时必填 | Langfuse 项目私钥 |
| `langfuse.host` | 否 | Langfuse 根地址；空值默认为 `https://cloud.langfuse.com` |

公钥和私钥必须同时填写。只填写其中一项时，MiniCode 会在启动阶段报错并退出。

MiniCode 使用 OTLP/HTTP 将 trace 发送到：

```text
{host}/api/public/otel/v1/traces
```

进程正常退出时会尝试在 10 秒内刷新缓冲的 trace。如果刷新失败，错误会写入标准错误。

## 4. 追踪的数据范围

启用 Langfuse 后，一轮用户任务会形成父级 turn span，并包含模型 generation 和工具 tool span。当前实现不对提示词、响应或工具载荷做脱敏和过滤。

上传内容包括：

- 用户的完整输入；
- 发给模型的完整 JSON 请求，包括系统 Prompt、对话历史和工具定义；
- 模型服务返回的完整原始响应；
- 模型名、响应 ID和输入、输出、总 token 数；
- 工具名称、调用 ID、完整参数和完整结果；
- 调用耗时、状态和错误信息。

这些内容可能包含源代码、文件内容、命令输出、路径、业务数据和其他敏感信息。请仅在以下条件都满足时启用：

1. Langfuse 项目或自部署实例可信；
2. 数据上传符合项目的隐私和合规要求；
3. 使用权限受控的专用项目密钥；
4. 已确认任务内容可以离开本机。

模型 API Key 不会作为 span 属性主动记录，但它仍保存在本地 TOML 文件中，应按密钥管理要求保护。

## 5. 完整示例

```toml
[model]
api_key = "sk-..."
base_url = "https://api.deepseek.com/v1"
name = "deepseek-chat"

[langfuse]
public_key = "pk-lf-..."
secret_key = "sk-lf-..."
host = "https://cloud.langfuse.com"
```

配置完成后，从包含 `apps/config/config.toml` 的项目根目录启动：

```bash
minicode
```

也可以直接提供第一轮任务：

```bash
minicode "分析这个项目并运行测试"
```

## 6. 配置校验与常见错误

### 找不到配置文件

典型提示：

```text
minicode: read config: ...
```

确认当前目录正确，并检查文件是否位于 `apps/config/config.toml`。注意，程序按启动目录查找，而不是按二进制位置查找。

### 缺少模型字段

典型提示：

```text
minicode: missing required config: model.api_key, model.base_url, model.name
```

补齐提示中列出的字段；仅包含空白字符也会被视为未配置。

### TOML 无法解析

典型提示：

```text
minicode: cannot parse apps/config/config.toml; check TOML syntax, field names and types
```

检查：

- 字符串是否闭合并使用引号；
- 字段名是否拼写正确；
- 字段值是否为字符串；
- 是否写入了实现不认识的字段。

解析器会拒绝未知字段，以便尽早发现拼写错误。错误信息不会回显原始配置行，避免意外暴露密钥。

### Langfuse 凭据不完整

典型提示：

```text
minicode: langfuse: public_key and secret_key must both be set
```

同时填写公钥和私钥，或者同时留空以关闭追踪。

### 看不到 trace

依次检查：

1. `public_key` 与 `secret_key` 是否属于同一个 Langfuse 项目；
2. `host` 是否为实例根地址，而不是已经包含 OTLP 路径的地址；
3. 当前网络是否能访问 `{host}/api/public/otel/v1/traces`；
4. 程序是否正常退出并完成 trace 刷新；
5. Langfuse 项目时间范围和筛选条件是否正确。

## 7. 相关实现

| 文件 | 职责 |
| --- | --- |
| [`apps/config/config.example.toml`](../apps/config/config.example.toml) | 可复制的配置模板 |
| [`apps/internal/config/config.go`](../apps/internal/config/config.go) | 固定路径读取、TOML 解析和必填字段校验 |
| [`apps/internal/telemetry/langfuse.go`](../apps/internal/telemetry/langfuse.go) | Langfuse OTLP 导出器、鉴权和关闭刷新 |
| [`apps/internal/provider/openai.go`](../apps/internal/provider/openai.go) | 模型 generation span、原始请求响应和 token 属性 |
| [`apps/internal/agent/session.go`](../apps/internal/agent/session.go) | turn 与 tool span，以及工具输入输出和错误属性 |
| [`apps/internal/cli/run.go`](../apps/internal/cli/run.go) | 加载配置、组装追踪客户端并在退出时刷新 |
