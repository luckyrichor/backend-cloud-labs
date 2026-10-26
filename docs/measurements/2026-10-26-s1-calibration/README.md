# 2026-10-26 S1 工具修复与真实 Mac 只读校准

Codex，2026-10-26（北京时间）。发压器与全仓回归源码：`3b6f980760634d6a17b9c0493cb7d3ba0dfa436a`，采集启动时工作区干净。实际已部署 E1 二进制的源提交未核实，二进制与镜像哈希见 service.json。因此本目录仅验证发压与采集链路，不作为 S1 正式策略报告。

## 已完成

- 修复 loadgen 的失败请求/缺失指标被当成有效、预热慢请求混进正式直方图、到达率分母与完整窗口的处理；增加可解释的 invalid_reasons。
- `scripts/check.sh` 退出码 0：136 次 Go 顶层测试通过、4 次预期跳过（MySQL 专项在内存源那轮跳过，在 MySQL 那轮均通过），包含子测试则 151 次通过；各模块 vet 通过，CTest 7/7。两次数据源运行会重复执行 E1 测试，136 不是独立测试数。见 check-summary.json、check.log.gz。
- 真实 luomac（Darwin arm64）发起请求，由临时 SSH 隧道到 tx 的 loopback。发压器在 tx 交叉编译并写入源提交，Mac 不需安装依赖/升级 Go/启动 Docker；子进程临时提高 nofile，隧道在 finally 中关闭。
- 三档到达率各预热 3 秒、正式窗口 10 秒，只有 GET，无商品写入，不切策略、不重建服务、不清缓存。校验了 200 万条 popularity 索引文件与 P1 清单的哈希一致，实际服务策略 cache-aside、TTL 1 分钟。

## 观测值及限制

| 到达率 req/s | 正式读请求 | 成功/失败/丢到达 | 客户端 p99 ms | 服务端 p99 ms |
|---:|---:|---|---:|---:|
| 50 | 500 | 500 / 0 / 0 | 216.447 | 3.035 |
| 200 | 2000 | 2000 / 0 / 0 | 407.551 | 10.703 |
| 500 | 5000 | 5000 / 0 / 0 | 103.359 | 13.167 |

三次 observed_window_send_rate 分别恰为 50/200/500，服务端 GET 直方图计数分别等于正式读请求数。全部有效，仅表示工具门禁通过，不表示策略收益显著。无写流量时 stale=0 无法证明缓存一致性。每档只跑一次、依次进行且未恢复缓存，不能比较到达率与延迟的因果关系，更不能拿这些缓存命中率比较策略。客户端 p99 包含计划到达、调度、SSH/网络与服务端处理；其与服务端差距需要后续定位，不能全部归因于网络。

最初有一轮联调与全仓检查的时间相邻，其空载 HTTP 采样有测试干扰；保留在 tx `.local/s1-calibration-2026-10-26/`，未用于上述表格。上表来自全仓检查结束后的独立重跑 `.local/s1-calibration-2026-10-26-isolated/`。不据此宣称缓存策略之间的性能差异。

## 文件与重现

- `read-only-*.json.gz`：未改写的 loadgen 输出，客户端/服务端原始 HdrHistogram、指标差值及配置。
- `read-only-*.csv.gz`：每个请求的计划/实际发送/完成时间（同一客户端单调时钟）、版本、状态、来源。
- `summary.json`：从上述原始 JSON 导出的摘要；`summarize.go` 为导出程序。
- `environment.json`：真实 Mac、有效 nofile、二进制/索引哈希、100 次空载 HTTP 管理请求往返原值。HTTP 往返包含连接建立及服务端处理，不是纯链路 RTT。
- `tx-environment.json`：源码干净时采集的 tx 环境。
- `registry.json`：已写入 MySQL bench_run 的三次 S1-calibration 登记，路径指向本目录压缩 JSON。
- `service.json`：实际部署镜像 ID、服务二进制哈希和未核实的源版本边界。
- `sha256.json`：本目录文件哈希（不包括清单自身）。

从 tx 本仓库干净工作区执行新一轮联调（输出目录不能已存在）：

```bash
python3 scripts/calibrate-s1-mac.py --output .local/s1-calibration-new
```

用 Go 的 HdrHistogram 库重算表格（先把三份 JSON 解压到 gitignore 的本地目录）：

```bash
mkdir -p .local/s1-recompute
python3 - <<'PY'
import gzip
from pathlib import Path
for p in Path('docs/measurements/2026-10-26-s1-calibration').glob('read-only-*.json.gz'):
    Path('.local/s1-recompute', p.stem).write_bytes(gzip.decompress(p.read_bytes()))
PY
go -C loadgen run ../docs/measurements/2026-10-26-s1-calibration/summarize.go ../.local/s1-recompute
```

## 继续开发

P2-3 尚未完成。下一步先补验证过版本的服务部署、表恢复与本项目缓存隔离、SLO 与三个正式率档的选取，再跑四策略 × 两个读写比 × 三档率 × 五次交替重复，补 B 关闭门控、绕过服务写 MySQL、热点事件时间线、pprof 和完整报告。正式窗口按 p99 样本量选定；本次 10 秒只用于联调。后续 P2-4/P2-5 为 binlog/Redis Stream/双实例及 S2，CP2 在 P2 完整交付后仍须停下等用户审阅。
