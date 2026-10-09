# 进度记录

最后更新：2026-10-09（北京时间）

本文件是 `backend-cloud-labs` 的进度事实源，汇总到 `workplan-docs/进度总览.md`。

格式：每条记录写明日期、做了什么、验证方式与结果、遇到的问题。**不写计划，只写已发生的事**；失败和返工也要记，那是面试时最有料的部分。

---

## 2026-09-20　E1 骨架就位，工具链在 tx 上验证通过

排期：W1 维持档（1 h）。目标是把 W3 主力周的时间留给缓存一致性本身，而不是花在脚手架和环境上。

### 做了什么

- 建 `experiments/e1-shopping-guide`，Go module，零第三方依赖。
- `internal/catalog`：Item 模型 + Store 接口 + 内存实现，5 个测试。
- 写 E1 的 README，写明范围、已做、**未做**和覆盖边界。

两个会影响后续的设计选择（理由写在实验 README 里）：Item 带 store 自己分配的单调 `Version`，让"缓存里这份是不是旧的"成为可判定问题，不依赖时钟；缓存不放进 `catalog` 包，否则不一致会变得不可观察。

### 验证

在 tx 上实测：

| 检查 | 结果 |
|---|---|
| `go version` | go1.27.1 linux/amd64 |
| `go test ./... -race` | ok，5 个测试全过 |
| `go vet ./...` | 无输出 |
| `gofmt -l .` | 无输出 |
| 模块下载（`go get github.com/redis/go-redis/v9`） | 成功，走腾讯云镜像，**不需要代理** |
| `docker pull redis:7-alpine` | 成功，57.8 MB，已备在 tx |

后两项是刻意提前做的探路：W3 才需要 Redis，但"拉不下来依赖"这种问题放到主力周再发现，代价是整周的节奏。探路用的临时 module 没有进仓库。

### 没做的部分

缓存层、HTTP 接口、降级路径、一致性的复现与验证 —— E1 的正题全都还没开始。**不要把当前状态描述成"导购服务已完成"。** E2～E5 未开工。

## 2026-10-01 W3 / E1 自动验收通过（Codex）

来源 backend-cloud-labs@1770378 + 未提交修改，tx。新增缓存接口及内存/Redis CAS 适配器、strict source revision 校验、导购 HTTP GET/PUT/recommendations、接口文档与设计取舍。确定性复现延迟读者旧值回填，内存/Redis 单调版本填充拒绝旧值；故障写不丢源结果，恢复后校验并修复旧缓存。

验证：真实临时 redis:7-alpine（本次独占 workplan-e1-20261001，127.0.0.1:56379），E1_REDIS_TEST_ADDR 配置后 `go test ./... -race -count=1` **10 passed、0 failed、0 skipped**，`go vet ./...` 与 `gofmt -l .` 无输出。

HTTP 服务实跑 127.0.0.1:8086：初始 kettle v1/4990；停 Redis 后 PUT 改为 v2/3990，HTTP 200/degraded=true；缓存停止期间 GET 得 v2/degraded=true；启动 Redis 后 GET 得 v2/source=store/degraded=false，下一次 source=cache_validated；POST recommendations/budget=4500 返回该商品。源是内存 store，不称作已验证持久数据库/生产导购；所有读严格验证源版本，未声称缓存减轻 DB 读。服务与临时容器验证后清理，不动常驻 memory Postgres。

未提交/推送，无实际工时声明。E2～E5 未实现。
## 2026-10-02 W5–W7（Codex）

来源 backend-cloud-labs@8519ae7 + 本轮工作树修改。W5 独立维护：E1 推荐去重、按价格/ID 排序，增加回归。W6 E2：独立 module，TCP JSON 长连接、权威帧循环、同帧广播、随机恢复凭据、重连恢复、输入去重。W7 独立维护：旧连接输入失效、非法握手拒绝、慢队列隔离和关闭时回收 worker 的验证。

`sg docker -c 'bash scripts/bootstrap.sh'`：E1 **11 tests**（实际临时 Redis，无 skip）、E2 **3 tests**，全部 go test -race -count=1 通过，go vet 通过；gofmt 已执行。Redis 临时容器自动清理，未动常驻服务。E2 默认50ms帧，本次测试用2ms/1ms有界驱动，无生产容量声明。E1源/房间状态仍在内存；E3–E5未开工。E2接口/边界见实验README。无实际工时声明，此前章节为历史记录。


## 2026-10-07 Codex：W8–W10实际执行

W8 E3完成独立C++ AST解析/求值与缓存/重解析对比：222条检查通过，20轮×100000操作checksum60012一致。W9维护完成短路与输入/节点/深度限制、异常和非有限数验证。W10 E4完成独立Go调度器：spread/binpack/preempt，CPU和内存约束、严格优先级抢占、失败抢占不修改既有任务；4个Go测试、race/vet通过。固定到达序列中spread拒绝大紧急任务，binpack接纳，preempt驱逐1项后接纳；不声称全局最优或Kubernetes部署。

全项目scripts/check.sh实际临时Redis验收E1/E2/E4并构建CTest E3通过；新增E4 CPU/内存专项测试单独race/vet通过。各实验README及CSV记录设备/方法/边界。来源backend-cloud-labs@68f0767 + 本轮工作树，SHA256见测量元数据。E5后续未做。


## 2026-10-08 Codex：E3批量过滤与枚举性能扩展

来源 backend-cloud-labs@6d1e487 + 本轮修改；测量源码SHA256见 docs/measurements/2026-10-08-e3-batch.json。AST运算符由字符串改枚举，解析时转换、求值switch，保留短路和输入/节点/嵌套限制。evaluate_batch/filter_batch使用只读span复用AST，返回结果或按序下标；顺序执行，错误包含文档下标，不返回部分结果。旧字符串实现冻结作独立性能/差分基线，不接入实际求值路径；没有新增字节码/并行执行。

Release -Wall/-Wextra/-Werror构建通过，CTest2/2：原222检查、新19568批量/差分检查。ASan/UBSan Debug CTest2/2通过（detect_leaks=0，不声明泄漏验证）。覆盖18组表达式×289份变量组合的数值/错误对照，以及10000文档批量、空输入、短路缺失变量、非有限值、失败下标、输入不变与节点限制。

四路径 benchmark 在相同输入/输出分配条件下测量，完整结果比较在计时外；两类数据分布、1000/10000/100000文档、每组预热+10轮，模式顺序轮换，每轮重复至100万文档求值。240条计时行及校验和一致。枚举单条较旧字符串2.29–3.06倍，枚举批量2.76–3.43倍；100000文档完整右侧场景分别140.28/51.28/47.49ns每文档（字符串/枚举单条/批量，中位轮均值）。最终精确数据以CSV/JSON为准。

本机共享VM未绑核，初始测量与sanitizer编译/测试重叠，元数据明确记录。批量只是顺序API，最大独立合成文档10万，不包装为生产吞吐或岗位全覆盖。未安装软件、未启动新常驻服务。此轮只修改E3与对应文档，其他实验没有重新验收。


## 2026-10-08 Codex：E4重排/策略组合/增量资源账本

来源 backend-cloud-labs@9fcc76c + 本轮修改，源码摘要见 measurements/2026-10-08-e4-requeue.json。确认旧实现被驱逐任务只留历史、preempt仅spread、选择时反复重算驻留占用。现在被驱逐任务进Pending，每个到达完成后按优先级稳定重排，使用剩余空间、禁止重排再次驱逐避免振荡；Preempted为历史。保留preempt=spread，新增preempt-binpack；每次成功放置/提交驱逐增量更新双资源，失败只修改试算副本。全量重算usage移至测试作独立oracle。

首轮原TestPolicies期待preempt驻留2而失败，新语义被驱逐任务迁到另一节点，最终驻留3；更新契约后7 tests全部go test -race -count=1通过，go vet通过。新增3项测试：迁移、多层抢占有界等待、80随机任务81前缀×4策略的账本/容量/唯一ID守恒。相同负载spread驻留2拒绝1，binpack/preempt/preempt-binpack均驻留3，驱逐次数分别0/1/0，Pending均0；保留旧历史CSV并发布新CSV。

32节点binpack1000/10000任务整批benchmark各3轮，中位0.337/4.035ms，原始内存/分配与工具链随结果保存；没有同期旧实现对比，不宣称量化提速。测试完成后最终源码重新测量。E3文档明确已有顺序批量与枚举，但只有10万独立合成文档，不具备索引/线程/列式/分片或百亿DOC验收；E4没有持续控制循环、任务完成事件、持久队列/节点故障恢复，抢占/Pending仍扫描。未安装软件/新增服务，其他实验本轮未重复测试。


## 2026-10-08：第二轮跨实验评审修复（Codex）

开工已核对工作树干净、GitHub同步；E3枚举/批量在9fcc76c，E4重排/增量在97eb85c，早前W8–W10在6d1e487，均已入Git。分别核对历史来源摘要的23/7/5个文件与对应提交一致，保留测量时“基线+工作树”的真实历史，不改写为当时已经提交。

先复现E4同优先级小任务先被驱逐导致2次驱逐；新增缺口有效贡献排序后此例仅1次，仍为启发式。E2改每连接64队列/每帧32预算，新增断线30秒身份宽限与过期回收，真实TCP验证过期容量释放和旧token拒绝。E1新增默认strict/可选cache-first，确定性缓存故障恢复验证strict v2/一次源读、cache-first v1/零源读，过期与故障降级均通过；补推荐去重及排序契约。

完整 scripts/check.sh：E1 13、E2 6、E4 8项顶层测试（共27，含子测试）通过race；实际临时Redis用例通过无skip，go vet均通过；E3 Release CTest2/2（原222+19568检查）通过。本轮首次Go测试因新增测试的int64/uint64比较编译失败，修正测试类型后通过。E1 benchmark初次遗漏包路径，修正为 ./internal/guide 后5轮完成。

E1内存热命中5轮中位179.6/135.2 ns/op，源读取1/0每请求；不是生产性能。完整来源、历史提交核验与范围见 measurements/2026-10-08-review-followup.json。修订对应97eb85c+本轮修改，提交后的源码可按SHA256核对。没有安装软件或部署常驻服务；E3未实现索引/并行/分片，不能声称百亿DOC。


## 2026-10-08：接口文档与统计勘误（Codex）

按实现7277e74核对E1接口说明，补cache_unvalidated、strict/cache-first新鲜度差别及推荐去重排序；源失败503仅适用于实际访问源的路径。复算5轮原始strict值184.5、179.6、181.4、178.9、177.7，中位179.6 ns/op；修正上轮progress误写180.4，原始数据未修改。这是文档修订，没有修改运行代码或沿用旧测试冒充新功能验收。


## 2026-10-09：第三轮七项建议落地（Codex）

基线544bf3a + 本轮修改：JSON标签/响应契约测试、Redis v2缓存隔离、直接依赖整理、注入时钟、E2/E4 module路径统一、本地缓存失败版本补偿、IP连接和身份准入及快照connected、E4三场景对比、E3绑定slot与含转换成本测量。首轮compare用外部包无字段名结构字面量导致go vet拒绝，改带字段名/构造助手后通过。

最终scripts/check.sh通过：E1 18/E2 8/E4 9项Go顶层测试（共35，含子测试）race、vet均通过；真实临时Redis两项用例无skip。E3 Release CTest3/3（旧222+19568、新1500差分及边界）通过；ASan/UBSan Debug CTest3/3，detect_leaks=0。E3十轮哈希/slot/转换slot中位47.6856/14.51135/34.85875 ns/doc，结果checksum一致。E4组合用例binpack拒绝urgent，preempt接纳且占用12，preempt-binpack接纳且占用16、驱逐2次；不声称全面优于其他策略。

新证据见 measurements/2026-10-09-third-review.json 与各CSV；历史报告保留原源码。无新常驻部署。补偿不跨重启/实例，IP限额不替代认证，slot优化不是百亿检索验收；所有这些边界同步各README。
