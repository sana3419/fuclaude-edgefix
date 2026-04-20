# Fuclaude v0.5.1 逆向分析与 edge-api 修复方案

## 背景

最近 Claude 官方更新了前端，将 `/api/bootstrap` 迁移到了 `/edge-api/bootstrap`，导致 fuclaude v0.5.1 出现 "cannot reach claude" 错误，具体表现为浏览器控制台报 500：

```
edge-api/bootstrap?statsig_hashing_algorithm=djb2&growthbook_format=sdk&include_system_prompts=false
Failed to load resource: the server responded with a status of 500 (Internal Server Error)
```

始皇已停更，社区等不到修复，于是尝试自行逆向分析并修复。

## 逆向分析过程

### 二进制基本信息

| 项目 | 内容 |
|------|------|
| 文件 | `fuclaude` 57MB, ELF ARM64, 静态链接, stripped |
| 语言 | Go，使用 **garble** 混淆（符号名全部打乱） |
| 框架 | Gin (Web), fhttp (HTTP/2指纹), uTLS (TLS指纹) |
| 版本 | v0.5.1 (build 12114b0), 2026-02-25 |

### 架构还原

通过 `strings`、ELF section 分析、JSON tag 提取，还原了完整架构：

```
用户浏览器 → fuclaude (Gin) → HTTP CONNECT Proxy → claude.ai
                                   ↑
                          fhttp + uTLS Chrome指纹
```

**核心组件：**
- **Gin 路由** — `/api/*`(需认证), `/_/ping`(健康检查), `/v1/*`(OpenAI兼容API), NoRoute(代理)
- **uTLS** — 模拟 Chrome TLS 指纹绕过 Cloudflare
- **fhttp** — 保持 HTTP/2 header 顺序匹配浏览器（标准 Go net/http 会暴露指纹）
- **securecookie** — 加密 session cookie
- **嵌入资源** — 3 个 HTML 模板 + Soehne 字体 + Auth0 ULP CSS + SweetAlert2

### 故障定位

通过运行时测试（对比不同路径的响应），精确定位了问题：

```bash
/edge-api/bootstrap  → 500 ← 唯一崩溃的路径！
/edge-api/test       → 200 ✓
/edge-api/bootstra   → 200 ✓ (少一个字母就正常)
/api/bootstrap       → 302 ✓ (需认证，正常)
/random-path         → 200 ✓ (正常代理)
```

**根因：** fuclaude 的 NoRoute 处理器把 `/edge-api/bootstrap` 代理到 claude.ai 后，在处理**响应**时内部崩溃（Claude 的新 bootstrap 响应格式导致 fuclaude 的解析逻辑报错）。而 fuclaude 自身的 `/api/*` 路由处理器能正确处理 bootstrap 请求。

### 关键发现

1. **TLS 指纹伪装仍然有效** — 不是 Cloudflare 封了，是 API 路径变了
2. **问题不是路由缺失** — fuclaude 的 NoRoute 确实会代理 `/edge-api/*` 到上游
3. **是响应处理崩溃** — 只有 `/edge-api/bootstrap` 这一个精确路径触发 500
4. **域名被 garble 加密** — `claude.ai` 不以明文存在于二进制中，无法简单 patch

## 修复方案

由于二进制被 garble 混淆，直接 patch Go 逻辑不现实。采用**前置代理**方案：

```
浏览器 → edgefix (:8181) → fuclaude (:8182) → claude.ai
```

**edgefix** 是一个 ~8MB 的 Go 程序，两个功能：

1. **路径重写**：`/edge-api/*` → `/api/*`，让请求走 fuclaude 正常的认证+代理逻辑
2. **JS 响应改写**：拦截 Claude 前端 JS 中的 `edge-api` 引用替换为 `api`，让浏览器不再请求 `/edge-api/` 路径

### 部署方式

```bash
# config.json 修改 bind
"bind": "127.0.0.1:8182"   # fuclaude 改为仅本地监听

# edgefix 对外监听 8181（用户无感）
# systemd service: edgefix.service
```

用户端完全不需要改任何配置。

### 回滚方法

```bash
# 还原 config.json bind 为 "0.0.0.0:8181"
systemctl stop edgefix
systemctl disable edgefix
systemctl restart fuclaude
```

## 局限性

- 如果 Claude 后端**完全移除** `/api/bootstrap` 接口（目前仍可用），此方案失效
- 如果 Claude 继续修改其他 API 路径，需要在 edgefix 中追加重写规则
- 长期建议关注社区替代方案（如 [funclaude](https://github.com/aiporters/funclaude-deploy)）

## 技术细节备注

- garble 混淆下，Go 的 exported struct field 名和 JSON tag 仍保留（反射需要），这是逆向的主要信息来源
- fuclaude 用 `fhttp`（非标准 `net/http`）确保 HTTP/2 HEADERS 帧顺序匹配 Chrome，这是绕过 Cloudflare 的关键
- `targetAddr`、`targetScheme` 等字段名可在二进制中找到，但实际值被 garble 的 literal obfuscation 加密
- edgefix 无检测风险——它是纯本地组件，对 Claude 服务端来说只是看到正常的 `/api/bootstrap` 请求
