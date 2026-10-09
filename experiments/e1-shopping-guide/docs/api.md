# E1 接口与一致性契约

最后更新：2026-10-09；Codex；原创建来源 backend-cloud-labs@1770378；本轮源码与测量来源见仓库根目录 docs/measurements/2026-10-09-fourth-review.json。

| 接口 | 输入 | 返回 |
|---|---|---|
| PUT /items/{id} | title、price_cent>=0、stock>=0 | item（含源版本）、source=store、degraded |
| GET /items/{id} | 路径 ID | item、source=store/cache_validated/cache_unvalidated、degraded |
| POST /recommendations | ids（1..100）、budget_cent>=0 | 候选ID去重，库存>0且价格<=预算；价格升序、同价ID字典序排序的 Result 数组 |

JSON 未知字段、尾随 JSON、超过 1MB 或参数非法返回 400。不存在商品 GET 返回 404；实际访问 source 时故障返回 503；cache-first 热命中不访问 source，因此不会探测其故障。推荐忽略不存在候选，源错误仍返回 503。服务只绑定 127.0.0.1:8086，无认证，供本地实验。

写入线性化点在 source Put。默认 strict（或未设置 CACHE_READ_MODE）每次读都访问 source 校验版本；并发读可以观察重叠写之前的版本，已完成的写之后开始的读不会把恢复前旧缓存作为最新值。cache-first 热命中返回 source=cache_unvalidated，不做源版本校验，可能读到未被共享本地队列跟踪的更新（例如其他实例写入）留下的旧值；不能沿用 strict 的新鲜度保证。写后缓存失败依然 HTTP 200 + degraded=true，源写入已经成功；PUT 没有幂等 key，不应把网络未知结果自动重试为 exactly-once。

缓存异常/未命中时 source 回退；strict 恢复读会修复旧缓存，cache-first 热命中可能持续旧值，通常到期/驱逐后再读源修复。degraded=false 仅表示本次未遇到缓存操作错误，不表示 cache_unvalidated 已校验；TTL不构成严格新鲜度SLA。TTL 1 分钟，Redis 网络操作 200ms 超时，无自动重试。多个实例的 source 目前不同（内存实现），所以不声称多实例一致性。


当前 Item 响应字段统一为 `id`、`title`、`price_cent`、`stock`、`version`、`updated_at`。2026-10-09 前大写字段的客户端须更新，这是可见响应契约变化；Redis缓存键增加v2命名空间，旧数据由TTL淘汰、不解码为新结构。


HTTP Handler 自动持有共享的本地修复队列。源写成功、缓存填充失败时记下版本；同进程后续读该key绕过未校验缓存，回源修复成功后再允许cache-first热命中。源写仍返回200+degraded=true。队列默认最多1024个key，溢出保守地关闭该队列的所有未校验读，直到重建该队列；没有遗忘待修复key后继续信任旧值。

该队列不是持久Outbox，也没有后台轮询：修复由后续读触发，进程重启、其他实例和绕过服务的源写不在跟踪范围，仍可能陈旧。Go调用方使用 `guide.NewService` 或显式共享 `Repairs`；直接构造且Repairs=nil时，cache-first的Get/Put明确返回ErrRepairQueueRequired，写入前拒绝，不会静默关闭补偿；strict仍可不配置队列。

空修复队列使用原子dirty状态跳过互斥锁；mark/clear在锁内同步地图与dirty，有标记或溢出时仍加锁。它不承诺与源提交并发发生的读具备线性一致性：读在写失败标记发布前可能已经选择缓存。标记不按TTL自动过期，溢出仍保守保持到进程结束；当前Cache接口不提供可证明的旧缓存最长寿命，且旧版本回填可能刷新TTL，不能简单定时删除安全屏障。
