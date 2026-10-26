# 进度记录


当前一页摘要见 [现状与边界](status.md)。下面是历史记录；每条测试/测量只对应当时元数据版本。
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

## 2026-10-09：第四轮并发与类型边界

E1无标记atomic读快路径已实现；锁内同步版本标记/溢出和dirty，清空后恢复快路径。cache-first缺Repairs显式报错且Put在源提交前拒绝，NewService/HTTP默认队列保持。暂不自动过期标记和overflow：接口无TTL上界、旧回填可刷新寿命，简单TTL清理不安全，此项仍是明确未解决限制。

E2 IPv6用/64聚合连接/保留身份配额，mapped IPv4归一。E3 bind返回独立拥有树的只读Bound，验证独立schema、原树修改和销毁不影响运行对象。E4报告驱逐事件打断CPU/内存，与拒绝/最终占用并列，组合场景preempt打断6/6且拒绝1、组合8/8且拒绝0。设计/E1/E2表头当前版本指向统一元数据，历史结果保留历史版本。

自动回归39项Go顶层（E1 20/E2 9/E4 10）race+vet，2项真实Redis未skip；E3 Release与ASan/UBSan CTest3/3，泄漏检测关闭。并发基准每组3次、300ms，GOMAXPROCS=1/4/8，共享同一队列；8工作线程locked-empty中位30.16 ns/op、atomic-empty1.247 ns/op，有其他商品标记时48.51 ns/op。这是队列隔离实验，不是API吞吐或请求尾延迟。Service并发基准包含内存store/cache锁，strict/cache-first 8线程中位222.1/191.3 ns/op；顺序ReadModes在GOMAXPROCS=1中位185.7/148.0，与历史源码时间不同，不能单因归因。测量元数据见2026-10-09-fourth-review.json；前轮slot数字未按新Bound复测，不宣称属于本轮。

## 2026-10-09：第五轮协议、语法与同源码复测

修正E3十进制扫描、locale独立转换与拒绝十六进制专项；E2每帧合并最新移动意图，只应用一次并限制斜向长度；E1推荐8路并发且等待所有worker完成，内存缓存RWMutex；新增source生命周期/缓存代际，1024标记溢出后恢复cache-first，捕获视图隔离旧回填，跨代源写重新标记。内存持续填充回收过期旧条目；Redis由TTL回收旧代。E4新增最终Rejected+Pending未服务资源量。

同源码完整Get路径切fast/rw开关，GOMAXPROCS1/4/8、每配置3次200ms；8并发旧式锁组合strict/cache-first323.7/401.7，新组合187.6/181.3 ns/op。Bound复测10轮中位哈希46.3972/预数组17.03175/逐条转换35.4393 ns/doc、checksum一致。不是HTTP或生产吞吐；不将历史版本差异归因为树复制。E3 README重写为当前接口+证据版本映射，新一页摘要docs/status.md。

最终49项Go顶层race+vet，3项真实Redis未skip；Release和ASan/UBSan CTest4/4。增加确定性跨代旧读、跨代成功旧填写、源生命周期、高版本Redis、推荐并行及取消、移动洪泛回归。执行中一次写测试文件误用了实验目录加根目录相对路径，命令报No such file并未写入，已改正路径后验收；首次服务比较完成后缓存视图前缀预计算实现又优化，旧比较结果丢弃并按最终实现重跑。

本轮不采用VM、后台排空和TTL删标记，理由与当前边界统一见status.md；永久溢出已由代际协议解决。此实现限定共享本地source/cache队列，source重启数据恢复/跨实例失效仍未实现。来源和源码摘要统一见2026-10-09-fifth-review.json。

## 2026-10-13 E5 CMDB（Codex）

来源 766efcf + 本轮修改。独立Go模块，配置项CRUD/有向关系、0..10层out/in/both图、全图revision CAS、actor/reason审计与历史快照。图和审计同文件临时write/fsync/rename发布；失败不更新内存，重启检查连续快照链。五项测试含20并发CAS、历史/循环/反向、副本隔离、存储失败及HTTP；完整check.sh四Go模块race+vet/真实Redis、E3 Release4/4通过。证据源摘要见measurements/2026-10-13-cmdb.json。单进程原子文件实验，无真实身份认证/防篡改/电源故障无损保证；完整快照存储非生产大规模策略。

### 2026-10-20 · P0 tx 独立实现（正式测量待后续证据提交）

已补容器化、独立管理端点、E2 可配置配额、E3 C++ HTTP 演示、E4 墙钟输出、开环 loadgen、MySQL bench_run 与 canal 选型探针、数据下载校验脚本。开发回归实际通过 Go 顶层 63 项 race 测试及 vet、CTest 7/7；其中包含原有真实 Redis 用例。开发错误包括 E2 配额兼容回归、MySQL secret 权限与临时健康误报，均已修复。Amazon 旧 URL 404，现行官方地址验证成功；六个数据文件均有哈希及行数。正式源码提交、干净工作区测量及其报告在后续记录，不将这里的开发工作区结果标为干净测量。Mac 环境/网络和 CP1 决定未完成，未进入 P1。

P0 干净源码 5e1efd8 的验证：全量 race/vet 与 CTest 通过；回显与 1 秒暂停校准均按设定 100 req/s 发送（各 300 请求、无失败），暂停 p99 接近一秒；bench_run 已登记。详数和原始直方图在后续正式证据中。监控初启发现 19090 被其他进程占用，没有停止该进程，改用已探测空闲的 loopback 19190；Grafana 13100。此后镜像固定到实际下载 digest，环境采集补记进程 TZ 与宿主时区，避免将两者混为一谈。

### 2026-10-20 · P0 tx 正式证据交付（Mac 尚待补证）

测量源提交 `5b421c1039da7b0d2ee3965c66f1afca434737f2`，采集前工作区干净；Go 顶层 63 项 race 测试与 vet 通过、无跳过，CTest 7/7。八个本项目容器全部 healthy，指标四目标 up；canal 实收 MySQL ROW 事件，ExprTk 编译运行通过。回显/暂停开环自检成功且登记 MySQL。原始证据与哈希：`docs/measurements/2026-10-20-p0/summary.json`；交付摘要：`docs/P0-tx交付报告-2026-10-20.md`。正式源上已固定运行镜像 digest。监控部署后十个一秒空载采样区间无 swap in/out，记录预存 swap，不做因果夸大。六个数据文件校验通过，原方案 SHA256 不变。Mac 环境、真实网络/隧道采样仍缺，P0 未验收、P1 未开始。后续证据/报告归档提交不改变已测服务源码，不将其提交号冒充测量源提交。

### 2026-10-20 · P0 Mac 补证与 CP1 交付

经负责人提供的 `ssh luomac` 连接真实 Mac，干净副本同步至 `9f8e770fa4e7d6cb10434d4e9e13fde5146cb947` 后采集。Mac 系统、CPU、内核、内存/swap、磁盘、nofile、Docker 客户端/daemon 状态与时区已记录；未在 Mac 启动容器。ICMP 20 次，平均 20.879 ms；原始记录保存于 `docs/measurements/2026-10-20-p0-mac/`。发现当前路径为 utun5，公网业务端口 TCP connect 快速成功但实际应用响应全部超时，不能据此声称公网可达；四服务 SSH 转发真实回复成功，临时进程及端口清理通过。曾仅用 TCP connect 判断可达，补充应用层及路由验证后纠正该歧义。再次通过 Go 顶层 63 项 race/vet、CTest 7/7；容器健康复核通过。当前只改采集脚本和归档文档，git diff 确认服务源码/部署相对 5b421c1 不变，保留原服务测量版本。P0 自动技术检查补齐，CP1 等负责人确认，P1 未开始。报告：`docs/P0交付报告-v2-2026-10-20.md`；上一版“Mac 待补”是其生成时状态。

### 2026-10-24 · P1 数据流水线（Claude Code）

负责人在群聊中要求“按方案开始”，作为 CP1 通过、进入 P1。代码提交 `b0e5aba`：新增 tools/datapipe（datagen、dbmigrate、itemload）、deploy/mysql/migrations、scripts/derive-degree-stats.py；check.sh 与 bootstrap.sh 纳入新模块。

- **数据源**：All_Beauty 只有 15.7% 的行有价格，且无类目路径。补下载 Appliances（66,406,659 字节，94,327 行）和 Video_Games（103,096,082 字节，137,269 行），经 verify-datasets.py 登记到 manifest，HTTP 200。三个品类共读入 344,186 行，保留有价格、有标题的 126,367 行；丢弃原因见 datasets/stats/catalog.json。
- **度分布**：阿里两个分片共 12,486,634 行，得到 2,209 个服务、7,091 条服务依赖边、13,516 条服务→存储边；结果保存在 datasets/stats/graph-degree.json，同时带 SNAP 的度分布作形状参照。第一次统计用错了列（把行号列当成第 0 列），得出“只有 2 个节点有出度”这种明显不对的结果；对照表头改正后重算。
- **生成**：在干净提交上运行 datagen（seed 20261023，约 20 秒），得到商品 200 万行（公开 126,367 行，克隆 1,873,633 行，15.83 倍）、文档 10^5/10^6/10^7 三档、CMDB 10^4/10^5/10^6 三档、两种轨迹（2000 个机器人 × 2400 帧）。数据版本见 datasets/stats/*.json。第二次在同一提交的独立 worktree 中生成到另一目录，15 个文件的 sha256 全部相同（docs/measurements/2026-10-24-p1/run-*.sha256），清单中的哈希与独立的 sha256sum 也一致。第二次运行最初在主目录中被“工作区不干净”拒绝（第一次运行产生的统计文件尚未提交），这是守卫生效，改用 worktree 后完成。
- **CMDB 实测**：每个配置项 2.19–2.28 条关系，低于方案“约 5 条”，原因与处理见 design-decisions；10^4 档有 1 次度数截断（少 42 条边）。
- **迁移与导入**：4 个迁移依次执行 up → 全部 down → 全部 up，指纹一致（3ae730f3…）。ci 的“仅存活行唯一”约束已在真实 MySQL 上验证，验证用的事务已回滚。导入 2,000,000 行用时 41.5 秒，行数、最大 id、价格总和、库存总和与清单全部一致（itemload.json）。items 表数据 310 MiB，超过 MySQL buffer pool 的 256 MiB，S1 的读延迟里会包含磁盘读，P2 定位时需要考虑。
- **测试**：datapipe 新增 7 项测试（race 与 vet 通过）：可复现性、克隆价格范围、热度排列、列式头部与对齐、CMDB 不变量（无自环、无重边、不跨租户、关系种类与端点合法、截断有计数）、轨迹前缀与速度上限、迁移成对。开发中有一项截断测试的前提写错（误以为 60 个服务里只有 7 个有入度权重），修正测试后通过。
- **收尾回归**：在源码 `b0e5aba` 上运行 scripts/check.sh，退出码 0。Go 顶层 70 项 race 测试通过（P0 的 63 项加 datapipe 7 项），无跳过，vet 通过，含真实 Redis；CTest 7/7。摘要见 check-summary.json。

### 2026-10-24 · P2-1 MySQL 数据源与写令牌（Claude Code）

- E1 新增 `catalog.MySQLStore`：一条 `INSERT … ON DUPLICATE KEY UPDATE` 在行锁内递增版本，经 `LAST_INSERT_ID(expr)` 带回已提交的版本。新增写令牌（`GUIDE_WRITE_TOKEN`，PUT 缺失或错误返回 401）和 `GUIDE_SOURCE=mysql` 的启动接线；MySQL 源缺令牌时拒绝启动。
- 现有 E1 测试的 id 机械替换为数字串，经 catalogtest 工厂可切换数据源；check.sh 新增一次性 MySQL 容器（同一固定镜像，tmpfs），把 E1 整套测试在 MySQL 源下再跑一遍。
- 第一次全量回归失败：`TestRedisLegacyPayloadIsNotUsedAfterJSONContractChange`。原因是批量把 "sku" 替换成 "101" 时，清理语句里拼接出的键 `v2:sku` 没有跟着变，内存源那一轮留下了 `v2:101`，MySQL 源那一轮共用同一个临时 Redis，读到残留键，报“legacy hit”。这个测试只测缓存、不涉及数据源，于是把两个纯缓存测试恢复成原来的 id，修正后通过。
- 结果：check.sh 退出码 0。E1 内存源 29 项通过，另 4 项 MySQL 专项按设计跳过；MySQL 源 33 项全部通过，含 3 项真实 Redis 测试与 4 项专项测试（16 个并发写者各写 25 次，版本恰好为 1..400；大于 2^53 的版本精确加一；非规范 id 被拒）。其他模块与此前一致，CTest 7/7。
- 用真实的 200 万行 items 表做了只读冒烟：GET /items/1 与 /items/2000000 正常，不存在或非法 id 返回 404，不带令牌的 PUT 返回 401；表行数与最大版本没有变化（2,000,000 行，最大版本 1）。

### 2026-10-24 · P2-2 四种读写策略与 S1 观测（Claude Code）

- `CACHE_READ_MODE` 新增 `cache-aside`（A）与 `versioned`（B），原有 strict（C）、cache-first（D）的代码路径不变，默认仍是 strict。`GUIDE_UNGATED_FILLS=1` 用来关闭 B 的版本门控做反向验证。内存缓存和 Redis 缓存都新增了不带门控的 Set/Delete，Redis 侧用 MULTI 保证 TTL 一起写入。
- 用单测确定性地复现 S1 的竞态：读者取到 v1 后暂停，写者提交 v2，读者恢复后回填。结果：A 留下陈旧的 v1；B 拒绝迟到的回填；B 关掉门控后又陈旧；C、D 都返回 v2。
- 观测：数据源与缓存的 10 个计数器、服务端 GET/PUT 的 HdrHistogram（`/debug/latency`，可同时重置窗口）、当前策略（`/debug/strategy`）、单个 key 的事件时间线（JSON 行文件）。都只在管理端口上；telemetry 新增了注册计数器与额外管理路由的接口。用二进制做了端到端冒烟：计数器、策略、时间线都符合预期，SIGTERM 时时间线已刷新到文件。
- 测试：新增 E1 策略测试 8 项、observe 测试 3 项、真实 Redis 的 Set/Delete 测试 1 项、telemetry 测试 1 项。check.sh 退出码 0：E1 内存源 41 项通过、4 项按设计跳过，MySQL 源 45 项全部通过，全仓 Go 顶层测试共 133 次通过、0 失败，CTest 7/7。
- 开发中的小问题：冒烟时用临时 Python 读直方图快照报 KeyError，原因是 hdr.Snapshot 序列化的字段名是 `Counts`（Go 端反序列化不区分大小写，所以 Go 测试能过）。这不是服务的缺陷，报告脚本要按实际字段名读取。


### 2026-10-26 · Codex 续接 P2-3：测量门禁与真实 Mac 校准

- 读取群内全部 20 条历史消息，并核对实际提交，确认 P2-1/P2-2 已由 Claude 完成；AGENTS.md 原状态过期，已修正。续接范围是 S1，CP2 仍在 P2 收尾后。
- 源提交 `3b6f980`：修复 loadgen/s1.go 的请求失败/缺版本/指标丢失误标有效、预热排空与直方图重置、完整到达窗口/5% 到达率门禁、非有限配置与零请求窗口、直写必须更新一行。新增三项顶层回归（包含子场景），scripts/calibrate-s1-mac.py 提供真实 Mac 的可重现只读校准。
- 在该干净提交上 scripts/check.sh 退出码 0：136 次 Go 顶层通过，4 次内存源下 MySQL 专项预期跳过（真实 MySQL 轮全部通过），含子测试共 151 次通过；vet 通过，CTest 7/7。不是 136 个互不重复的测试。
- luomac 当前可连，核实 Darwin arm64 与 ssh tx 身份。交叉编译后由 Mac 临时 SSH 隧道发压；不用 Mac 上旧 Go，临时提高子进程 nofile。正式校准窗口 50/200/500 req/s × 10 秒，预热各 3 秒，总计 7500 次读全部成功，无丢到达。首次联调的空载采样与回归有干扰，正式归档使用回归结束后独立重跑的数据。
- 证据：measurements/2026-10-26-s1-calibration/，含压缩原始结果/逐请求记录、环境、校验和、登记 ID 与摘要重算脚本。三次归档校准以 S1-calibration 登记 bench_run。实际服务未重建，源版本尚未核实，仅记录镜像/二进制哈希。
- 本轮只读、单次、不恢复缓存的校准不能替代 S1 矩阵；客户端 p99 波动大，正式报告要服务端/客户端并列，不把差距全部归因于网络。下一步补安全数据恢复、版本可追溯部署、正式率档与交替编排，然后完成 S1，再进入 S2；当前没有宣称 P2-3 完成。

- 飞书：已使用 codex profile、用户身份追加本次开发记录（revision 4），回读署名与章节、回查项目节点均通过，沿用 AGENTS.md 中原链接；没有覆盖 Claude 原记录。


### 2026-10-26 · S1 正式编排与隔离数据准备（Codex，进行中）

已完成独立 bcl_s1 数据库、items_base/活动表、专用 Redis/E1 实验容器、固定提交/镜像/二进制哈希、Mac 持续 SSH worker、逐轮恢复、最终写版本/价格/库存审计、HdrHistogram 摘要工具与独立 CPU/锁剖析编排。共享 bcl.items 初检有 176 行版本不为 1，价格/库存汇总与 P1 不同，保持原样；实验表从哈希匹配的 P1 TSV 导入，200 万行及各汇总对账通过（import.json），导入约 41.7 秒。

遇到并修正的问题：docker inspect 默认会把同名镜像当成容器（改用 --type container）；编排漏传 GUIDE_SOURCE=mysql（已修正，并增加 source/mysql 与初始热点版本门禁，前三轮误测为内存源全部判无效）；SSH 监听未就绪时过早派发（改为等待实际本地 listener）；SSH 客户端约 1024 通道处 poll: Invalid argument（改为连接上限 256，持久诊断日志）；无效运行里写提交已发生但确认丢失，源码最终状态可能超过客户端已确认版本（保留原始无效结果，不当成可比较数据）。

真实 MySQL/Redis cache-aside 有效探测曾在 500/1500 req/s 观察到陈旧读，不能据单次探测宣称策略提升。受连接启动抖动影响，后续探测仍有热身失败/丢到达，正式矩阵未启动。MySQL 会话/服务端指标诊断保存于 .local/s1-suite/mysql-diagnostic.jsonl。新增测量前只读连接预建（不写商品、不进入窗口），loadgen race/vet 通过，真实链路效果待验证。全部探测结果、恢复审计与事件保留在 .local/s1-suite/，下一步先通过稳定性门禁再跑完整矩阵。

服务/原负载源 3ee5dd3 的全仓回归：136 次 Go 顶层通过、4 次内存源下 MySQL 专项预期跳过，vet 与 CTest 7/7；后续只读连接预建新增 1 项顶层测试，loadgen race/vet 单独通过，尚未在最终源上重跑全仓。


### 2026-10-26 · S1 完整矩阵启动（Codex）

源 `b0edac1bf376a45d9c2d0e0800ae8a2ab072704e`：只预建已缓存热点不能充分触达源连接池，改为固定热度前缀 128 个不同商品的只读连接预建后，500/1500 req/s 基线探测通过；3000 仍不通过客户端有效性门禁。按预先写定的 25%/50%/80% 向下对齐 50 req/s 规则，design.json 选择 350/750/1200 三档。原始有效/无效探测均保留在 .local/s1-suite/probe*。

完整矩阵已启动：两档读写比、四策略、三个率档、五次交替重复（120 轮），加高率 95:5 反向组 15 轮、直写 MySQL 20 轮，以及三次独立剖析。每轮预热 10 秒，正式窗口 max(20,ceil(12000/(rate*ratio))) 秒。正式结果尚未完成，不能预写结论；进行中的逐轮原始结果/写审计/恢复/登记保存在 .local/s1-suite/matrix/，事件追加至 events.jsonl。

已将隔离编排与启动问题、a9ed948 全仓 137 次 Go 顶层通过/4 次预期跳过、vet/CTest 7/7 追加飞书开发记录 revision 5，并回查位置、回读署名。后续 b0edac1 只读热度前缀改动单独 loadgen race/vet 通过；完整矩阵期间不运行其他测试或构建。


### 2026-10-26 · 修正预热/正式丢弃计数，保留先导矩阵（Codex）

b0edac1 的七轮矩阵试跑中发现口径错误：result.scheduled/reads/writes 仅统计正式窗口，但 dropped 混入预热丢弃，导致原始请求数不平衡。控制器已在一轮写审计/恢复/登记完成的边界停止，七轮及全部失败原因保留 .local/s1-suite/matrix/，不会后改原 JSON 或据它们声称正式完成。

现改为 dropped 只计正式窗口，新增 warmup_dropped 独立保留；正式失败/丢弃/超时门禁不变，已发出但失败的预热请求仍判无效，避免隐藏未确认写。新增回归用受控慢热身触发两次预热丢弃，验证正式请求完整、统计平衡且不会混入正式丢弃；loadgen race/vet 通过。连接上限调整为 384，两连接池上限合计 768，仍低于已见 SSH 约 1024 通道故障点。下一步在统一新版本重做率档探测、完整矩阵至 matrix-v2，不混用不同源版本。
