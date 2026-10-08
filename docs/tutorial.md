# MiniCode 使用教程

## 1. 准备配置

MiniCode 从启动目录下的 `apps/config/config.toml` 读取模型配置：

```bash
cp apps/config/config.example.toml apps/config/config.toml
```

编辑 `apps/config/config.toml`，填写模型服务信息：

```toml
[model]
api_key = "你的 API Key"
base_url = "https://api.deepseek.com/v1"
name = "deepseek-chat"
```

## 2. 启动 MiniCode

进入需要操作的项目目录，然后运行：

```bash
minicode
```

也可以直接在命令行中提交任务：

```bash
minicode "分析这个项目"
```

交互模式中可连续输入任务。使用 `/exit`、`/quit` 或 EOF 退出。

## 3. 自动批准工具调用：`--yes`

默认情况下，MiniCode 在执行 `bash`、`write` 和 `edit` 等有副作用的工具前会请求确认。

使用 `--yes` 可以自动批准这些工具调用：

```bash
minicode --yes "运行测试并修复问题"
```

`--yes` 会允许模型执行命令和修改文件，请只在可信项目和明确任务中使用。

## 4. 保存和恢复会话：`--session`

使用 `--session` 指定 JSON 文件后，MiniCode 会保存对话、模型回复、工具调用和工具结果：

```bash
minicode --session project.session.json "分析项目结构"
```

下次在同一个工作区使用相同文件，即可继续此前的会话：

```bash
minicode --session project.session.json "继续刚才的任务"
```

也可以集中保存到目录中：

```bash
mkdir -p .minicode/sessions
minicode --session .minicode/sessions/project.session.json
```

Session 文件可能包含源码、命令输出和其他敏感内容，请勿提交到 Git。仓库已忽略 `.minicode/` 和 `*.session.json`。

## 5. 设置任务超时：`--timeout`

每轮任务默认最多运行 1 小时。可以通过 `--timeout` 调整：

```bash
minicode --timeout 30m "运行完整测试"
```

常用时间格式：

```text
30s  30 秒
10m  10 分钟
1h   1 小时
```

## 6. 组合使用

自动批准工具、保存会话并设置超时：

```bash
minicode --yes --session project.session.json --timeout 30m "检查项目并修复测试"
```

之后继续该会话：

```bash
minicode --yes --session project.session.json "继续处理剩余问题"
```

## 7. 查看帮助

```bash
minicode -h
```
