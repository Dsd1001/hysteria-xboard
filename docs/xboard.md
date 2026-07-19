# Hysteria × Xboard 薄分叉

本分支基于官方 Hysteria，在 `extras/xboard` 增加 Xboard 控制面适配，并在 `app/cmd` 做少量装配。未修改 Hysteria wire protocol、QUIC、客户端或 `core/server` 通用接口。

## 默认兼容模式

默认 `apiMode: legacy` 直接兼容**未修改的 Xboard**，使用和 cedar 集成相同的原生 UniProxy 契约：

```text
GET  /api/v1/server/UniProxy/user
POST /api/v1/server/UniProxy/push
query: token=<server-token>&node_id=<id-or-code>&node_type=hysteria
```

用户响应：

```json
{"users":[{"id":1001,"uuid":"...","speed_limit":0,"device_limit":0}]}
```

流量请求体：

```json
{"1001":[123,456]}
```

其中数组顺序为 `[upload, download]`。成功响应必须为：

```json
{"data":true}
```

不需要修改 Xboard、执行 migration、安装插件或运行额外 scheduler。

## 已实现

- 定期从原生 Xboard UniProxy user API 同步用户；
- ETag/304、HTTP 超时、响应大小限制和默认强制 HTTPS；
- 鉴权热路径只读取原子内存快照，不访问 HTTP 或磁盘；
- 用户快照原子写入权限为 `0600` 的缓存文件；
- 面板故障时继续使用未过期的 last-known-good 快照；
- 停权用户拒绝新连接，已有活跃连接在后续流量事件中断开；
- `TrafficLogger` 热路径只做分片内存累加；
- 使用 bbolt 持久化 pending 流量和不可变本地 batch；
- 同一节点最多保留一个未确认 batch，其余流量聚合在 pending；
- 单 batch 最多 1000 个用户，超过 PHP `int64` 的单用户 delta 自动拆分；
- legacy 模式只有收到 HTTP 200 且 `{"data":true}` 才删除本地 batch；
- 官方 `trafficStats` 与 Xboard logger 可通过 `MultiTrafficLogger` 同时启用；
- SIGTERM 时停止后台循环，并将最后的内存流量写入 bbolt 后退出。

暂未实现：

- 自动读取并覆盖 Xboard 节点端口、证书域名、混淆和带宽配置；
- 用户级 `speed_limit` 数据面限速；
- 在线 IP/连接状态上报；
- HTTP health/readiness 端点；
- 空闲连接的瞬时主动踢线。

因此本地 `listen`、TLS 域名和混淆设置必须与 Xboard 节点配置一致。快速部署模板默认使用 UDP 443、ACME HTTP challenge 和无混淆。

## 服务端配置

参考 `examples/xboard/server.yaml` 或运行 `./scripts/xboard-init.sh`：

```yaml
auth:
  type: xboard

xboard:
  baseURL: https://xboard.example.com
  tokenFile: /run/secrets/xboard_token
  nodeID: "401"
  apiMode: legacy
  timeout: 8s
  users:
    cacheFile: /var/lib/hysteria/xboard-users.json
    pullInterval: 30s
    maxStale: 6h
  traffic:
    spoolFile: /var/lib/hysteria/xboard-traffic.db
    flushInterval: 1s
    batchInterval: 1m
    reportInterval: 5s
```

规则：

- `apiMode` 省略时默认为 `legacy`；
- `baseURL` 默认必须是 HTTPS origin，不能包含 userinfo、子路径、query 或 fragment；
- `token` 与 `tokenFile` 二选一，生产推荐 `tokenFile`；
- `nodeID` 可以是 Xboard 节点 ID 或 code，建议加引号保存为字符串；
- `cacheFile`、`spoolFile` 必须位于持久卷；
- 首次启动远程同步失败且没有有效缓存时，服务启动失败；
- 后续同步失败不会清空用户；超过 `maxStale` 后拒绝新鉴权；
- `flushInterval` 是强制断电/进程崩溃时的最大内存计费窗口，默认 1 秒。

### 为什么 legacy token 在 query 中

未修改 Xboard 的 `server` middleware 从请求参数读取 `token`，不能只用 Authorization Header。客户端会使用 `url.Values` 正确转义 token，错误信息不会输出请求 URL；生产必须使用 HTTPS，并建议让反向代理 access log 不记录 query string。

### 可选 ledger 模式

代码仍保留 `apiMode: ledger`，供已经实现 `/api/v2/server/report` batch ACK 契约的面板使用。它不是默认模式，也不是使用本项目的前置条件。未修改的 Xboard 必须使用 `legacy`。

## 计费可靠性边界

本地 bbolt 可以避免正常重启和大部分面板故障造成的流量丢失，但原生 Xboard push API 没有 `batch_id` 或幂等键：

- 收到明确 `{"data":true}`：删除本地 batch；
- HTTP 非 200、无效 JSON 或 `data != true`：保留并重试；
- 请求在面板计费后丢失响应：节点无法判断是否已处理，重试可能重复计费。

这是“不修改面板”条件下无法从节点侧彻底消除的二义性。另有两个已知窗口：

- 内存 collector 到 bbolt 之间最多约一个 `flushInterval`；
- 官方 `core/server.Close` 没有等待全部 client handler 的显式 barrier，极端并发关闭可能产生尾部窗口。

## Docker 运行

```bash
./scripts/xboard-init.sh
docker compose pull
docker compose up -d
docker compose logs -f hysteria
```

默认镜像：

```text
ghcr.io/dsd1001/hysteria-xboard:latest
```

本地源码构建：

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

普通单 UDP 端口不需要 `NET_ADMIN`。只有使用端口范围和主机 nftables/iptables 重定向时才考虑增加该 capability。

## 更新和回滚

更新：

```bash
git pull --ff-only
docker compose pull
docker compose up -d
```

生产建议将 `HYSTERIA_IMAGE` 固定为明确 tag 或 digest，而不是长期跟随 `latest`。更新前备份 `/var/lib/hysteria`；回滚时恢复旧镜像，不要删除用户缓存或 `xboard-traffic.db`。单实例重启会断开现有 QUIC 连接，多节点应逐个更新。

## 官方同步

```bash
git fetch upstream --tags
git switch -c sync/app-vNEXT feature/xboard
git merge --no-ff <upstream-tag-or-commit>
```

同步后至少运行：

```bash
go test ./extras/xboard ./extras/trafficlogger
go test ./app/cmd
go vet ./extras/xboard ./extras/trafficlogger ./app/cmd
go build ./app
```
