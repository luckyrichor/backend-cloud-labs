# 进度记录

最后更新：2026-10-01（北京时间）

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
# 2026-10-02 W5–W7（Codex）

来源 backend-cloud-labs@8519ae7 + 本轮工作树修改。W5 独立维护：E1 推荐去重、按价格/ID 排序，增加回归。W6 E2：独立 module，TCP JSON 长连接、权威帧循环、同帧广播、随机恢复凭据、重连恢复、输入去重。W7 独立维护：旧连接输入失效、非法握手拒绝、慢队列隔离和关闭时回收 worker 的验证。

`sg docker -c 'bash scripts/bootstrap.sh'`：E1 **11 tests**（实际临时 Redis，无 skip）、E2 **3 tests**，全部 go test -race -count=1 通过，go vet 通过；gofmt 已执行。Redis 临时容器自动清理，未动常驻服务。E2 默认50ms帧，本次测试用2ms/1ms有界驱动，无生产容量声明。E1源/房间状态仍在内存；E3–E5未开工。E2接口/边界见实验README。无实际工时声明，以下为历史记录。
