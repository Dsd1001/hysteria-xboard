# Hysteria × Xboard 薄分叉

本分支基于官方 Hysteria `app/v2.10.0`，只在 `extras/xboard` 增加 Xboard 控制面适配，并在 `app/cmd` 做少量装配。未修改 Hysteria wire protocol、QUIC、客户端或 `core/server` 接口。

## 能力边界

已实现：

- 通过 Xboard `/api/v2/server/user` 定期同步用户；
- 鉴权热路径只读取原子内存快照；
- ETag/304、HTTP 超时、响应大小限制、HTTPS 默认强制；
- 使用 Bearer Header 传递 server token，避免凭据进入 URL/access log；
- 用户快照原子写入私有缓存文件（`0600`）；
- 面板故障时使用未过期的 last-known-good 快照；
- 停权用户拒绝新连接，已有活跃连接在下一次流量事件时返回断开信号；
- `TrafficLogger` 热路径只做分片内存累加；
- bbolt 持久化 pending 和不可变 traffic batch；
- 只有匹配的 `accepted` / `already_processed` 业务 ACK 才删除本地 batch；
- 官方 `trafficStats` 与 Xboard logger 可通过通用 `MultiTrafficLogger` 同时启用；
- SIGTERM 时停止后台循环，将最后内存流量写入持久化 batch 后退出。

暂未实现：

- 用户级 `speed_limit` 数据面限速；
- 空闲连接的瞬时主动踢线；
- 在线 IP/连接状态上报；
- HTTP health/readiness 端点。

## 面板前置条件

可靠重试依赖配套 Xboard 补丁。补丁为 `/api/v2/server/report` 增加：

- `batch_id` / `Idempotency-Key`；
- `(server_id, batch_id)` 唯一请求账本；
- 持久化原始 traffic payload；
- 每个 `component + chunk` 的事务内 step 账本；
- 未完成批次定时重放；
- 90 天已完成账本保留期；
- 严格拒绝负数、小数及非法用户 ID。

部署顺序必须是：

1. 部署 Xboard 面板补丁；
2. 执行 `php artisan migrate --force`；
3. 确认 queue worker 和 Laravel scheduler 正常运行；
4. 再升级 Hysteria 节点。

如果节点先升级而面板未升级，面板会返回旧的 `data: true`。节点会将其视为无效业务 ACK 并保留本地 batch，不会静默删除账单。

## 服务端配置

参考 `examples/xboard/server.yaml`。关键字段：

```yaml
auth:
  type: xboard

xboard:
  baseURL: https://xboard.example.com
  tokenFile: /run/secrets/xboard_token
  nodeID: "401"
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

- `baseURL` 默认必须是 HTTPS origin，不能包含 userinfo、子路径、query 或 fragment；
- `token` 与 `tokenFile` 二选一，生产推荐 `tokenFile`；
- `nodeID` 可以是 Xboard 节点 ID 或 code，必须加引号保存为字符串；
- `cacheFile`、`spoolFile` 必须位于持久卷；
- 首次启动时，远程同步失败且没有有效缓存会直接启动失败；
- 后续同步失败不会清空用户；超过 `maxStale` 后拒绝新鉴权；
- `flushInterval` 是强制断电/进程崩溃时的最大内存计费窗口。默认 1 秒；调小可降低窗口，但会增加磁盘 fsync 频率。

## 运行

原生命令：

```bash
hysteria server -c /etc/hysteria/server.yaml --disable-update-check
```

Docker Compose：

```bash
docker compose up -d
docker compose logs -f hysteria
```

普通单 UDP 端口不需要 `NET_ADMIN`。只有使用服务端 UDP 端口范围和主机 nftables/iptables 重定向时，才考虑：

```yaml
cap_add:
  - NET_ADMIN
```

## 更新和回滚

镜像必须使用明确版本，不要生产使用 `latest`：

```text
2.10.0-xboard.1
2.11.0-xboard.1
```

更新前确认：

- `/var/lib/hysteria` 已持久化；
- 面板 migration 已完成；
- 当前未确认 batch 数量受控；
- 已保留旧镜像 tag/digest。

单实例重启会断开现有 QUIC 连接。多节点应一次更新一个节点，并在 Xboard 中先隐藏/排空。

回滚 Hysteria 只需恢复旧镜像，不能删除 `xboard-traffic.db`。如果新旧版本涉及 spool schema 变化，应先备份整个 `/var/lib/hysteria`。

## 官方同步

```bash
git fetch upstream --tags
git switch -c sync/app-v2.11.0 feature/xboard
git merge --no-ff app/v2.11.0
```

冲突应主要局限于 `app/cmd/server.go`；`extras/xboard` 和 `app/cmd/server_xboard.go` 通常不会与上游冲突。同步后至少运行：

```bash
go test ./extras/xboard ./extras/trafficlogger
go test ./app/cmd
go build ./app
```
