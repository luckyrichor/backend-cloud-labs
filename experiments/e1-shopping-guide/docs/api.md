# E1 接口与一致性契约

最后更新：2026-10-09；Codex；原创建来源 backend-cloud-labs@1770378；本轮源码与测量来源见仓库根目录 docs/measurements/2026-10-09-fifth-review.json。

| 接口 | 输入 | 返回 |
|---|---|---|
| PUT /items/{id} | title、price_cent>=0、stock>=0 | item（含源版本）、source=store、degraded |
| GET /items/{id} | 路径 ID | item、source=store/cache_validated/cache_unvalidated、degraded |
| POST /recommendations | ids（1..100）、budget_cent>=0 | 候选ID去重，库存>0且价格<=预算；价格升序、同价ID字典序排序的 Result 数组 |

JSON 未知字段、尾随 JSON、超过 1MB 或参数非法返回 400。不存在商品 GET 返回 404；实际访问 source 时故障返回 503；cache-first 热命中不访问 source，因此不会探测其故障。推荐忽略不存在候选，源错误仍返回 503。服务默认只绑定 127.0.0.1:8086，无认证，供本地实验。

写入线性化点在 source Put。默认 strict（或未设置 CACHE_READ_MODE）每次读都访问 source 校验版本；并发读可以观察重叠写之前的版本，已完成的写之后开始的读不会把恢复前旧缓存作为最新值。cache-first 热命中返回 source=cache_unvalidated，不做源版本校验，可能读到未被共享本地队列跟踪的更新（例如其他实例写入）留下的旧值；不能沿用 strict 的新鲜度保证。写后缓存失败依然 HTTP 200 + degraded=true，源写入已经成功；PUT 没有幂等 key，不应把网络未知结果自动重试为 exactly-once。

缓存异常/未命中时 source 回退；strict 恢复读会修复旧缓存，cache-first 热命中可能持续旧值，通常到期/驱逐后再读源修复。degraded=false 仅表示本次未遇到缓存操作错误，不表示 cache_unvalidated 已校验；TTL不构成严格新鲜度SLA。TTL 1 分钟，Redis 网络操作 200ms 超时，无自动重试。多个实例的 source 目前不同（内存实现），所以不声称多实例一致性。


当前 Item 响应字段统一为 `id`、`title`、`price_cent`、`stock`、`version`、`updated_at`。2026-10-09 前大写字段的客户端须更新，这是可见响应契约变化；Redis缓存键增加v2命名空间，旧数据由TTL淘汰、不解码为新结构。


HTTP Handler 自动持有共享的本地修复队列。源写成功、缓存填充失败时记下版本；同进程后续读该key绕过未校验缓存，回源修复成功后再允许cache-first热命中。源写仍返回200+degraded=true。队列默认最多1024个key，溢出轮换缓存代际，使旧条目不可达后再清空标记，恢复热命中。

该队列不是持久Outbox，也没有后台轮询：修复由后续读触发，进程重启、其他实例和绕过服务的源写不在跟踪范围，仍可能陈旧。Go调用方使用 `guide.NewService` 或显式共享 `Repairs`；直接构造且Repairs=nil时，cache-first的Get/Put明确返回ErrRepairQueueRequired，写入前拒绝，不会静默关闭补偿；strict仍可不配置队列。

空修复队列使用原子dirty状态跳过互斥锁；完整服务对比的fast开关在构造时固定，不在并发请求中修改。内存缓存默认读写锁并提供构造时rw开关作同源码比较。

每个共享Repairs拥有随机source生命周期命名空间。每次Get/Put捕获固定generation；队列1024容量溢出时换代、清空标记，旧请求只向旧代回填；跨代完成的源写重新标记当前代，旧读者无法清掉新标记。新的source/队列不会复用旧Redis高版本。此协议不需要知道旧条目何时过期，但旧条目仍占存储直到Redis TTL或内存过期回收。新代可能冷启动，没有跨实例同步或持久source保证。

推荐最多8个并发读，去重与结果排序不变；错误按去重后输入顺序选择，所有工作协程完成才返回。调用方取消通过同一个context传给每次Get；source/cache实现仍必须响应context。


## 2026-10-24 · MySQL 数据源与写令牌（P2-1，Claude Code）

- `GUIDE_SOURCE=memory|mysql`（默认 memory，行为不变）；`GUIDE_MYSQL_DSN` 指定库。选 mysql 时**必须**设置 `GUIDE_WRITE_TOKEN`，否则拒绝启动：共享的真实数据源不接受匿名写。
- 设置 `GUIDE_WRITE_TOKEN` 后，PUT 必须带 `Authorization: Bearer <令牌>`，缺失或不符返回 401 并带 `WWW-Authenticate: Bearer`；GET 与推荐保持开放（方案 B.1：E1 只对写接口做不记名共享令牌）。未设置令牌时与原契约一致。令牌按 SHA-256 摘要做常量时间比较。
- MySQL 源下 id 必须是规范的无符号十进制：PUT 非法 id 返回 400，GET 返回 404（与不存在一致）。内存源仍接受任意字符串 id。
- 写返回的 `version` 就是 MySQL 中已提交的版本，压测程序以此建立真值日志。PUT 新 id 时 category_id 为 0；更新时保留导入数据的类目。
- 线性化点仍是数据源写入；缓存失败返回 200 + degraded 的契约不变。

## 2026-10-24 · 四种读写策略与 S1 观测（P2-2，Claude Code）

`CACHE_READ_MODE` 取值（默认 strict，原有两种的行为不变）：

| 取值 | 方案代号 | 读 | 写 |
|---|---|---|---|
| `cache-aside` | A 基线 | 命中直接返回（cache_unvalidated）；未命中读源，**普通 SET** 回填 | 写源后**删除**缓存 |
| `versioned` | B 版本门控 | 同 A，但回填走“只接受更大版本”的门控 | 写源后用门控**更新**缓存 |
| `strict` | C 严格校验 | 每次先读源版本，相等才用缓存（cache_validated） | 写源后门控更新 |
| `cache-first` | D 缓存优先 | B 的读写，外加本地修复队列与缓存代际 | 同左 |

- B 与 D 只在缓存写失败时不同：D 会标记该 key，让后续读绕过旧条目；B 不标记，要等 TTL 过期。缓存不出错时二者应表现相近。
- `GUIDE_UNGATED_FILLS=1` 只对 versioned 生效：把门控回填换成普通 SET，用于 S1 反向验证；配在其他模式上会拒绝启动。
- cache-aside 与关闭门控的 versioned 需要缓存支持普通 Set/Delete（内存与 Redis 都支持）；缺少时源写照常提交，结果标 degraded。
- `GUIDE_CACHE_TTL` 设置缓存寿命，默认 1 分钟（方案 S1 基线）。

管理端口新增（仅管理端口，不在业务端口）：

- `/metrics` 增加计数器：`bcl_e1_source_reads_total`（S1 的“MySQL 读 QPS”由此求差得到）、`bcl_e1_source_writes_total`、`bcl_e1_source_errors_total`、`bcl_e1_cache_hits_total`、`bcl_e1_cache_misses_total`、`bcl_e1_cache_errors_total`、`bcl_e1_cache_fills_applied_total`、`bcl_e1_cache_fills_rejected_total`、`bcl_e1_cache_sets_total`、`bcl_e1_cache_deletes_total`。
- `GET /debug/latency[?reset=1]`：服务端 GET 与 PUT 延迟的 HdrHistogram 快照（微秒，1 µs–600 s，3 位有效数字，与 loadgen 相同），`reset=1` 在读取的同时开始新窗口。
- `GET /debug/strategy`：当前策略、是否关闭门控、TTL。
- `GUIDE_TRACE_ID` + `GUIDE_TRACE_FILE`：把一个 key 的事件按 JSON 行追加到文件。事件有 begin、read_start、store_read、cache_hit、cache_validated、fill、write_commit、delete，`t_ns` 为进程内单调时间。文件每 200 ms 刷新一次，收到 SIGTERM 时最后刷新。
