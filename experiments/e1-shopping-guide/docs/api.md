# E1 接口与一致性契约

最后更新：2026-10-08；Codex；原创建来源 backend-cloud-labs@1770378；本次按已提交实现 backend-cloud-labs@7277e74 更新接口契约。

| 接口 | 输入 | 返回 |
|---|---|---|
| PUT /items/{id} | title、price_cent>=0、stock>=0 | item（含源版本）、source=store、degraded |
| GET /items/{id} | 路径 ID | item、source=store/cache_validated/cache_unvalidated、degraded |
| POST /recommendations | ids（1..100）、budget_cent>=0 | 候选ID去重，库存>0且价格<=预算；价格升序、同价ID字典序排序的 Result 数组 |

JSON 未知字段、尾随 JSON、超过 1MB 或参数非法返回 400。不存在商品 GET 返回 404；实际访问 source 时故障返回 503；cache-first 热命中不访问 source，因此不会探测其故障。推荐忽略不存在候选，源错误仍返回 503。服务只绑定 127.0.0.1:8086，无认证，供本地实验。

写入线性化点在 source Put。默认 strict（或未设置 CACHE_READ_MODE）每次读都访问 source 校验版本；并发读可以观察重叠写之前的版本，已完成的写之后开始的读不会把恢复前旧缓存作为最新值。cache-first 热命中返回 source=cache_unvalidated，不做源版本校验，可能读到写入期间缓存更新失败留下的旧值；不能沿用 strict 的新鲜度保证。写后缓存失败依然 HTTP 200 + degraded=true，源写入已经成功；PUT 没有幂等 key，不应把网络未知结果自动重试为 exactly-once。

缓存异常/未命中时 source 回退；strict 恢复读会修复旧缓存，cache-first 热命中可能持续旧值，通常到期/驱逐后再读源修复。degraded=false 仅表示本次未遇到缓存操作错误，不表示 cache_unvalidated 已校验；TTL不构成严格新鲜度SLA。TTL 1 分钟，Redis 网络操作 200ms 超时，无自动重试。多个实例的 source 目前不同（内存实现），所以不声称多实例一致性。


当前 Item 未声明 JSON 字段标签，序列化字段为 `ID`、`Title`、`PriceCent`、`Stock`、`Version`、`UpdatedAt`，并非请求侧的 snake_case。后续统一标签属于可见响应契约变化，应同步调用方与接口测试。
