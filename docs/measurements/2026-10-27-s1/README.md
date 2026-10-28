# S1 原始证据与重算（2026-10-27，已完成）

[完整报告](../../S1缓存策略对照报告-2026-10-27.md)。测量源码固定 `d2ca677db32a17eaed9aff776244262b88d42801`；业务、发压、依赖和编排关键代码相对该版本无差异。文档与分析工具另行提交。

## 范围与索引

| 输入 | 完成 | 有效 | 无效 | 用途 |
| --- | --- | --- | --- | --- |
| Mac主对照含整组补跑 | 144 | 137 | 7 | 95:5/99:1 × 100/250/400 × A/B/C/D |
| 反向验证 | 15 | 15 | 0 | A/B-off/B |
| 服务外直写MySQL | 24 | 23 | 1 | A/B/C/D |
| 独立剖析 | 3 | 3 | 0 | CPU/mutex/goroutine，排除正式性能比较 |
| tx本机校准 | 20 | 20 | 0 | 单列校准，不混入Mac主结果 |

共206次完成、198有效、8无效，各比较至少5个共同有效重复编号。另有1次用户暂停中断，部分记录独立保存，未计入有效/完成。暂停前161次的快照保存在paused-state.json；它是历史状态，最终状态见completion.json。用户随后明确要求继续，同源恢复补跑。

- `matrix-idle-2026-10-27/`：Mac主矩阵、反向、直写和独立剖析；原始result JSON无改写压为.json.gz，CSV/trace本来就是gzip；其余worker/container/correctness/done/network/service元数据保留。
- `tx-calibration-2026-10-27/`：同源Linux客户端，CPU3、单loopback目标和1024连接；客户端硬件/拓扑不同，结果仅用于诊断。
- `analysis.json`、`groups.csv`、`quality.csv`：全部尝试的质量统计，全部有效尝试的描述统计，以及共同有效重复的比较。延迟单位μs，比例为0–1，回源QPS分母包含实际排空/采集时间。
- `trace-histograms.json.gz`：41个C热点诊断组，37771次完整读区间，含连接预建/预热；不是正式窗口全目录分位数。
- `metadata/`：固定版本、二进制/镜像哈希、数据基底、主/补跑/本机环境、设计、最终补跑计划及独立pprof文本摘要。原始9个pprof在matrix目录。
- `registry.json`：280条登记及原始JSON SHA256，登记路径全部有对应归档，无缺失旧记录；其中74条是正式输入以外的探测/先导诊断，不混入统计。
- `trace-integrity.json`：单实例边界检查。旧版本trace有跨尝试混合风险，不能用来定位竞态；当前固定源trace边界均为1。正式时序证据只取有效的固定源运行。

同目录其他probe/matrix文件夹及相邻2026-10-26-s1均为按原登记路径保留的前置诊断，涉及旧源码、旧隧道、预热计数口径或连接问题；不作为本次优化前后比较。旧raw valid字段不做事后修改。

## 重算

仓库根目录、Go1.27.1及Python3环境：

```bash
python3 docs/measurements/2026-10-27-s1/recompute.py --out .local/s1-recomputed
diff -u docs/measurements/2026-10-27-s1/analysis.json .local/s1-recomputed/analysis.json
sha256sum -c docs/measurements/2026-10-27-s1/SHA256SUMS
```

脚本临时解压JSON，构建本仓库s1summary重算HdrHistogram分位数，再从CSV统计预热/正式错误；核验源审计、source=mysql、策略、门控开关和60秒TTL。全部有效样本用于描述统计，结论只用双方共同有效重复编号。至少5个共同编号、相对变化>10%且min/max不重叠才按实验规则记显著；零基线不定义相对提升。该规则不是p值或置信区间。

热点诊断直方图重算（输出JSON与gzip内的JSON比较）：

```bash
go -C loadgen run ../docs/measurements/2026-10-27-s1/control/tracehdr.go ../docs/measurements/2026-10-27-s1/matrix-idle-2026-10-27 ../docs/measurements/2026-10-27-s1/tx-calibration-2026-10-27 > .local/s1-trace-recomputed.json
```

`control/`保存实际控制和分析程序：supplement.py按有效性整组补跑；tx-calibration.py独立校准；tracehdr.go从同进程事件做诊断HdrHistogram；find-race.py定位A晚回填；archive-evidence.py白名单无损归档并扫描已知凭据值。write-report.py为本次固定资料的报告构建器，部分定位说明为已核实的文字事实，不用于给新实验自动下结论。control/supplement-rounds.json是暂停快照，最终完成计划以metadata/supplement-rounds.json为准。

重跑性能实验需要独立环境、现有私有配置和数据；本次已经完成，不应直接覆盖同名输出或再次prepare现存数据库。所有临时客户端凭据已清理，服务器永久私有配置未发布。

最终回归：139次Go顶层执行通过、4次内存源下MySQL专项预期跳过（真实MySQL源另跑通过），vet通过，CTest7/7。详细定位限制与下一步S2见报告。
