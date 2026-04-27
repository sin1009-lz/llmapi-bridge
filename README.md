# API Bridge 1.0
（本项目完全通过deepseekv4构建）

Go 语言编写的 AI API 统一网关，对外提供单一 OpenAI 兼容接口，对内路由转接到多个上游 AI 提供商（DeepSeek、智谱 ChatGLM、月之暗面 Kimi、本地 llama.cpp 等）。

***

## 转接原理

```
客户端 → 认证(Account) → 模型映射(Mapper) → 提供商定位(Provider) → 请求改写 → 上游转发 → 响应反向映射 → 客户端
```

1. **认证** — 从 `Authorization` / `X-API-Key` 提取 token，匹配 Account，注入请求上下文
2. **模型映射** — 客户端传别名 → 查找 `ModelMapping` → 还原真实模型名；校验是否在 `EnabledModels` 白名单
3. **提供商定位** — 根据 Model 的 `ProviderID` 找到 Provider（BaseURL + API Keys）
4. **请求改写** — 替换请求体中模型名为真实名，注入 thinking 配置
5. **上游转发** — 轮询 Provider 的多个 API Key 实现负载均衡，透传请求到上游
6. **响应反向映射** — 将上游返回的真实模型名替换回客户端别名，流式 SSE 实时替换

**三层映射关系**：`Account → Model → Provider`，客户端完全无感知后端。

***

## 功能特性

- **统一入口** — OpenAI 兼容的 `/v1/chat/completions`、`/v1/models`、`/v1/embeddings` 等端点
- **账号权限** — 多账号，每账号独立配置可访问模型白名单与别名映射
- **模型别名** — 将上游真实模型名映射为自定义名称，客户端只看到被授权的模型
- **Thinking 控制** — 支持为模型配置思考模式（`-think`、`-high`、`-max`），自动注入推理参数
- **多 Key 轮询** — 每个 Provider 支持多个 API Key，并发安全轮询，实现负载分担
- **流式 SSE** — 完整支持流式响应，逐行解析、实时模型名反向替换，内存友好
- **模型自动发现** — 定时拉取各 Provider 模型列表，自动按类别分类
- **刷新不阻塞** — 模型刷新期间 API 请求零影响，I/O 在锁外异步完成
- **局域网扫描** — 自动探测内网 llama.cpp 服务器，一键添加
- **内嵌管理面板** — Web UI 可视化管理账号、供应商、模型、Thinking 配置
- **多模态** — 聊天补全、嵌入、图片生成、语音转写/合成

***

## 快速开始

### 编译运行

```bash
go build -o api-bridge.exe ./cmd/bridge/
./api-bridge.exe
```

默认监听 `:8081`（API）和 `:8082`（Web 管理面板），管理密钥为 `sk-admin-default`。

### 配置

编辑 `config.yaml`：

```yaml
server:
  listen_addr: ":8081"
  web_listen_addr: ":8082"
  admin_key: "sk-admin-default"

storage:
  data_dir: "./data"
  model_refresh_sec: 3600

providers:
  - name: "DeepSeek"
    type: "openai"
    base_url: "https://api.deepseek.com"
    api_keys:
      - "sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    enabled: true
```

### 通过 Web 管理面板使用

打开浏览器访问 `http://localhost:8082`，进入管理面板。所有操作都在 Web UI 中完成，无需命令行交互：

#### 添加 AI 提供商

点击左侧导航 **供应商** → 点击 **添加供应商** 按钮 → 填写表单：

| 字段       | 说明                                            |
| -------- | --------------------------------------------- |
| 名称       | 自定义标识，如 "DeepSeek"                            |
| 类型       | 通常选择 `openai`（OpenAI 兼容 API），本地部署选 `llamacpp` |
| Base URL | 上游 API 地址，如 `https://api.deepseek.com`        |
| API Keys | 粘贴上游 API 密钥，支持多个分行填写以实现轮询                     |
| 启用       | 勾选后立即生效                                       |

保存后模型列表会自动拉取，也可以手动点击供应商行的 **刷新** 按钮触发。

#### 创建账号

点击左侧导航 **账号** → **添加账号** → 填写账号名称 → 勾选该账号可访问的模型 → 可选填写模型别名映射。

创建成功后系统会生成唯一的 `account_key`（形如 `sk-bridge-xxx`），将此 key 提供给客户端用户即可。

#### 配置模型别名

在账号编辑界面中设置 **模型映射**：

```
真实模型名          →  别名
deepseek-v4-flash   →  gpt-fast
deepseek-v4-pro     →  gpt-pro
```

客户端使用别名发起请求，桥接层自动替换为真实模型名转发到上游。

#### 配置 Thinking 模式

点击左侧导航 **思维链** → 找到目标模型 → 选择模式：

| 模式                 | 效果                               | 客户端可用模型名    |
| ------------------ | -------------------------------- | ----------- |
| 默认（无）              | 正常请求                             | `模型名`       |
| 启用（enabled）        | 注入 `thinking: {type: "enabled"}` | `模型名-think` |
| 高（reasoning\_high） | 注入 `reasoning_effort: "high"`    | `模型名-high`  |
| 最大（reasoning\_max） | 注入 `reasoning_effort: "max"`     | `模型名-max`   |

配置后带后缀的模型变体会自动出现在模型列表中，客户端可直接调用。

***

## 项目结构

```
.
├── cmd/bridge/main.go        # 程序入口
├── internal/
│   ├── account/              # 账号数据结构、密钥生成、CRUD
│   ├── config/               # YAML 配置加载
│   ├── middleware/            # 认证、CORS、日志中间件
│   ├── model/                # 模型定义、定时拉取、别名映射
│   ├── provider/             # 提供商定义、多 Key 轮询、注册中心
│   ├── proxy/                # 核心代理逻辑、SSE 流式解析
│   ├── router/               # Chi 路由 + 所有 Handler
│   ├── scanner/              # 局域网 llama.cpp 扫描
│   ├── store/                # JSON 文件存储（RWMutex 并发安全）
│   └── webui/                # 内嵌 Web 管理面板
├── data/store.json           # 运行时数据
├── config.yaml               # 配置文件
├── go.mod / go.sum           # Go 依赖
└── README.md
```

***

## API 文档

### 公开 API（账号 Key 认证）

| 端点                         | 方法   | 说明                     |
| -------------------------- | ---- | ---------------------- |
| `/v1/models`               | GET  | 列出当前账号可访问的模型（含别名）      |
| `/v1/chat/completions`     | POST | 聊天补全，支持 `stream: true` |
| `/v1/completions`          | POST | 文本补全                   |
| `/v1/embeddings`           | POST | 向量嵌入                   |
| `/v1/images/generations`   | POST | 图片生成                   |
| `/v1/audio/transcriptions` | POST | 语音转文字                  |
| `/v1/audio/speech`         | POST | 文字转语音                  |

### 管理 API（admin\_key 认证）

| 端点                              | 方法           | 说明                  |
| ------------------------------- | ------------ | ------------------- |
| `/`                             | GET          | Web 管理面板            |
| `/admin/accounts`               | GET / POST   | 列出 / 创建账号           |
| `/admin/accounts/{id}`          | DELETE       | 删除账号                |
| `/admin/accounts/{id}/models`   | PUT          | 更新账号模型权限与别名映射       |
| `/admin/providers`              | GET / POST   | 列出 / 添加提供商          |
| `/admin/providers/{id}`         | PUT / DELETE | 更新 / 删除提供商          |
| `/admin/providers/{id}/refresh` | POST         | 手动刷新模型列表            |
| `/admin/models`                 | GET          | 查看所有已知模型            |
| `/admin/models/thinking`        | GET          | 查看所有模型的 Thinking 配置 |
| `/admin/models/{id}/thinking`   | PUT          | 设置模型 Thinking 模式    |
| `/admin/scanner/llamacpp`       | GET          | 扫描局域网 llama.cpp 服务器 |

***

## 性能

- **并发模型**：Go goroutine + RWMutex 读写分离
- **200 并发**：流式/非流式均 100% 成功，延迟主要来自上游 LLM 推理
- **代理开销**：桥接层额外延迟在微秒级，淹没在上游秒级波动中，不可测
- **刷新不阻塞**：模型刷新期间 I/O 在锁外异步完成，API 请求零影响

***

## 技术栈

- **语言**：Go 1.26+
- **HTTP 路由**：[go-chi/chi/v5](https://github.com/go-chi/chi/v5)
- **配置**：YAML
- **存储**：本地 JSON 文件（RWMutex 并发安全，支持热加载）
- **管理面板**：纯 HTML + CSS + JavaScript，无前端框架依赖

***

## AI 自动维护与测试

详见 [SKILL.md](SKILL.md)（机器可读格式，含完整架构、数据模型、API 参考、调试指南、测试矩阵、修改规则、部署命令）。

***

## 许可证

MIT
