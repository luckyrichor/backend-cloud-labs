# S2 原始证据与重算

测量发生于2026-10-28；报告及归档整理于2026-10-29（北京时间）。正式测量代码 `fdccc715f37d30bd9bad0483e687977f9b20aee0`；报告在 `docs/S2跨实例一致性窗口报告-2026-10-29.md`。归档整理/重算脚本和S1补测脚本位于当前归档提交；测试验证对应这次归档提交的工作树，并非重新跑过S2。

## 文件范围

- `raw/s2-matrix-v5/`：50轮最终正式矩阵、原始请求流、全key读/SQL提交日志、运行时配置、直方图快照及quality.json。
- `raw/s2-faults-v5/`：四类各5次，20次故障验证的全部日志与断言结果。
- 其余 `raw/s2-*`：校准、旧观察器、无效/中断尝试，不用于最终性能比较。每个原始valid字段完整保留；`attempts.json`另外标出最终eligibility，不事后修改valid。
- `manifest.json`：1837个白名单原始文件的压缩/解压SHA256、字节数；252次有结果的诊断/正式尝试，最终70次（50矩阵+20故障）。未完成尝试无结果文件，仍保留其运行日志。
- `histograms.json.gz`、`recomputed.json.gz`、`stage-histograms.json.gz`：从公开归档重建，与本机原始汇总逐字节一致。
- `source-provenance.json`：正式测量二进制来源与SHA；`verification.json`/`.log.gz`：归档后S1新增计数器/脚本的检查，158次Go顶层通过、6次预期跳过、真实MySQL另跑通过、race/vet、CTest7/7、Python8/8。
- `SHA256SUMS`：本目录全部交付文件的SHA，不包括其自身。

没有归档环境文件、DSN文件、凭据、二进制或Redis AOF。白名单原始文件逐个检查已知实验凭据未出现，归档保留必要的loopback/容器配置元数据。JSONL/log/JSON为无损gzip，原`.gz`字节不变；mtime=0避免压缩时间影响重算。

## 核验及公开重算

从项目根目录执行（需要Go和Python3，不需要数据库、Redis、SSH或Mac；不会发压）：

```bash
(cd docs/measurements/2026-10-28-s2 && sha256sum -c SHA256SUMS)
python3 scripts/p2/rebuild_s2.py docs/measurements/2026-10-28-s2 --output .local/s2-public-check
cmp docs/measurements/2026-10-28-s2/histograms.json.gz .local/s2-public-check/histograms.json.gz
cmp docs/measurements/2026-10-28-s2/recomputed.json.gz .local/s2-public-check/recomputed.json.gz
cmp docs/measurements/2026-10-28-s2/stage-histograms.json.gz .local/s2-public-check/stage-histograms.json.gz
```

`verify_s2.py`独立从服务端事件核对6100个提交、732000个轮询，重算最后旧值/首次新值的上下界，检查同boot单调时钟、SQL/HTTP及读区间、版本匹配、首次新值后回退；不只是重新读取汇总分位数。阶段时延保留`commit_to_observed_signed_us`，允许观察早于提交返回，不强加串行假设。HdrHistogram统一1us–600s、3位有效数字；零值数量另列，不把直方图1us下限当作实测正延迟。

重算输出：50轮、6100目标、732000轮询、0次首次新值后回退；实际最大轮询间隔28.275099ms。所有轮询次数一样（每目标120次），固定计划间隔5ms；真实调度误差和上下界见报告。不能把5ms计划当成每次测量的误差硬上限。

## 不再执行的命令

`archive_s2.py`依赖本机私有运行目录，是本次封存工具；普通读者只用上述公开重算命令。`scripts/p2/s2.py`及`faults.py`会启动实验服务/写入独立实验表，不是本归档的复核必要条件。本次没有改公网绑定、开放新端口或更新共享`bcl.items`。
