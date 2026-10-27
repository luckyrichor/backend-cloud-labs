# AGENTS.md

最后更新：2026-10-27（北京时间）· Codex

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

**进行中**

- **P2（E1 · S1/S2）**：P2-1（MySQL 源、写令牌，`7bc669f`）、P2-2（四策略及观测，`2b49e7d`）已完成；原交接文档落后于群聊与代码，2026-10-26 已核对。
- **P2-3 / S1**：基础压测负载已由 Claude 实现（`c0c5df8`）。Codex 在 `3b6f980` 修复测量门禁与预热边界，新增 `scripts/calibrate-s1-mac.py`；真实 Mac 只读校准已完成：三档 50/200/500 req/s，共 7500 个正式窗口读请求，全部成功，无丢到达；证据见 `docs/measurements/2026-10-26-s1-calibration/README.md`。全仓回归 136 次 Go 顶层通过、4 次内存源下预期跳过、vet 与 CTest 7/7。S1 正式矩阵、反向验证、绕过服务直写、定位与最终报告尚未完成；续接已实现 `scripts/s1-suite.py`（独立数据库/带标签实验容器、固定版本构建、逐轮恢复、最终写版本/字段审计、真实 Mac 编排）和 `loadgen/cmd/s1summary`。独立数据已从哈希校验的 P1 TSV 导入并对账；固定热度前缀的只读 HTTP/数据源连接预建后，500/1500 req/s 的探测通过门禁；3000 不通过。由预定义规则确定正式率档 350/750/1200 req/s，源 `b0edac1` 的矩阵试跑发现预热/正式丢弃计数混用，已在完成并恢复的轮次边界停止；七轮先导数据保留 `.local/s1-suite/matrix/`，不作最终比较。已修复计数范围，源 `1c3ca2a` 的 matrix-v2 首轮 A 有效（12251 次读，15 次陈旧读），B 遭 Mac 离线中断。2026-10-27 用户恢复 Mac 后已取回 B 文件、登记无效、停止旧实验 E1 并恢复实验数据；旧控制进程/worker 已退出，session 58831 不再有效。跨日结果不混作同一组比较，今天源 `11120a1` 的单隧道试跑在 C 等条件出现大量客户端排队/丢到达，已在完成审计和恢复的 C p99/q750/r1 边界停止（旧 session 92819 不再有效）；`.local/s1-suite/matrix-2026-10-27/` 保留为链路诊断，不作为最终五次比较。当前改为四条独立业务 SSH 连接和独立管理/MySQL 控制连接，按相同请求计划序号分流；每业务流最多 256 HTTP 连接、全局 1536 在途、旁路数据库最多 64 连接。新增分流/统计回归、loadgen race/vet 和 Python 归档测试 3 项已通过。正式矩阵尚未完成；下一步在新提交上全仓检查，同时探测 A/C，按原方案由基线 A 有效且服务端 GET p99≤50ms 的最高探测档定档（C 的成本单独保留，不用于筛选），再按 25%/50%/80% 得出三个档位，统一重新执行完整矩阵。新输出将使用 multistream 名称，不与旧结果混合。

**未开始**

- P2-4/P2-5，CP2；P3（E3 · S4/S5）、P4（E4 · S6）、P5（E5 · S7）、P6（E2 · S3）、P7（控制台与收口）、CP3。

## 8. 下一步计划（按优先级）

1. **P2-3**：阅读本轮校准证据，按基线空载延迟与明确的 SLO 定出三个到达率档，补实验编排。四策略 × 两档读写比 × 三档到达率 × 至少五次交替重复，另做 B 关门控与直写 MySQL 对照；不能用只读校准替代正式结论。
2. 正式实验每轮须恢复相同商品基底、清理本项目缓存并固定服务/发压二进制版本；先实现安全、可复现的恢复步骤，不能对共享 Redis 做 FLUSHALL。记录热点事件时间线、服务端直方图和 pprof，完成 S1 报告。
3. **P2-4/P2-5**：binlog 消费进程 + Redis Stream + 双实例 + 故障注入，然后跑 S2 并出报告。
4. P2 收尾后在 **CP2 停下**，提交 S1/S2 报告等用户审阅。

Mac 校准命令（在 tx 的本仓库运行，要求工作区干净，输出路径不可已存在）：

```bash
python3 scripts/calibrate-s1-mac.py --output .local/s1-calibration-<日期>
```

此脚本从 tx 交叉构建带提交版本的 Darwin 发压器，复制到真实 `luomac`，由 Mac 临时建立到 `tx` 的 SSH 转发。验证 popularity 哈希并临时提高子进程 nofile，退出时关闭隧道。不恢复表、不切策略、不清缓存、不写商品；标记故事 `S1-calibration`，结果只能用于联调。正式测量不要与全仓测试、构建或其他压测同时运行。

正式 S1 控制器状态（2026-10-26 续接）：

- 私有运行状态：`.local/s1-suite/`（凭据勿外传）；`prepared.json`/`import.json` 已完成；`events.jsonl` 为追加事件日志，`probe*` 为全部探测原始结果，`provenance.json` 为每次构建版本。失败探测不进入性能比较；不要覆盖或删除。
- `bcl_s1.items`/`bcl_s1.items_base` 是独立实验副本。原 `bcl.items` 初检有 176 行版本不为 1，价格/库存汇总与 P1 不同，未动原表；独立副本按 P1 TSV 原始哈希导入、对账。
- 带 `bcl.experiment=s1` 标签的 `bcl-s1-redis`、`bcl-s1-e1` 是实验容器，后者由控制器停止/重建。共享栈保持不变。只对这台专用 Redis FLUSHDB。
- 控制器要求源码干净。首次准备已完成，**不要再次 --prepare**。使用新的输出目录跑探测，成功后才进入 matrix：

```bash
python3 scripts/s1-suite.py --phase probe --output .local/s1-suite/probe-<新名字>
python3 scripts/s1-suite.py --phase matrix --output .local/s1-suite/matrix-multistream-2026-10-27
```

- `design.json` 绑定源提交，变更源码后必须重新探测。Mac 是 `scripts/s1/mac-worker.py`，通过 stdin 接收控制指令，保持临时 SSH 转发，结束删除客户端凭据并关闭隧道。
- SSH 客户端曾在约 1024 通道时报 `poll: Invalid argument` 并退出；单隧道 384 连接又出现明显外部排队。当前四条独立业务 SSH 流各上限 256 HTTP 连接，管理/旁路 MySQL 独立第五条流（数据库池 64），全局在途 1536；固定热度前缀 128 个不同商品键只读预建。这个改动是测量链路调整，不能当作业务优化收益。门禁不允许正式失败/丢到达进入可比较结果。
- 服务 `/debug/strategy` 新增 `source` 字段；控制器要求 source=mysql，并读热点确认初始 version=1，避免漏配时测成内存源。

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

- 暂无待用户决定的事项。当前假设：CP1 已由“按方案开始”放行；P1 之后直接做 P2，到 CP2 再停。

## 12. 飞书文档

位置：我的文档库 › WorkPlan › backend-cloud-labs（2026-10-24 用户确认），原由 Claude 使用 profile `claude` 以用户身份写入；Codex 续写使用 profile `codex`、`--as user`，保留原创建来源。

- 项目概览（节点本身，Codex 2026-10-13，来源 b98f225）：https://kcn0pbn0hwo0.feishu.cn/wiki/PgE6wzqFbiUa6lktbhHcPqP0nDi
- backend-cloud-labs 方案（用户方案原文同步）：https://kcn0pbn0hwo0.feishu.cn/wiki/BnIcwD3TUiLXNHkEs9qcCxgdng6
- backend-cloud-labs 开发记录（每个阶段在文末追加一节，只追加不改旧内容）：https://kcn0pbn0hwo0.feishu.cn/wiki/AkEQwOqXTiyhbiklUqPcjUKfnXg

2026-10-26：Codex 已同步测量修复/真实 Mac 校准（revision 4）及隔离编排/137 次 Go 顶层回归/启动问题（revision 5），回读署名并回查确认仍在本项目节点下。连接预建/统计范围修正、138 次 Go 顶层回归已追加至 revision 6，并回查节点。revision 7 已同步恢复与启动；revision 8 已追加单隧道停止/多隧道调整并回读 139ac9f 署名、回查项目节点。共同门槛撤回及后续实验待同步。

## 13. 更新日志

- 2026-10-27 · Codex：用户恢复 Mac；取回旧 B 中断文件并登记无效，恢复实验基底；新增随任务生命周期的 Mac 保活与控制/传输/单轮超时，准备跨日独立重跑。

- 2026-10-26 · Codex：核对全部群历史与实际提交，纠正 P2-1/P2-2 的过期状态；修复 S1 采样/有效性门禁，补回归和真实 Mac 校准脚本。

- 2026-10-24 · Claude Code：飞书新建「backend-cloud-labs 方案」「backend-cloud-labs 开发记录」并回查位置。
- 2026-10-24 · Claude Code：按全局交接规范重写本文件（补概述、快速上手、目录、架构、进度、下一步、已知问题、待确认、飞书、日志）；记录 P1 完成、CMDB 密度定案、P2 开始。
- 2026-10-24 · Claude Code：P1 数据流水线完成并推送（b0e5aba、3cebe71），暂停交接见 docs/暂停交接-2026-10-24.md。
- 2026-10-20 · Codex：P0 完成，停在 CP1。
- 2026-10-13 · Codex：E5 CMDB 完成。
- 2026-10-09 · Codex：E1–E4 第五轮评审（缓存代际、E2 移动合并、E3 十进制语法、E4 未服务需求量）；证据 docs/measurements/2026-10-09-fifth-review.json。

2026-10-27 多隧道版本全仓验证：scripts/check.sh 退出 0，139 次 Go 顶层通过、4 次内存源下 MySQL 专项预期跳过，vet 通过、CTest 7/7；Python 归档测试 3 项通过。新链路真实探测待执行。

2026-10-27 探测补充：139ac9f 四流探测 A500/A1500、C500/C1500/C3000 有效；A3000 有丢到达/失败，不用于定档。C500 服务端 p99 68.5ms，C1500 为119.5ms，额外共同50ms门槛无达标点。核对原方案“由基线空载延迟与SLO定义”后撤回共同门槛，恢复原A基线规则；保留C负面证据，不放宽有效性。新提交重新探测，五次正式比较尚未完成。
