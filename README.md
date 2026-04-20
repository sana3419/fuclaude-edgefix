# fuclaude-edgefix

Fuclaude v0.5.1 的 `/edge-api/bootstrap` 修复补丁。

Claude 官方将 `/api/bootstrap` 迁移到 `/edge-api/bootstrap`，导致 fuclaude v0.5.1 响应处理崩溃返回 500。本项目通过前置代理将 `/edge-api/*` 重写为 `/api/*`，同时改写 JS 响应中的路径引用，修复此问题。

## 原理

```
浏览器 → edgefix (:8181) → fuclaude (:8182) → claude.ai
```

- **路径重写**：`/edge-api/*` → `/api/*`
- **JS 改写**：替换前端 JS 中的 `edge-api` 引用为 `api`

## 使用方法

### 1. 修改 fuclaude 的 config.json

```json
"bind": "127.0.0.1:8182"
```

### 2. 部署 edgefix

```bash
# 下载 edgefix 二进制（ARM64）或自行编译
cd src/edgefix
go build -o ../../edgefix .

# 创建 systemd service
cat > /etc/systemd/system/edgefix.service << 'EOF'
[Unit]
Description=Fuclaude Edge-API Fix Proxy
After=network.target fuclaude.service
Requires=fuclaude.service

[Service]
Type=simple
WorkingDirectory=/path/to/fuclaude
ExecStart=/path/to/fuclaude/edgefix
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable edgefix
systemctl restart fuclaude
systemctl start edgefix
```

### 3. 验证

```bash
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8181/edge-api/bootstrap
# 应返回 302（不再是 500）
```

## 回滚

```bash
# 还原 config.json bind 为 "0.0.0.0:8181"
systemctl stop edgefix && systemctl disable edgefix && systemctl restart fuclaude
```

## 自行编译

```bash
cd src/edgefix
go build -o edgefix .
```

如需修改端口，编辑 `main.go` 中的 `upstream` 和 `listen` 变量。

## 致谢 · 友情链接

- 灵感来源：[fuclaude](https://github.com/wozulong/fuclaude) by 始皇
- 友好社区：[LINUX DO](https://linux.do/)
- 社区替代方案：[funclaude](https://github.com/aiporters/funclaude-deploy)

## License

MIT
