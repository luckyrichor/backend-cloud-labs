# E1 导购服务 + 缓存一致性

最后更新：2026-10-08；Codex；原创建来源 backend-cloud-labs@1770378；第二轮修订基线 97eb85c + 工作树。

W3 E1 自动验收已通过：可运行 HTTP 导购最小服务、确定性旧读者缓存回填竞态复现、单调版本填充修复、缓存 outage/read/write/recovery 降级。本实验保留 W1 Item+Store 骨架，并增加独立缓存层。

```bash
go test ./... -race
go vet ./...
gofmt -l .
go run ./cmd/guide
# 可选真实 Redis（容器默认在 tx）
E1_REDIS_TEST_ADDR=127.0.0.1:56379 go test ./... -race -count=1
REDIS_ADDR=127.0.0.1:56379 go run ./cmd/guide
```

Redis 用例不配地址会显式 skip，本次验收配置临时 Redis 后实跑无 skip。接口见 [docs/api.md](docs/api.md)，取舍见仓库 docs/design-decisions.md。

竞态复现：读者先取 v1 暂停 → 写者存 v2 并更新缓存 → 旧读者覆盖缓存为 v1。测试先验证朴素 map 回填产生不一致，再验证内存/Redis PutIfNewer 拒绝旧版本。Redis Lua 将比较和写入原子执行；恢复期严格 source 版本校验处理缓存故障期间漏写。不是用 sleep 赌概率。

覆盖边界：岗位 06 中一致性、接口设计、降级的实验；source 为内存而非持久数据库，推荐按候选 ID 去重，库存>0、价格<=预算过滤；按价格升序、同价 ID 字典序排序。默认严格读会验证 source，可选 cache-first 用新鲜度换源读取负载；不声称生产吞吐或完整导购排序。E2～E4 独立实验已验收，E5 未做。Redis 崩溃不丢 source 写，但服务自身重启会丢内存商品数据。


## 双模式对照（2026-10-08）

`CACHE_READ_MODE=strict`（默认，包括未设置）保持每次验证 source 版本；`CACHE_READ_MODE=cache-first` 在缓存命中时直接返回 `source=cache_unvalidated`，不读取 source；缓存未命中/故障仍读源、故障标记 degraded。非法模式在启动时拒绝。两种写入都先提交 source、再原子更新缓存，缓存失败不伪装源写入失败。

**cache-first 不保证最新版本。** 丢失缓存更新时，恢复后可以命中旧值；`degraded=false` 只表示本次未遇到缓存错误，不代表数据已校验。到期/驱逐后读源修复，但延迟填充与重复故障会影响滞后时长，TTL 不是严格的新鲜度 SLA。价格/库存关键决策应使用 strict 或另行核对源数据。

确定性对照：缓存 v1 → 源提交 v2 但缓存故障 → 恢复读。strict 返回 v2、源读取1次；cache-first 返回 v1、源读取0次。令缓存过期后两者均刷新到 v2；缓存不可用时两者均降级到源。回归不靠 sleep 竞态。

```bash
CACHE_READ_MODE=cache-first go run ./cmd/guide
go test ./internal/guide -run '^$' -bench BenchmarkReadModes -benchmem -count=5
```

TX 5轮内存源+内存缓存热命中微测：strict 中位179.6 ns/op，cache-first 135.2 ns/op；源读取分别1/0次每请求，均0 B/op。包含 Go 锁与时间检查，没有真实数据库/Redis/HTTP延迟，不能推算生产吞吐收益。原始值见 ../../docs/measurements/2026-10-08-e1-read-modes.txt。
