# S1 v2补测证据（登记目录沿用2026-10-28）

正式测量2026-10-29、2026-10-31，整理2026-11-01。正式源码a00e15101716f21cd674662e74210130719a0bcc；旧探针来源35960f1/cfe45c7另列，不能混入正式比较。原始文件3142个、278次完成尝试（250正式+28探针）、压缩原始字节222656650。无效/中断和不同源探针均保留，不改变原valid。

- `manifest.json`：压缩文件及原始解压字节SHA256；raw是无损原始证据。完整trace去重，仅额外保存未重复的中断trace。
- `attempts.json`：每次完成尝试的client/truth/source质量；raw内incomplete及traces另有中断现场，不计完成轮数。
- `raw/quality.json.gz`：8个条件的前5共同有效ID，205选中轮。250正式=226质量有效/24无效；并非226都进入完整对照。
- `recomputed.json.gz`：公开归档重算250轮全key真值与原HDR分位数；共41格。
- `groups.csv`：客户端/服务端GET与PUT的p50/p95/p99逐轮中位数及范围、成功率、A.2陈旧计数/分母、源QPS、完整行/版本查询数、命中率。
- `tables.md`：从recomputed生成的人类可读表。报告保留首轮结论，见[新版报告](../../S1缓存策略对照报告-v2-2026-11-01.md)。

公开复核不依赖数据库、Mac、私有env或原.local：

```bash
# 在本目录核验文件，再回到仓库根目录执行重算
sha256sum -c SHA256SUMS
# 仓库根目录：
go -C loadgen build -o /tmp/bcl-s1summary ./cmd/s1summary
python3 scripts/p2/rebuild_s1_v2.py docs/measurements/2026-10-28-s1-v2 --summary-binary /tmp/bcl-s1summary --output /tmp/bcl-s1-v2-rebuilt
cmp /tmp/bcl-s1-v2-rebuilt/recomputed.json.gz docs/measurements/2026-10-28-s1-v2/recomputed.json.gz
python3 scripts/p2/s1_tables.py /tmp/bcl-s1-v2-rebuilt/recomputed.json.gz --output /tmp/bcl-s1-v2-rebuilt
cmp /tmp/bcl-s1-v2-rebuilt/groups.csv docs/measurements/2026-10-28-s1-v2/groups.csv
```

所有实际请求均记日志，实际每轮访问约6700–7000个不同key，不表示访问了目录全部200万key。计时是服务端/直写端同机CLOCK_MONOTONIC；Mac仅为负载客户端，客户端ACK下界保留但不作A.2真值。

完整性：末轮活动表恢复指纹见raw/measurement-finished.json.gz。服务端mode0600原始日志通过sudo只读复制，不扩权限。排除运行env、私有JSON/DSN、可执行文件、数据集和凭据。

`registry-paths.json`列278条客户端结果别名：顶层各phase的`.json.gz`与raw中对应文件逐字节一致，保留测量时bench_run登记的recorded-path可达；它们不算新增尝试或独立原始证据。
