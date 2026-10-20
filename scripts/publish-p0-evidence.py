#!/usr/bin/env python3
"""Assemble existing measurements; never run a benchmark or invent missing Mac evidence."""
import collections,datetime,hashlib,json,re,shutil,subprocess
from pathlib import Path
# Run at repository root. All inputs are local, non-secret outputs from the documented commands.
root=Path.cwd();local=root/'.local/p0';commit=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
assert not subprocess.check_output(['git','status','--porcelain']), 'publish only after formal clean-source measurements finish'
env=json.loads((local/'env-tx-final.json').read_text());load=json.loads((local/'loadgen-selftest.json').read_text());assert env['git_commit']==load['git_commit']==commit
assert load['registry_status']=='registered'
checks=(local/'check-final.log').read_text();tests=collections.Counter();skips=[]
for line in checks.splitlines():
 try:r=json.loads(line)
 except ValueError:continue
 assert r.get('Action')!='fail', 'failed test output'
 if r.get('Test') and '/' not in r['Test']:
  if r['Action']=='pass':tests[r['Package']]+=1
  if r['Action']=='skip':skips.append(r['Test'])
assert not skips
match=re.search(r'100% tests passed, 0 tests failed out of (\d+)',checks);assert match
smoke=json.loads((local/'smoke.json').read_text());assert len(smoke['containers'])==8
canal=json.loads((local/'canal-final.json').read_text());assert canal['canal_row_event_received']
assert (local/'exprtk-final.txt').read_text().strip()=='5'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
meta={'generated_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_commit':commit,'working_tree_modified_before_measurements':False,'go_tests':dict(tests),'top_level_go_tests':sum(tests.values()),'skipped':skips,'ctest_count':int(match[1]),'hdr_version':'v1.4.0','canal_version':'v1.16.0','mysql_driver_version':'v1.10.1','exprtk_commit':'1e4a80b5ec9b4832ed59c6faa65f625a01b18ef0','exprtk_header_sha256':sha(root/'.local/third-party/exprtk/exprtk.hpp'),'exprtk_license_sha256':sha(root/'.local/third-party/exprtk/license.txt'),'exprtk_license':'MIT','binaries':{p.name:sha(p) for p in (root/'.local/bin').iterdir() if p.is_file()},'mac_evidence':'missing; no Linux substitute','P0_accepted':False,'next_phase_started':False,'scope':'P0 availability/calibration only; no S1-S7 performance conclusions'}
raw=subprocess.check_output(['docker','image','inspect','mysql:8.0','redis:7-alpine','busybox:1.37.0','prom/prometheus:latest','grafana/grafana:latest'],text=True)
# Whitelist fields; never serialize container environment/secrets.
meta['runtime_images']=[{'id':r['Id'],'repo_digests':r['RepoDigests']} for r in json.loads(raw)]
for c in smoke['containers']:
 if c['service'].startswith('e'):
  rev=subprocess.check_output(['docker','inspect','--format','{{index .Config.Labels "org.opencontainers.image.revision"}}','bcl-p0-'+c['service']+'-1'],text=True).strip();assert rev==commit,(c['service'],rev,commit)
meta['formal_commands']=['python3 scripts/collect-env.py --role tx --output .local/p0/env-tx-final.json','vmstat 1 11','bash scripts/build-deploy.sh','BCL_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose -f deploy/compose.yaml build','docker compose -f deploy/compose.yaml up -d --wait --wait-timeout 120','python3 scripts/smoke-deploy.py','bash scripts/check.sh','python3 scripts/with-p0-env.py go -C loadgen run ./cmd/loadgen --repo .. --selftest --output ../.local/p0/loadgen-selftest.json','python3 scripts/with-p0-env.py go -C tools/p0-probe run .','go -C experiments/e4-scheduler run ./cmd/compare','g++ -std=c++20 -O0 -I.local/third-party/exprtk tools/exprtk-probe.cpp -o .local/third-party/exprtk/probe']
out=root/'docs/measurements/2026-10-20-p0';out.mkdir(parents=True,exist_ok=True)
files=['env-tx-initial.json','env-tx-final.json','idle-vmstat.txt','idle-monitoring-vmstat.txt','check-final.log','smoke.json','loadgen-selftest.json','canal-final.json','e4-timing.csv','exprtk-final-build.log','exprtk-final.txt','mysql-auth.txt','registry.txt','monitoring-stats.json','prom-targets.json']
for name in files:shutil.copyfile(local/name,out/name)
meta['artifact_sha256']={p.name:sha(p) for p in out.iterdir() if p.is_file()}
(out/'summary.json').write_text(json.dumps(meta,ensure_ascii=False,indent=2)+'\n')
data=json.loads((root/'datasets/manifest.json').read_text())['datasets']
rows=['# backend-cloud-labs P0 tx 侧交付报告','',f'日期：{datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()}（北京时间）；执行：Codex。源码：`{commit}`，正式测量前工作区干净。','', '**结论：tx 可独立完成的 P0 实现和验证已完成；Mac 环境与 Mac→tx 网络证据未取得，因此 P0 尚未验收，未进入 P1。**','', '## 本轮交付','', '| 项目 | 实测结果 |','| --- | --- |',f'| 回归 | {sum(tests.values())} 项 Go 顶层测试，race + vet；跳过 {len(skips)}；CTest {match[1]}/{match[1]}；含真实 Redis 用例 |',f'| 容器 | {len(smoke["containers"])} 个全部 healthy；MySQL、Redis、E1/E2/E3/E5、Prometheus、Grafana；cpuset/nofile/内存限制已核对 |',f'| binlog | MySQL {canal["mysql_version"]} 默认 caching_sha2_password；canal v1.16.0 收到实际 INSERT 的 ROW/FULL 事件；无需 outbox 回退 |','| ExprTk | 固定提交 1e4a80b5ec9b4832ed59c6faa65f625a01b18ef0，MIT；编译和 x*2+1=5 运行通过 |','| 可观测 | 四个管理端口可读直方图及 pprof，Prometheus 四目标 up；业务 HTTP 路由不暴露管理端点 |','', '## 开环压测自检（仅客户端校准）','', '| 场景 | 计划/发送/失败 | 实际发送率 | 偏差 | p99 |','| --- | --- | --- | --- | --- |']
for phase,x in load['results'].items():rows.append(f'| {phase} | {x["Scheduled"]}/{x["Sent"]}/{x["Failed"]} | {x["ObservedSendRate"]:g} req/s | {100*x["SendRateDeviation"]:.3f}% | {x["ResponseLatency"]["p99_us"]/1000:.3f} ms |')
rows+=['','暂停场景服务端停顿 1 秒，p99 反映停顿。原始 HdrHistogram、调度延迟与失败/丢弃计数已保存；MySQL bench_run 已登记。这不是 S1–S7 性能结果，后续仍需按方案做完整对照、多轮和正确性验证。','', '## 数据下载核实','', '| 来源 | 文件字节 | 数据行数 | 结果 |','| --- | --- | --- | --- |']
for d in data:rows.append(f'| {d["id"]} | {d["bytes"]:,} | {d["rows"]:,} | {"SHA256/结构校验通过" if d["verified"] else "失败"} |')
rows+=['','Azure 的 ZIP、SQLite 哈希与原方案一致。阿里为两个采样分片，已扣表头。Amazon 旧地址 404，现行官方地址成功；未启用合成目录回退。数据与源站许可/说明副本保留仓库外，manifest 记录来源、原文链接和哈希；许可不明确仍如实标注，原始数据不入库。原方案保持原样，其附录 C 的当前核实更新见 manifest。','', '## 环境与监控','', '核心栈之前已有 swap 占用；核心栈和含监控栈分别采集空载 vmstat，具体区间见附件，不能把历史占用归因为本栈。监控各限制 256 MiB，实际内存与换页记录见 monitoring-stats.json/idle-monitoring-vmstat.txt。Prometheus/Grafana 已部署，无需申请“无监控”回退。所有宿主端口均仅发布 loopback；Prometheus 19190、Grafana 13100，管理凭据只在私有文件。进程 TZ 与宿主时区分别记录，测量时间使用 UTC。','', '## 失败、修复与边界','', '- E2 初版拆开配额破坏旧构造函数联动语义：恢复兼容，保留显式新配置。','- MySQL secret 读权限和临时服务器误报健康：修复权限，健康检查改正式 TCP 数据表查询，实际认证与 binlog 复核通过。','- 19090 已占用：保留其他进程，改用已探测空闲端口；运行镜像按实际 digest 固定。','- E3 HTTP 是调用现有 C++ AST 的受限演示，非吞吐优化；E4 仅补计时输出。E1 未加入 MySQL 商品源，E5 未加入 MySQL 事件存储，均属后续阶段。','', '## 仍需负责人提供/确认','', '1. 在 Mac 的本仓库干净副本按 deploy/README.md 采集环境、实际 tx 地址的直连 TCP/往返时延和 SSH 转发端口可达性，回传 JSON；或提供可用 Mac SSH 连接名。tx loopback 不能替代此证据。','2. 补齐 Mac 证据后提交 CP1 审阅，再由负责人决定进入 P1。目前不提前跨阶段。','', '## 可追溯文件','', '- `datasets/manifest.json`：完整下载、SHA256、许可原文来源与行数。','- `docs/measurements/2026-10-20-p0/summary.json`：源码、实际依赖/镜像/二进制哈希、测试统计和逐件证据 SHA256。','- 同目录：环境、全量测试日志、容器/端口、回显/暂停直方图、binlog、ExprTk、E4、监控与登记证据。','- `deploy/README.md`：复现命令和 Mac 补采集步骤。','']
(root/'docs/P0-tx交付报告-2026-10-20.md').write_text('\n'.join(rows))
print('Evidence and report assembled from actual artifacts:',commit)
