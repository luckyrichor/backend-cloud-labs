# AGENTS.md

最后更新：2026-10-28（北京时间，P2-4公共设施）· Codex

本文件是本仓库唯一的交接正文（Claude Code 经 `CLAUDE.md` 的 `@AGENTS.md` 导入，Codex 直接读）。没参与过的人只读这一份，应能知道项目是什么、做到哪、怎么继续。

## 1. 项目概述

后端服务与云原生方向的实验集合，三个月求职计划六项目之一（原编号 ⑥），对应岗位 **06、13、15、16、17**。总计划见 [workplan-docs](https://github.com/luckyrichor/workplan-docs)。

**结构原则：拆成小实验，不做成大项目。** 五个岗位方向差异极大，做一个“什么都沾一点”的大项目，结果是哪个方向都覆盖不了。

| 岗位 | 实验 | 核心考点 |
|---|---|---|
| 06 字节 后端（导购） | E1 导购服务 + 缓存一致性 | 数据一致性、接口设计、降级 |
| 13 米哈游 游戏服务器 | E2 房间状态同步服务 | 长连接、状态同步、帧驱动 |
| 15 拼多多 C++ 研发 | E3 规则表达式求值引擎 | AST／字节码、性能、benchmark |
| 16 拼多多 弹性计算 | E4 简化版调度器（打分、装箱、抢占） | 资源调度算法 |
| 17 拼多多 云原生（CMDB） | E5 配置项模型 + 关系图 + 变更审计 | 数据建模、图查询、变更管理 |

**当前阶段的目标**：执行用户提供的 [面试级完善方案](<docs/backend-cloud-labs 面试级完善方案.md>)，从“功能能跑”推进到“有真实数据量、有部署、有压测报告、有可复现的优化故事”。方案定义了 7 条优化故事 S1–S7 和 P0–P7 八个阶段，以及三个必须停下等用户审阅的检查点 CP1/CP2/CP3。

**不做什么**：不声称生产级多实例、云控制平面或百亿级检索；共用组件（容器化、监控、压测）不等于五个方向的专项能力都已覆盖，每个实验的覆盖范围写在各自 README，不做笼统声称。

## 2. 技术栈与环境

- Go 1.27.1（E1、E2、E4、E5、loadgen、datapipe、telemetry），每个模块独立，**不做 go.work 聚合**；C++20 + CMake（E3）。
- MySQL 8.0（ROW/FULL binlog）、Redis 7、Prometheus、Grafana，全部以容器跑在 **tx**（Ubuntu，4 核，7.5 GB 内存，内存紧张，多服务前先 `free -h`）。
- 压测端是 Mac，经 SSH 转发访问 tx 的 loopback 端口（公网端口 TCP 能连上但应用层超时，不能用）。
- 环境变量（只列名字）：
  - 部署客户端：`.local/deploy/client-env.json` 提供 `LOADGEN_MYSQL_DSN`、`P0_MYSQL_ADDRESS`、`P0_MYSQL_USER`、`P0_MYSQL_PASSWORD`，用 `python3 scripts/with-p0-env.py <命令>` 注入；
  - E1：`GUIDE_ADDRESS`、`ADMIN_ADDRESS`、`REDIS_ADDR`、`CACHE_READ_MODE`；E2：`ROOM_ADDRESS`、`ROOM_PER_IP_CONNECTIONS` 等（见 deploy/README.md）；测试：`E1_REDIS_TEST_ADDR`。
- 密钥只在已 gitignore 的 `.local/`，永不进仓库、文档、日志或聊天。

## 3. 快速上手

```bash
cd /home/ubuntu/project/WorkPlan/backend-cloud-labs   # tx 上的约定路径
git pull --ff-only
bash scripts/check.sh            # 全量回归：各 Go 模块 race+vet（含临时真实 Redis）+ E3 CTest
docker compose -f deploy/compose.yaml ps              # 部署栈 bcl-p0：8 个容器应 healthy；重建见 deploy/README.md
python3 scripts/with-p0-env.py go -C tools/datapipe run ./cmd/dbmigrate --dir ../../deploy/mysql/migrations status
```

- 数据生成：`go -C tools/datapipe run ./cmd/datagen --repo ../.. --out /home/ubuntu/datasets/backend-cloud-labs/p1 --amazon-dir /home/ubuntu/datasets/backend-cloud-labs/p0`（要求工作区干净）。
- 商品导入：`python3 scripts/with-p0-env.py go -C tools/datapipe run ./cmd/itemload --catalog <p1>/catalog --output <结果.json>`（要求 items 表为空）。
- 单个实验：进入目录 `go test -race -count=1 ./... && go vet ./...`；E3 见 experiments/e3-rule-engine/README.md。

## 4. 目录结构

| 路径 | 职责 |
|---|---|
| `experiments/e1-shopping-guide` | E1：`internal/catalog`（Item 与 Store 接口）、`internal/cache`（内存/Redis 版本门控缓存）、`internal/guide`（读写策略、修复队列与缓存代际）、`internal/httpapi`，入口 `cmd/guide` |
| `experiments/e2-room-sync` | E2：`room/server.go`，入口 `cmd/room` |
| `experiments/e3-rule-engine` | E3：`expression.hpp` 解析与求值，`httpdemo` 为 Go 薄 HTTP 封装 |
| `experiments/e4-scheduler` | E4：`scheduler.go`，入口 `cmd/compare` |
| `experiments/e5-cmdb` | E5：`store.go`、`http.go`，入口 `cmd/cmdb` |
| `support/telemetry` | 各服务共用的管理端口、指标直方图与 pprof |
| `loadgen` | 开环压测客户端（HdrHistogram，bench_run 登记） |
| `tools/datapipe` | P1：`datagen` 数据生成、`dbmigrate` 迁移、`itemload` 商品导入 |
| `tools/p0-probe` | P0 的 canal/binlog 选型探针 |
| `deploy/` | compose、Dockerfile、Prometheus/Grafana 配置；`deploy/mysql/migrations` 为编号 up/down 迁移 |
| `datasets/` | `manifest.json` 公开数据下载与校验记录；`stats/` 生成数据的清单与统计（原始数据不进仓库） |
| `scripts/` | check、bootstrap、数据下载校验、度分布统计、环境采集等 |
| `docs/` | 方案原文、status、progress、design-decisions、各阶段交付报告、`measurements/` 原始证据 |

原始数据在仓库外：P0 下载 `/home/ubuntu/datasets/backend-cloud-labs/p0`，P1 生成 `/home/ubuntu/datasets/backend-cloud-labs/p1`（945 MB）。

## 5. 架构与核心流程

- 五个实验相互独立，各自有入口、测试和边界。新增三层共用设施：真实存储（MySQL、Redis）、可观测与压测（指标端点、loadgen、bench_run 登记）、控制台（P7，React + TypeScript，尚未开始）。
- 部署：tx 上 compose 项目 `bcl-p0`；业务端口只发布到 127.0.0.1（E1 18086、E2 18082、E3 18083、E5 18085），管理端口 19086/19082/19083/19085，MySQL 13306，Redis 16379，Prometheus 19190，Grafana 13100。
- E1 当前数据流：读请求按模式（strict 每次比对源版本 / cache-first 直接用缓存）→ 未命中或不一致时读源 → 版本门控回填（Redis Lua 只接受更大版本）。写先提交源，再回填缓存；缓存失败时返回 degraded，并在共享修复队列里标记，溢出时切换缓存代际。P2 将把源换成 MySQL 并抽出四种策略。
- 数据模型见方案 D.2 与 `deploy/mysql/migrations`；E3 文档格式 BCLDOC1、E2 轨迹格式 BCLTRK1 的定义在 `tools/datapipe/docs.go`、`tracks.go` 文件头注释。

## 6. 关键决策记录

完整理由在 [docs/design-decisions.md](docs/design-decisions.md)（面试要讲的取舍都在这里）。近期要点：

- Amazon 用 3 个品类（All_Beauty 只有 15.7% 的行有价格，补 Appliances、Video_Games），只保留有价格的行，不插补。
- CMDB 图按阿里调用图度分布生成，约 2.3 条关系/配置项，**不补到方案估计的 5 条**（2026-10-24 用户授权助手决定）。
- ci 唯一键只约束存活行（生成列 live），偏离方案 D.2，为了支持删除后同名重建。
- bench_run 由 P0 init.sh 创建，不纳入迁移，全量回滚不会删掉运行登记。
- 数据生成只保证在 tx（linux/amd64）上逐字节可复现。
- 新方案授权在 P3 加入 E3 字节码 VM，推翻此前“不做 VM”的决定（届时记录同口径差分证据）。

## 7. 当前进度

**已完成（均已验证）**

- E1–E5 五个实验的本地功能与自动验收（历史细节见 docs/status.md、docs/progress.md）。
- **P0 基础与核实**（2026-10-20，Codex）：容器化、管理端点、loadgen 骨架、数据集下载校验、canal 与 ExprTk 选型验证、监控部署、tx 与 Mac 环境实测。报告 [P0 交付报告 v2](docs/P0交付报告-v2-2026-10-20.md)。
- **CP1**：2026-10-24 用户在群聊中要求“按方案开始”，视为通过。
- **P1 数据流水线**（2026-10-24，Claude Code，代码 `b0e5aba`，证据 `3cebe71`）：200 万商品已导入 MySQL 并对账；E3 文档三档、CMDB 三档、E2 轨迹两种；同一种子可逐字节复现；迁移 up/down/up 指纹一致；全量回归 Go 顶层 70 项 race 测试与 vet、CTest 7/7。证据在 `docs/measurements/2026-10-24-p1/` 与 `datasets/stats/`。

- **P2-1/P2-2**（Claude Code，7bc669f/2b49e7d）：MySQL源、写令牌、四策略和基础观测已实现。
- **P2-3 / S1**（Codex，测量代码 `d2ca677db32a17eaed9aff776244262b88d42801`）：实验及报告已完成。[S1缓存策略对照报告](docs/S1缓存策略对照报告-2026-10-27.md)、[原始证据/重算说明](docs/measurements/2026-10-27-s1/README.md)。主对照含补跑144轮（137有效/7无效），反向15轮全有效，直写24轮（23有效/1无效），独立剖析3轮全有效，另列tx本机校准20轮全有效；总206次完成、198有效/8无效，用户暂停中断1次另列。每个预定比较至少5个共同有效重复编号；所有失败保留，未事后改valid。
- **S1结论边界**：B/D相对A的主矩阵GET p99未获得显著改善证据；确认口径陈旧率均0，收益不确定，反向组缺乏分离度。有效A热点trace仍捕获源v60提交/删除后迟到回填v59并命中v59。直写时A/B/D陈旧率中位数约27.9%，C观察为0，但回源约378 QPS对A约195 QPS；不能称门控解决服务外写入，也不把观察0包装成未来保证。C热点回源/核心路径41组、37771次完整调用的HdrHistogram已重算；它不是裸SQL或正式全目录分位数。
- **S1验证/交付状态**：198个有效结果的源审计和运行时source/策略/TTL通过；公开压缩原始文件重算与本机结果逐字节一致。280条登记均有归档，旧诊断与最终输入隔离，旧trace混合已标识。全仓139次Go顶层测试执行通过、4次内存源下MySQL专项预期跳过（真实MySQL源另跑通过）、vet通过、CTest7/7。当前发压/E1均已停止；实验活动表恢复至200万行P1基底并汇总对账，原共享商品表没有被本实验更新。

**进行中**

- **P2-4进行中**：用户2026-10-28更新方案已落库。新增Linux统一单调时钟全key读/提交返回日志、S1 strict-version/故障回填延迟、S2 L1+Redis、事务边界Stream/checkpoint原子发布及每实例PEL恢复；自动测试通过，一次真实双实例MySQL→binlog→Stream→L1诊断已通过，S2首批50轮和20次故障已跑完，但观察器先等写响应、各组观察负载不同，已明确不作为最终窗口结果；并发/等观察量9个短探针已通过；持续批次完成19轮（13有效、6轮50写/秒观察器容量不足），已停止并保留原始记录。固定120次采样不变，5/10/20批次中20写/秒也出现间歇采样超时（失败目标均已见新值），完整保留v3；保守2/5/10批次v4已完成50轮47有效，最高档第4重复三组观察预算超时，故障门禁正确停止。观察器已增加HTTP连接复用及完整A/B/C组重试（最多20组、至少5共同有效重复）；4项Python协议测试、A/C各20秒200目标校准和4类真实故障探针全通过。v5同档位正式矩阵由用户级systemd `bcl-s2-v5.service` 执行，输出 `.local/p2-suite/s2-matrix-v5`，只有质量文件通过才续跑 `.local/p2-suite/s2-faults-v5`；查日志 `.local/p2-suite/s2-v5-controller.log`。2026-10-28已核实v5矩阵50/50有效，三个档位均为重复1–5共同有效、反向5/5有效；四类故障各5次，共20/20有效，systemd正常退出（ExecMainStatus=0），完成标记 `.local/p2-suite/s2-v5-finished.json` 存在，测量源码fdccc71。S2报告/归档已于2026-10-29完成：报告 `docs/S2跨实例一致性窗口报告-2026-10-29.md`，证据 `docs/measurements/2026-10-28-s2`（1837原始文件/252完成尝试，最终70次），公开重算3份产物逐字节一致；正式6100目标/732000轮询，0次首次新值后回退。此前全部批次保留，S1补测脚本已落 `scripts/p2/s1_v2.py`，公共真值重算 `audit_truth.py` 已有4项测试，另4项HTTP协议测试；新增完整行/窄版本源查询计数器，strict-version修复标记清理与C保持一致。全仓158次Go顶层通过/6预期跳过、race/vet及真实MySQL、CTest7/7、Python8/8。正式补测尚未开始；已跑3秒超时旧探针7次（A有效，C/CV部分transport_timeout），对应服务端最大约22–38ms而Mac控制往返出现1–2秒以上长尾。保留 `.local/p2-s1/probes`（源码35960f1）及中断，统一新补测Mac客户端10秒等待，服务端50ms SLO不变，下一步同源重新跑A/C/CV探针，再续自然/竞态/直写三组；方法更正同步飞书开发记录revision18–20并回读/回查。命名空间和TTL/删除后版本水位已修复并通过真实回归。协议见[全量日志与恢复协议](docs/P2-4全量日志与S2恢复协议-2026-10-28.md)。
- **S1首轮保留，验收缺口待补**：ACK下界不满足新版A.2，旧trace只记热点，无法倒算全key；不得删除首轮结论。公共日志建好后补做全key自然组、竞态放大组和version-only对照，CP2前修订报告。
- **P2整体未收尾**：S2/P2-5实测/报告已完成，S1补做待实测；CP2尚未到达。

**未开始**

- S1新版补做、CP2；P3（E3 · S4/S5）、P4（E4 · S6）、P5（E5 · S7）、P6（E2 · S3）、P7（控制台与收口）、CP3。

## 8. 下一步计划（按优先级）

1. **P2-4/S2联调**：使用真实MySQL ROW/FULL binlog与独立持久Redis，两个loopback实例验证恢复位置、PEL、重复乱序和进程kill。原公网绑定不变；Mac不参与毫秒窗口计时。
2. 部署两个E1实例验证源提交到缓存可见的时序；保留应用内删缓存/延迟双删对照。事件重复、乱序、丢失、进程重启分别测收敛与不一致窗口，不直接把单实例修复队列称为跨实例协议。
3. S2实测、报告及归档已完成；运行S1补测 `scripts/p2/s1_v2.py --phase probe/natural/race/bypass`，补齐新版A.2全key指标、50ms故障回填延迟和strict-version，首轮原始结果/结论保留。源/缓存仍用独立实验副本，不触碰共享表或清空共享Redis。
4. **整个P2完成后在CP2停下**，交S1/S2两份完整报告给用户审阅。

S1复现、固定版本与全部脚本见[证据说明](docs/measurements/2026-10-27-s1/README.md)。原始记录已归档，不覆盖同名输出、不再次prepare现存数据库。17:00暂停和18:04恢复均为历史状态，当前S1已完成。

## 9. 已知问题与坑

- items 表数据 310 MiB，超过 MySQL buffer pool（256 MiB），S1 读延迟会包含磁盘读，定位时要考虑。
- Mac 历史 Go 是 1.27.0（模块要求 1.27.1），nofile 默认 256，Docker daemon 不常驻。本轮校准用 tx 交叉构建的 Darwin 二进制，临时提高子进程 nofile，不改变 Mac 系统设置；正式实验仍需采集实际客户端环境。
- Mac→tx 当前路径经 utun5，公网 TCP connect 成功不代表服务可达；只用 SSH 转发，转发本地接入时间不能当作往返时延。
- tx 上非交互 ssh 没有代理环境变量；从 tx 下载 GitHub 等资源要显式带 `--proxy http://127.0.0.1:7890`。
- `datagen`/`loadgen`/镜像构建都拒绝脏工作区：生成后要先提交统计文件，重跑请用独立 `git worktree`。
- E1 的历史限制仍在：修复队列与缓存代际只协调同进程，不是多实例失效协议（S2 要解决）；IP 配额不替代认证；E3 无 VM 与索引（S4/S5 要解决）。
- MySQL DDL 隐式提交，单个迁移失败会停在半途，需要人工恢复。

## 10. 约定与规范

用户明确要求，优先级高于一般工程习惯：

- **区分事实与推断**：JD 原文明确写的、与“行业常见要求／补充推断”显式区分；联网补充要标注来源性质。
- **不夸大**：不把规划写成成果，不把本地数据包装成生产规模，不把加分项改写成硬性门槛。
- **不预设降级**：不因“时间可能不够”提前砍掉或缩水，取舍由用户做。
- 不安排模型算法、训练、微调、推理引擎优化方向的学习；不安排语言入门教程（用户 Python 最熟，C++/C#/Go 有基础）。
- **AI 产出的代码不等于可以写进简历**：同步维护 `docs/design-decisions.md`，让用户能讲清每个关键取舍。
- 数字纪律（方案 A.3）：不预写提升百分比；提升不超过 10% 记“无显著提升”；每个数字都要能指到原始文件与脚本。
- 多机协作：GitHub 是唯一事实源；同一项目同一时间只在一台机器上改；开工 `git pull`、收工 `git push`；动环境前读 `workplan-docs/环境与踩坑记录.md`。
- **Docker 默认跑在 tx**，在 Mac/Windows 起容器须先征得用户同意。从别的机器去 tx 跑测试用 `workplan-docs/scripts/run-on-tx.sh`（拒绝脏工作区并校验两边哈希）。
- 跨平台：`.gitattributes` 强制 `eol=lf`；Windows 写文件不带 BOM。
- 提交信息用 Conventional Commits 风格（如 `feat(p1): …`、`docs(p1): …`），说明正文写清验证结果。

**新内容写到哪儿**：

| 内容类型 | 去处 |
|---|---|
| 指引、约定、边界、当前进度与下一步 | 本文件 |
| 环境、机器、工具链的坑 | `workplan-docs/环境与踩坑记录.md`（［实测］／［预警］标注） |
| 已经发生的进展、验证结果、失败与返工 | `docs/progress.md`（只写已发生的事） |
| 关键设计取舍及其理由 | `docs/design-decisions.md` |
| 排期、工时、里程碑 | `workplan-docs/总节奏表.md`；跨项目进度汇总到 `workplan-docs/进度总览.md` |
| 岗位要求本身 | `workplan-docs/岗位要求原文.md`（唯一事实来源） |

`CLAUDE.md` 只保留 `@AGENTS.md` 导入与指路，不写正文。

## 11. 待确认事项

- 用户2026-10-28要求继续S2，S1新版口径补做在公共日志建好后、CP2前完成；不回头重跑旧矩阵打断P2-4。只有整个P2结束后到CP2再等审阅。

## 12. 飞书文档

位置：我的文档库 › WorkPlan › backend-cloud-labs（2026-10-24 用户确认），原由 Claude 使用 profile `claude` 以用户身份写入；Codex 续写使用 profile `codex`、`--as user`，保留原创建来源。

- 项目概览（节点本身，Codex 2026-10-13，来源 b98f225）：https://kcn0pbn0hwo0.feishu.cn/wiki/PgE6wzqFbiUa6lktbhHcPqP0nDi
- backend-cloud-labs 方案（用户方案原文同步）：https://kcn0pbn0hwo0.feishu.cn/wiki/BnIcwD3TUiLXNHkEs9qcCxgdng6
- S2报告（2026-10-29，实测源码fdccc71，归档/报告35960f1）：https://kcn0pbn0hwo0.feishu.cn/wiki/HvnawG2UbiDfY1kAJybc4eBznGb
- backend-cloud-labs 开发记录（每个阶段在文末追加一节，只追加不改旧内容）：https://kcn0pbn0hwo0.feishu.cn/wiki/AkEQwOqXTiyhbiklUqPcjUKfnXg

2026-10-27：Codex已将恢复/链路调整、真实竞态、120轮主矩阵及158轮完整计划进展追加至revision 7–12；本次暂停记录已追加至revision 13，回读“161次已完成尝试”并核验文档仍在本项目父节点下。18:04恢复记录已追加revision14并回读/回查通过。S1最终报告与完成状态已在2026-10-28同步至revision15，追加署名日期更正至revision16；回读与项目父节点回查通过，暂停快照保留作历史。

## 13. 更新日志

- 2026-10-29 · Codex：S2报告和175MB无损归档完成，公开重算6100目标/732000轮询逐字节一致；A/B窗口p99约503ms，C约12–13ms，停桥恢复TTL量级，写p99无稳定收益。S1补测脚本/全key真值与投影计数已通过检查，准备同源探针；CP2未到达。

- 2026-10-28 · Codex：接入用户修订方案，P2-4统一时钟全key日志、version-only/故障延迟和Stream恢复组件通过自动检查；下一步真实双实例联调，S1补做/CP2未完成。飞书开发记录revision17、方案更新revision6已追加；关键词回读与父节点回查通过。

- 2026-10-28 · Codex：S1最终报告/证据已提交至f56e418；2551个归档文件SHA256对Git提交内容核验通过；飞书开发记录revision15/16已同步并回读/回查，含同步日期更正。

- 2026-10-27 · Codex：完成S1全部实验/有效整组补跑及tx校准；206次完成198有效，归档280条登记与原始证据，公开重算逐字节一致；全仓139次Go顶层通过/4次预期跳过、vet和CTest7/7。S1报告如实保留门控p99/确认口径收益未证实结论，下一项S2。

- 2026-10-27 · Codex：18:04按用户“继续”恢复同源补跑，跳过已完成D/C/B，保留暂停和中断证据；恢复记录同步飞书revision14。

- 2026-10-27 · Codex：按用户要求暂停；停止当前发压/E1/worker，单独保留中断A轮，恢复并对账实验基底；原定158轮及3轮有效补跑已保留，归档恢复协议和控制脚本，S1报告/tx校准仍待完成。

- 2026-10-27 · Codex：用户恢复 Mac；取回旧 B 中断文件并登记无效，恢复实验基底；新增随任务生命周期的 Mac 保活与控制/传输/单轮超时，准备跨日独立重跑。

- 2026-10-26 · Codex：核对全部群历史与实际提交，纠正 P2-1/P2-2 的过期状态；修复 S1 采样/有效性门禁，补回归和真实 Mac 校准脚本。

- 2026-10-24 · Claude Code：飞书新建「backend-cloud-labs 方案」「backend-cloud-labs 开发记录」并回查位置。
- 2026-10-24 · Claude Code：按全局交接规范重写本文件（补概述、快速上手、目录、架构、进度、下一步、已知问题、待确认、飞书、日志）；记录 P1 完成、CMDB 密度定案、P2 开始。
- 2026-10-24 · Claude Code：P1 数据流水线完成并推送（b0e5aba、3cebe71），暂停交接见 docs/暂停交接-2026-10-24.md。
- 2026-10-20 · Codex：P0 完成，停在 CP1。
- 2026-10-13 · Codex：E5 CMDB 完成。
- 2026-10-09 · Codex：E1–E4 第五轮评审（缓存代际、E2 移动合并、E3 十进制语法、E4 未服务需求量）；证据 docs/measurements/2026-10-09-fifth-review.json。
