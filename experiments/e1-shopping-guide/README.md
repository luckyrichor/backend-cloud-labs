# E1 导购服务与缓存一致性

最后更新：2026-10-09；Codex；原创建来源 backend-cloud-labs@1770378；当前来源统一见仓库根目录 docs/measurements/2026-10-09-fifth-review.json。

内存source赋予每商品单调版本；缓存独立于source，内存/Redis拒绝延迟旧版本回填。Item统一snake_case；Redis Lua用十进制字符串比较避免大整数精度丢失。服务源数据不持久化，重启丢失商品。

## 运行与接口

```bash
# 本实验目录
go test ./... -race
go vet ./...
go run ./cmd/guide
CACHE_READ_MODE=cache-first go run ./cmd/guide
REDIS_ADDR=127.0.0.1:56379 go run ./cmd/guide
```

实际Redis集成须设置E1_REDIS_TEST_ADDR，不设置明确skip；根目录scripts/check.sh在TX创建独占临时Redis并完整验收。API契约见[docs/api.md](docs/api.md)。默认strict读取source核对版本；cache-first命中返回cache_unvalidated以降低源负载。

推荐先去重，最多8个并发Get，库存>0、价格<=预算过滤，价格升序、同价ID字典序。工作协程全部退出后才返回；按去重后的输入顺序选择第一个业务错误。HTTP候选最多100，不是全局数据库并发限制。

## 失败修复与缓存代际

使用NewService或共享Repairs；HTTP Handler默认创建1024容量队列，cache-first漏配队列显式报ErrRepairQueueRequired（写入前拒绝）。源提交后缓存失败仍返回写成功与degraded=true。

每个队列拥有随机source-lifetime命名空间与单调generation；每次请求在source操作前捕获不可变缓存视图。1024个标记溢出时换代，使旧缓存/旧请求回填不可被新请求访问，并清空修复标记、恢复cache-first。跨代完成的源写即使填旧缓存成功，也登记当前代修复；旧代读不能清除新代标记。新队列/source生命周期不复用旧Redis高版本，避免重启后的版本重置冲突。业务JSON仍返回原商品ID，物理缓存ID含私有命名空间。

标记有版本栅栏，成功后续读/写可清除；空队列原子快路径跳过队列锁。内存缓存读取用RWMutex；持续填充期间定期回收过期条目，旧Redis命名空间靠TTL清理，不扫描/删除其他实例的缓存。

**代价与范围**：换代冷启动、旧代存储存活到TTL/回收；队列限定本进程同一source/cache共享调用，不提供多实例失效、持久Outbox或服务重启后的源恢复。请求开始到写失败标记发布之间仍有并发窗口，cache-first不能用于要求线性一致的价格/库存决策。不存在简单“一个TTL后删标记”的安全保证。

## 可复算测量

完整Get路径在同一源码切换fast=false/true、rw=false/true，严格和cache-first分别比较；RunParallel共享一个服务，GOMAXPROCS1/4/8，每配置3次、200ms。原始输出2026-10-09-e1-service-comparison.txt；包含代际视图/键处理、内存源/缓存锁，不含HTTP、Redis或真实数据库延迟。中位数与局限见[现状与边界](../../docs/status.md)。

```bash
go test ./internal/guide -run '^$' -bench '^BenchmarkServiceReadComparison$' -benchmem -cpu 1,4,8 -benchtime=200ms -count=3
```

历史179.6/135.2、207.3/158.9、队列隔离30.16/1.247属于以前源码和不同测量口径，各原始CSV/JSON保留，不能当本轮整体前后对照。本轮对照只改变两个不可变开关，代际协议在所有组中都启用。
