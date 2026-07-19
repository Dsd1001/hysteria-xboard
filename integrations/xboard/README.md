# Xboard 后端配套补丁

Hysteria 节点的可靠流量重试依赖这里的 V2 batch-ledger 补丁：

```text
xboard-idempotent-traffic.patch
SHA256 d0ce067b2940479285abe44f67a46203d5093e8914ae168f7e74233c2854528b
```

## 适用基线

补丁经过以下 Xboard commit 验证：

```text
8e4864b4c7f6240e3ef08ecd7b59447e5d9dd363
```

补丁包含两个 commit：

```text
c3cb74c feat(server): add idempotent traffic batch ledger
1982248 fix(server): preserve batch tombstones across pruning
```

如果你的 Xboard 已明显偏离该基线，请先在测试分支执行 `git am --3way` 并运行测试，不要直接在生产目录尝试。

## 安装

先备份数据库和 Xboard 源码，然后在 Xboard 仓库执行：

```bash
git switch -c feature/hysteria-idempotent-traffic
git am --3way /path/to/hysteria-xboard/integrations/xboard/xboard-idempotent-traffic.patch
php artisan migrate --force
php artisan queue:restart
```

如果使用 Horizon：

```bash
php artisan horizon:terminate
```

确认以下进程正常：

```bash
php artisan schedule:list
php artisan queue:work
```

生产环境应由 systemd、Supervisor 或容器编排系统持续运行 queue worker/Horizon 和 scheduler，不能只在终端临时启动。

## 验证

```bash
vendor/bin/phpunit
php artisan migrate:status
```

然后再启动 Hysteria 节点：

```bash
cd /path/to/hysteria-xboard
docker compose up -d --build
docker compose logs -f hysteria
```

## 回滚注意

- 回滚 Hysteria 时保留 `deploy/xboard/data/xboard-traffic.db`；
- 不要直接删除 batch ledger 表；
- 完成 request 的 `(server_id, batch_id, payload_hash)` tombstone 用于阻止延迟重放重复计费；
- 如需回滚 migration，先停止 Hysteria reporter 和队列 worker，并完成数据库备份。
