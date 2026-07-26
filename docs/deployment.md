# 部署、网络与数据恢复

本文提供完整的 Home-Cloud 部署和维护步骤。第一次使用可以先阅读根目录的 [README](../README.md)，遇到网络、权限或数据恢复问题时再回到这里。

## 部署拓扑

推荐链路如下：

```text
Koishi / 其他客户端
        │ EasyTier
        ▼
AI Image Gateway
        │ Docker 共享网络
        ▼
      newapi

AI Image Gateway
        │ Docker bridge → 宿主机路由
        ▼
 mihomo TUN / 公网图片站
```

网关保持普通 Docker bridge 网络，不需要 `network_mode: host`、`privileged`、`NET_ADMIN` 或宿主机 TUN 权限。

## 部署前提

- Docker Engine 与 Compose 插件可用。
- newapi 已运行，并能加入一个外部 Docker 网络。
- EasyTier 客户端能访问 Home-Cloud 宿主机的数据端口。
- Docker bridge 的公网出站能被宿主机路由和 mihomo TUN 正常处理。
- 宿主机持久化目录可由 UID/GID `10001:10001` 写入。

## 配置说明

先复制配置模板：

```bash
cp .env.example .env
```

主要配置：

| 变量                    | 作用                                 | 示例                                     |
| ----------------------- | ------------------------------------ | ---------------------------------------- |
| `NEWAPI_BASE_URL`       | Docker 网络中的固定 newapi 地址      | `http://newapi:3000`                     |
| `DATA_PORT`             | 数据 API 的监听与 Docker 发布端口    | `15880`                                  |
| `ADMIN_PORT`            | 管理页面的监听与 Docker 发布端口     | `15881`                                  |
| `PUBLIC_IMAGE_BASE_URL` | 返回给 Koishi 的图片基础地址         | `http://10.0.0.1:15880/_gateway/images/` |
| `EASYTIER_BIND_IP`      | 数据端口绑定的宿主机地址             | `0.0.0.0`                                |
| `LAN_BIND_IP`           | 管理端口绑定的宿主机地址             | `0.0.0.0`                                |
| `DATA_ALLOWED_CLIENTS`  | 数据端口允许的客户端 IP/CIDR 列表    | `192.168.1.21,10.20.30.0/24`             |
| `ADMIN_ALLOWED_CLIENTS` | 管理端口允许的客户端 IP/CIDR 列表    | `192.168.1.21,10.20.30.40`               |
| `NEWAPI_DOCKER_NETWORK` | 与 newapi 共用的外部 Docker 网络     | `newapi`                                 |
| `ADMIN_USERNAME`        | 首次创建的管理员用户名               | `admin`                                  |
| `ADMIN_PASSWORD`        | 首次创建的管理员密码，至少 12 字符   | 使用强密码                               |
| `COOKIE_SECURE`         | 管理页面是否只通过 HTTPS 发送 Cookie | HTTP 用 `false`，HTTPS 用 `true`         |
| `MIN_FREE_BYTES`        | 调用 newapi 前要求的最小可用空间     | 默认 2 GiB                               |

`DATA_PORT` 与 `ADMIN_PORT` 也适用于直接运行程序的场景。修改 `DATA_PORT` 后，必须同步修改 `PUBLIC_IMAGE_BASE_URL`；该地址应以 `/_gateway/images/` 结尾，而且必须能从 Koishi 所在网络访问。

Compose 默认将两个端口绑定到所有 IPv4 接口。`DATA_ALLOWED_CLIENTS` 和 `ADMIN_ALLOWED_CLIENTS` 使用逗号分隔，支持单个 IPv4/IPv6 地址与 CIDR。未配置白名单时只允许 `127.0.0.0/8` 和 `::1`；这两个回环范围始终允许，以保证容器健康检查可用。任何无效或空的列表项都会让服务拒绝启动。

白名单只检查直接 TCP 对端 `RemoteAddr`，不会读取 `X-Forwarded-For` 或 `X-Real-IP`。它在 HTTP 层返回 `403 Forbidden`，不代替宿主机防火墙。不要将端口绑定到公网可达接口，除非部署环境另有可靠的网络层保护。

## 创建共享网络

```bash
docker network create newapi
```

若 newapi 已经在运行：

```bash
docker network connect newapi <newapi-container-name>
```

网络名必须和 `NEWAPI_DOCKER_NETWORK` 一致。`NEWAPI_BASE_URL` 中的主机名也必须是该网络中可解析的容器名或网络别名。

## 准备数据目录

```bash
mkdir -p data
sudo chown -R 10001:10001 data
chmod 750 data
```

服务会在 `/data` 下创建：

```text
database/  SQLite 数据库与 WAL
images/    已保存的图片
tmp/       下载中的 .part 文件
trash/     预留的恢复/删除目录
```

容器根文件系统只读，业务数据只写入 `/data`。如果看到 permission denied，优先检查宿主目录所有权是否为 `10001:10001`。

## 启动与检查

```bash
docker compose config
docker compose build
docker compose up -d
docker compose ps
docker compose logs --no-color image-gateway
```

1Panel 用户可以改用已经加入外部 `1panel-network` 的专用配置：

```bash
docker compose -f compose.1panel.yaml config
docker compose -f compose.1panel.yaml up -d --build
```

newapi 已在 `1panel-network` 时无需处理其他 Docker 网络。

健康检查（从白名单内的客户端执行，并将地址替换为宿主机实际可达 IP）：

```bash
curl -fsS "http://<HOST_REACHABLE_IP>:${DATA_PORT}/_gateway/health"
```

管理页面：

```text
http://<HOST_REACHABLE_IP>:<ADMIN_PORT>/
```

首次启动时，数据库中没有管理员才会使用环境变量创建管理员。修改 `.env` 不会重置已有管理员密码。修改任一白名单后需要重启容器。

## mihomo、Fake-IP 与 Docker bridge 验收

从最终容器中测试真实图片域名：

```bash
docker compose exec image-gateway wget -S -O /dev/null https://<真实图片域名>/
```

建议依次确认：

1. newapi 容器名能在共享网络解析，服务端口可访问。
2. 外部图片域名能解析；Fake-IP 模式下出现 `198.18.0.0/15` 地址属于正常情况之一。
3. HTTPS 请求成功，mihomo 日志能观察到来自 Docker bridge 的连接。
4. 从 Koishi 所在 VPS 通过 EasyTier 访问健康检查和实际图片。
5. 图片生成请求只抵达 newapi 一次；失败图片下载最多发起三次 GET。

如果 bridge 出站没有进入 TUN，应修复宿主机路由或防火墙。不要为了绕过网络问题给网关增加 host 网络、privileged 或 `NET_ADMIN`。

## 数据保存方式

- generations 保存请求 JSON、原始 newapi 响应、改写后响应、图片和下载记录。
- edits 只保存普通字段和上传文件的文件名、MIME、大小、SHA-256，不保存完整 multipart 或输入文件。
- `/v1/models` 不写请求历史。
- `Authorization` 不写入数据库，也不会发送给外部图片站。
- 图片默认永久保留，除非在管理页面手动删除。
- 图片先写到 `/data/tmp/*.part`，同步后原子重命名到 `/data/images`；服务启动时会清理遗留 `.part`。
- 图片下载失败时，原本成功的 API 响应仍会返回，但图片 URL 指向稳定的本地占位图。管理页面重试成功后，同一个 URL 会开始返回真实图片。

## 备份

SQLite 使用 WAL。服务运行时只复制 `gateway.db` 可能漏掉已经提交但尚未 checkpoint 的数据。

最简单的方式是停机备份数据库和图片目录：

```bash
docker compose stop image-gateway
tar -C data -czf "image-gateway-backup-$(date +%F-%H%M%S).tar.gz" database images
docker compose start image-gateway
```

如果不能停机，应使用与 SQLite 兼容的在线备份工具生成一致性数据库副本，再备份图片目录。数据库和图片目录应作为同一个备份集保存。

## 恢复

1. 停止网关。
2. 保留当前 `data/` 作为故障现场副本。
3. 恢复同一备份集中的数据库和图片。
4. 修正目录所有权。
5. 启动并检查健康状态、日志、请求记录和图片抽样。

```bash
docker compose stop image-gateway
mv data "data.failed.$(date +%s)"
mkdir data
tar -C data -xzf <backup.tar.gz>
sudo chown -R 10001:10001 data
docker compose up -d
docker compose logs --no-color image-gateway
```

当前版本会清理遗留 `.part`，但不会自动扫描所有孤儿最终文件，也不会自动修复数据库中 READY 但文件缺失的记录。崩溃恢复后建议抽样核对管理页面和 `data/images/`。

## 删除行为

管理员删除会先提交数据库删除，再尽力删除对应文件。如果文件删除失败，可能留下不再被数据库引用的文件。删除不可撤销，批量整理前应先备份。

当前没有自动 TTL、按容量淘汰、完整回收站状态机或 WebUI 一致性修复器。

## 常见问题

### 容器无法写入 `/data`

确认宿主目录所有权：

```bash
sudo chown -R 10001:10001 data
```

### Koishi 收到了 URL，但无法下载图片

按顺序检查：

- `PUBLIC_IMAGE_BASE_URL` 是否填写 EasyTier 可达地址。
- 数据端口是否绑定到包含 EasyTier 地址的宿主机接口；默认 `0.0.0.0` 覆盖所有 IPv4 接口。
- Koishi 的直接来源 IP 是否命中 `DATA_ALLOWED_CLIENTS`；不命中时服务返回 `403`。
- Home-Cloud 防火墙是否允许 EasyTier 网卡访问 `DATA_PORT`（默认 `15880`）。
- Koishi 所在机器能否直接请求健康检查。
- 返回的图片 ID 是否能在管理页面找到。

### 网关无法下载公网图片

- 从容器内运行 `wget` 测试目标域名。
- 检查 DNS 与 Fake-IP 结果。
- 检查宿主路由和 mihomo 日志。
- 检查目标站是否返回 PNG、JPEG、GIF 或 WebP，并确认未超过大小限制。

### 健康检查正常但生成请求返回 507

数据库可用并不代表磁盘空间充足。检查 `MIN_FREE_BYTES` 和数据盘剩余空间；网关会在请求 newapi 前执行低空间保护。
