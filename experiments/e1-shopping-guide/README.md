# E1 导购服务 + 缓存一致性

最后更新：2026-10-01；Codex；来源 backend-cloud-labs@1770378 + 未提交修改。

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

覆盖边界：岗位 06 中一致性、接口设计、降级的实验；source 为内存而非持久数据库，推荐仅做候选库存/预算筛选。所有读会验证 source，牺牲缓存减负，不声称生产吞吐或完整导购排序。E2～E5 未做。Redis 崩溃不丢 source 写，但服务自身重启会丢内存商品数据。
