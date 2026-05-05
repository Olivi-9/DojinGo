# DojinGo

中文 | [English](README.md)

自动从 e-hentai / exhentai / nhentai / pixiv 下载图片集并上传到 Telegraph 的 Bot。

## 功能特性

- 可以直接连接也可以使用出站代理（所有流量）
- 缓存仅限本地：内存或文件系统支持。

## 支持源

- `e-hentai`
- `exhentai`
- `nhentai`
- `pixiv`

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

或本地构建：

```bash
go build -o build/Dojingo ./cmd/Dojingo
```

运行：

```bash
./build/Dojingo -c config.yaml
```

## Systemd（systemctl）

示例服务文件：[systemd/dojingo.service](systemd/dojingo.service)。

将配置放到 `/etc/dojingo/config.yaml` 以匹配服务文件。请确保配置中的 `storage.path` 指向可写目录，例如 `/var/lib/dojingo/cache`。

编辑服务后启用：

将 [dojingo.service](systemd/dojingo.service) 文件复制到 `/etc/systemd/system/` 下，并编辑路径


```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dojingo
sudo systemctl status dojingo
```

日志：

```bash
sudo journalctl -u dojingo -f
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
  upstream:
    http: ""
    socks5: ""

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
- `proxy.upstream` 用于出站请求，可配置 `http(s)://` 或 `socks5://`（`host:port` 可省略协议）。
- 当配置了 `proxy.upstream` 时，所有出站流量（Telegram API、采集器、上传）都会走代理。


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
go build -o build/Dojingo ./cmd/Dojingo
```

## 部署说明

- 容器读取 `CONFIG_FILE`，默认为 `config.yaml`。
- 基于文件的缓存需要可写目录。

# 项目来源与改动说明
本项目基于 [eh2telegraph](https://github.com/qini7-sese/eh2telegraph) 进行重新实现，采用 Go 语言构建。在原有功能基础上，对代理配置与存储机制进行了调整。
