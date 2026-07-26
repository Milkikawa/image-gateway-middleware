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

EASYTIER_BIND_IP=<宿主机的EasyTier地址>
LAN_BIND_IP=<家庭局域网地址>
NEWAPI_DOCKER_NETWORK=newapi
```

`DATA_PORT` 和 `ADMIN_PORT` 同时决定程序监听端口与 Docker 发布端口，可以按需修改；直接运行程序时也会使用这两个端口。修改 `DATA_PORT` 后，请同步更新 `PUBLIC_IMAGE_BASE_URL`。该地址必须能被 Koishi 实际访问。

### 2. 准备数据目录

```bash
mkdir -p data
sudo chown -R 10001:10001 data
chmod 750 data
```

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

`compose.1panel.yaml` 会让服务直接加入 1Panel 已有的外部网络 `1panel-network`。newapi 已在该网络中时，无需创建或连接其他网络，也无需设置 `NEWAPI_DOCKER_NETWORK`。

可以在 1Panel 的 Compose 项目中选择该文件，或在项目目录执行：

```bash
docker compose -f compose.1panel.yaml config
docker compose -f compose.1panel.yaml up -d --build
docker compose -f compose.1panel.yaml ps
```

请确保 `NEWAPI_BASE_URL` 中的主机名是 newapi 在 `1panel-network` 中可解析的容器名或网络别名。

启动后可以访问：

- 数据 API：`http://<EASYTIER_BIND_IP>:<DATA_PORT>`（默认 `15880`）
- 管理页面：`http://<LAN_BIND_IP>:<ADMIN_PORT>`（默认 `15881`）
- 健康检查：`http://<EASYTIER_BIND_IP>:<DATA_PORT>/_gateway/health`

首次启动会使用 `.env` 中的 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 创建管理员。已有管理员不会因环境变量变化而自动重置密码。

## 使用提示

- 数据 API 建议只绑定 EasyTier 地址，管理页面只绑定可信的家庭局域网地址。
- 图片、请求记录和审计数据默认永久保留，可在管理页面手动删除。
- 磁盘空间低于配置阈值时，网关会在调用 newapi 之前拒绝新请求。
- 如果 Koishi 无法获取改写后的图片，请先检查 EasyTier 地址、Docker bridge 出站和 mihomo TUN 路由。

## 更多文档

- [部署、网络与数据恢复](docs/deployment.md)

## 当前状态

核心功能已实现并通过单元测试、集成测试、静态检查与关键包竞态检查。完整网络链路（Docker、mihomo、EasyTier、Koishi）的验收请参照部署文档。

## License

MIT
