# AI Image Gateway Middleware

一个为 Home-Cloud 场景准备的轻量图片网关，用来解决 AI 生成图片存放在外部图床、Koishi 或其他客户端无法稳定访问的问题。

网关会把 OpenAI 兼容的图片请求转发给固定的 newapi，下载响应中的图片并保存到本地，再把图片地址替换为 EasyTier 可访问的地址。整个服务由一个 Go 进程提供 API、本地图床和管理页面，数据保存在 SQLite 与本地目录中。

## 它能做什么

- 兼容 OpenAI 图片生成与编辑接口。
- 每次生成或编辑请求只提交给 newapi 一次，避免自动重试导致重复扣费。
- 自动下载 `data[*].url` 中的图片，原子保存后返回本地 URL。
- 图片下载最多尝试三次；仍然失败时返回本地占位图，并可在管理页面重试。
- 保存请求、响应、图片和下载记录，方便查看与排查。
- 提供带登录保护的轻量管理页面，无需额外前端服务。
- 适合 Docker bridge + mihomo TUN/Fake-IP + EasyTier 的网络环境。

## 支持的接口

| 方法           | 路径                     | 说明                               |
| -------------- | ------------------------ | ---------------------------------- |
| `GET`          | `/v1/models`             | 转发模型列表，不保存访问记录       |
| `POST`         | `/v1/images/generations` | 生成图片、保存并改写图片 URL       |
| `POST`         | `/v1/images/edits`       | 编辑图片，仅保存表单字段和文件摘要 |
| `GET` / `HEAD` | `/_gateway/images/{id}`  | 访问已经保存的图片                 |
| `GET`          | `/_gateway/health`       | 健康检查                           |

目前不支持 Chat、Responses、SSE、WebSocket、视频、音频和 Gemini 原生接口，也不会作为通用反向代理使用。

## 快速开始

### 1. 准备配置

```bash
cp .env.example .env
```

打开 `.env`，至少修改这些内容：

```dotenv
NEWAPI_BASE_URL=http://newapi:3000
DATA_PORT=15880
ADMIN_PORT=15881
PUBLIC_IMAGE_BASE_URL=http://<EASYTIER_IP>:15880/_gateway/images/
ADMIN_PASSWORD=<至少12字符的强密码>

EASYTIER_BIND_IP=0.0.0.0
LAN_BIND_IP=0.0.0.0
DATA_ALLOWED_CLIENTS=<数据端口允许的IP或CIDR，逗号分隔>
ADMIN_ALLOWED_CLIENTS=<管理端口允许的IP或CIDR，逗号分隔>
NEWAPI_DOCKER_NETWORK=newapi
```

`DATA_PORT` 和 `ADMIN_PORT` 同时决定程序监听端口与 Docker 发布端口，可以按需修改；直接运行程序时也会使用这两个端口。修改 `DATA_PORT` 后，请同步更新 `PUBLIC_IMAGE_BASE_URL`。该地址必须能被 Koishi 实际访问。

两个 Compose 默认把端口绑定到所有 IPv4 接口，再由 `DATA_ALLOWED_CLIENTS` 和 `ADMIN_ALLOWED_CLIENTS` 分别限制直接连接的客户端。白名单支持单个 IPv4/IPv6 地址和 CIDR，例如 `192.168.1.21,10.20.30.0/24`；未配置时只允许回环地址。配置非法会导致服务拒绝启动，`X-Forwarded-For` 和 `X-Real-IP` 不参与判断。

### 2. 准备数据目录

默认 Compose 使用仓库下的 `data/`：

```bash
mkdir -p data
sudo chown -R 10001:10001 data
sudo chmod 750 data
```

1Panel 专用 Compose 固定挂载 `/opt/image-gateway-middleware/data`。首次启动前必须显式创建该目录：

```bash
sudo install -d -o 10001 -g 10001 -m 0750 \
  /opt/image-gateway-middleware/data
sudo stat -c 'mode=%a uid=%u gid=%g path=%n' \
  /opt/image-gateway-middleware/data
```

预期结果包含 `mode=750 uid=10001 gid=10001`。无需在宿主机创建 UID/GID 10001 对应的用户或用户组。目录缺失时，1Panel Compose 会直接报错，不会自动创建一个所有权错误的目录。

### 3. 使用默认 Compose 启动

默认 Compose 不创建项目专用网络，只连接 newapi 所在的外部网络。首次部署时准备网络：

```bash
docker network create newapi
docker network connect newapi <newapi-container-name>
```

如果共享网络不是 `newapi`，请修改 `.env` 中的 `NEWAPI_DOCKER_NETWORK`。

```bash
docker compose config
docker compose up -d --build
docker compose ps
```

### 4. 使用 1Panel Compose 启动

`compose.1panel.yaml` 要求仓库位于 `/opt/image-gateway-middleware`，并将上一节准备好的绝对数据目录挂载到容器 `/data`。服务会直接加入 1Panel 已有的外部网络 `1panel-network`；newapi 已在该网络中时，无需创建或连接其他网络，也无需设置 `NEWAPI_DOCKER_NETWORK`。

可以在 1Panel 的 Compose 项目中选择该文件并重新构建、创建，或执行：

```bash
cd /opt/image-gateway-middleware
docker compose -f compose.1panel.yaml config
docker compose -f compose.1panel.yaml up -d --build --force-recreate
docker compose -f compose.1panel.yaml ps
```

请确保 `NEWAPI_BASE_URL` 中的主机名是 newapi 在 `1panel-network` 中可解析的容器名或网络别名。

启动后，白名单内的客户端可以通过宿主机实际可达地址访问：

- 数据 API：`http://<HOST_REACHABLE_IP>:<DATA_PORT>`（默认 `15880`）
- 管理页面：`http://<HOST_REACHABLE_IP>:<ADMIN_PORT>`（默认 `15881`）
- 健康检查：`http://<HOST_REACHABLE_IP>:<DATA_PORT>/_gateway/health`

首次启动会使用 `.env` 中的 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 创建管理员。已有管理员不会因环境变量变化而自动重置密码。

## 使用提示

- 在 `.env` 中填写实际客户端 IP/CIDR；管理端口通常应使用比数据端口更严格的白名单。
- 回环网段 `127.0.0.0/8` 和地址 `::1` 始终允许，以保证容器健康检查可用。
- 图片、请求记录和审计数据默认永久保留，可在管理页面手动删除。
- 磁盘空间低于配置阈值时，网关会在调用 newapi 之前拒绝新请求。
- 如果 Koishi 无法获取改写后的图片，请先检查客户端白名单、EasyTier 地址、Docker bridge 出站和 mihomo TUN 路由。

## 更多文档

- [部署、网络与数据恢复](docs/deployment.md)

## 当前状态

核心功能已实现并通过单元测试、集成测试、静态检查与关键包竞态检查。完整网络链路（Docker、mihomo、EasyTier、Koishi）的验收请参照部署文档。

## License

MIT
