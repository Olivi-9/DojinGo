# DojinGo

中文 | [English](README.md)

自动从 EH/EX/NH 下载图片集并上传到 Telegraph 的 Bot。

## 更新内容

- Bot 现在直接通过 Go 的 `net/http` 与支持的网站通信。
- 缓存仅限本地：内存或文件系统支持。
- 可选的入站代理服务内置在 Bot 进程中。

## 支持源

- `e-hentai`
- `exhentai`
- `nhentai`

`pixiv` 目前只保留配置入口，暂未实现采集器。

## 命令

- `/start`
- `/help`
- `/sync <url>`
- `/id`
- `/version`
- `/cancel`
- `/delete <cache-key>` 仅管理员

包含支持的图库 URL 的消息或说明文字也会自动同步

## 快速开始

1. 复制 [config_example.yaml](config_example.yaml) 为 `config.yaml`。
2. 填写 Telegram Bot Token 和至少一个 Telegraph Token。
3. 如需 `exhentai`，在 `collectors.exhentai` 下添加必需的 Cookie。
4. 使用 Docker 启动：

```bash
docker compose up -d --build
```

或本地运行：

```bash
go run ./cmd/ehbot -config ./config.yaml
```

## 配置

示例：

```yaml
bot:
  token: "YOUR_BOT_TOKEN"
  admins: [123456789]

telegraph:
  tokens: ["YOUR_TELEGRAPH_TOKEN"]
  author_name: "Author"
  author_url: "https://example.com"

ipv6:
  prefix: ""

storage:
  type: "memory"
  path: "./cache"
  ttl: 3888000
  max_entries: 1024

proxy:
  listen:
    http: "127.0.0.1:8080"
    socks5: "127.0.0.1:1080"
  auth:
    enabled: false
    username: ""
    password: ""
  rate_limit_per_minute: 120

collectors:
  exhentai:
    ipb_pass_hash: ""
    ipb_member_id: ""
    igneous: ""
  pixiv:
    session: ""

whitelist:
  enabled: false
  ids: [123456789]
```

说明：

- `storage.type` 支持 `memory` 和 `file`。
- `storage.path` 仅由文件存储使用。
- `ipv6.prefix` 可以是较大的 IPv6 CIDR，例如 `2001:db8::/64`，用于轮换本地源地址。
- 如果 `proxy.listen.http` 和 `proxy.listen.socks5` 为空，则内置代理服务器保持禁用状态。


## IPv6 轮换

如果你拥有可路由的 IPv6 前缀并想要按连接源地址轮换：

1. 在本地绑定该前缀。
2. 启用 `net.ipv6.ip_nonlocal_bind=1`。
3. 在配置中设置 `ipv6.prefix`。

这是可选的。留空 `ipv6.prefix` 使用正常的本地网络。

## 开发

要求：

- Go `1.26`

有用的命令：

```bash
go test ./...
go build ./cmd/ehbot
```

## 部署说明

- 容器读取 `CONFIG_FILE`，默认为 `config.yaml`。
- 基于文件的缓存需要可写目录。
- 内置代理服务器和 Telegram Bot 在同一进程中运行。

# 项目来源与改动说明
本项目基于 [eh2telegraph](https://github.com/qini7-sese/eh2telegraph) 进行重新实现，采用 Go 语言构建。在原有功能基础上，对代理配置与存储机制进行了调整。
