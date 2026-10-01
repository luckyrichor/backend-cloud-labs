# E1 接口与一致性契约

最后更新：2026-10-01；Codex；backend-cloud-labs@1770378 + 未提交修改。

| 接口 | 输入 | 返回 |
|---|---|---|
| PUT /items/{id} | title、price_cent>=0、stock>=0 | item（含源版本）、source=store、degraded |
| GET /items/{id} | 路径 ID | item、source=store/cache_validated、degraded |
| POST /recommendations | ids（1..100）、budget_cent>=0 | 有库存且价格不超过预算的 Result 数组 |

JSON 未知字段、尾随 JSON、超过 1MB 或参数非法返回 400。不存在商品 GET 返回 404；source 故障返回 503（不返回旧缓存冒充成功）。推荐忽略不存在候选，源错误仍返回 503。服务只绑定 127.0.0.1:8086，无认证，供本地实验。

线性化点在 source Get/Put；并发请求可以观察重叠写之前的版本，但完成在写之后启动的读不会把恢复前旧缓存作为最新值。每次读均验证源版本，不宣称避开数据库读。写后缓存失败依然 HTTP 200 + degraded=true，源写入已经成功；PUT 没有幂等 key，不应把网络未知结果自动重试为 exactly-once。

缓存异常时 source 回退，恢复后旧版本自动修复；TTL 1 分钟，Redis 网络操作 200ms 超时，无自动重试。多个实例的 source 目前不同（内存实现），所以不声称多实例一致性。
