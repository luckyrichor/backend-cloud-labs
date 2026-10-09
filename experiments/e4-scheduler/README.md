# E4：简化资源调度器（W10）

Codex，2026-10-08。独立 Go module，CPU/内存双资源、确定性输入顺序。

- spread：优先已有CPU/内存占用率之和最小的可放置节点。
- binpack：优先占用率之和最大的可放置节点，尽量保留空节点。
- preempt：先spread（兼容旧策略名），放不下时仅驱逐严格低优先级任务，成功容纳才提交驱逐。
  每节点按优先级逐个选择候选，选这些方案中驱逐数量最少的节点；
  这是贪心近似，不保证在所有子集中找到最优驱逐集合。

实际对比负载：两节点各8CPU/8内存，两低优先任务各4/4，再到一个高优先8/8。
spread拒绝紧急任务；binpack容纳3项；preempt容纳紧急任务并驱逐1项，被驱逐任务迁移至另一节点，最终容纳3项。旧CSV是未重排的历史版本。
CSV见 `docs/measurements/w10-scheduler.csv`。不同策略均不超过容量；
对比揭示空闲资源分布对后续大任务的影响，不声称装箱一直最好。

```bash
cd experiments/e4-scheduler
go test -race ./...
go vet ./...
go run ./cmd/compare
```

这是单进程到达序列实验；无Kubernetes API、跨进程状态、真实节点故障恢复，
不代表生产调度器。基线backend-cloud-labs@68f0767 + 本轮工作树修改。


## 2026-10-08：重排与增量账本

- `preempt-binpack`：正常放置使用binpack，放不下时启用同一抢占逻辑。在贪心候选中选驱逐数量更少的节点，平局按节点输入顺序；不声称最优victim集合。
- 被驱逐任务进入 `Pending`，每个新任务到达处理完后按优先级降序、同优先级原队列顺序重试。重排只放入空闲资源、不再抢占，避免互相驱逐振荡；没有空间则保留Pending。
- `Preempted` 是驱逐历史（任务可能之后恢复，重复被驱逐时可重复记录），不是最终未运行列表；`Rejected` 是新到达且未接纳的任务，本接口不自动重试这类任务。`Placements` 是最终驻留，`Pending` 是被驱逐后仍等待的任务。每个输入ID恰处于驻留/拒绝/等待之一。
- `Usage` 是按节点增量资源账本。放置加资源、提交驱逐减资源；失败抢占只在局部副本试算，不修改账本。正常放置扫描节点，不再重新扫描全部驻留任务计算占用。独立全量重算仅在测试中校验账本。

相同到达序列重新对比：spread驻留2/拒绝1；binpack驻留3/驱逐0；preempt驻留3/驱逐1/等待0；preempt-binpack驻留3/驱逐0。原preempt回归期待驻留2，新增重排后首轮失败，更新为新的驻留3契约；旧历史测量保留。结果见 `docs/measurements/2026-10-08-e4-requeue.csv`。

7项测试通过 `go test -race -count=1 -v ./...`，go vet通过。新增空闲节点迁移、多层优先级抢占无振荡、80个随机任务所有81个到达前缀×4策略的资源/ID守恒校验。前缀测试独立重算CPU与内存，不用增量账本自我校验。

32节点binpack整批调度benchmark（3轮，含结果/校验输入与分配）：1000任务中位0.337ms，10000任务中位4.035ms。原始ns/op、B/op、allocs/op与设备见 `docs/measurements/2026-10-08-e4-incremental.txt`；不是单任务p95，也没有旧实现同期测速，不宣称已量化加速比。运行：

```bash
go test -run '^$' -bench BenchmarkIncrementalPlacement -benchmem -count=3
```

这是一次性离线调度函数：没有任务完成事件、持续控制循环、持久队列或节点变化；最终Pending需调用者在下一次资源变化后重新提交。每次到达都会重扫Pending，抢占候选仍扫描驻留任务并排序，不能声称整个调度器线性复杂度或Kubernetes级恢复能力。输入顺序影响结果，没有饥饿预防或全局配额。


## 同优先级的资源考量（2026-10-08）

新增失败回归确认旧方案：容量10，先到2单位小任务、再到8单位大任务，同优先级；8单位高优先级新任务触发时会先驱逐小任务再驱逐大任务（2次），小任务随后虽重排成功，仍产生不必要驱逐。

现在保留低优先级先驱逐；同优先级按对本次CPU/内存缺口的有效贡献降序，贡献各维最多1，平局稳定沿用到达顺序。该用例只驱逐大任务（1次）。不把不同资源维度直接按裸数相加，也不越过优先级约束。8项测试race通过，包括独立账本/任务守恒oracle。

这是固定初始缺口评分的贪心启发式，不保证最少受害者或最小浪费；多资源组合仍可能次优。候选扫描、排序和Pending重试仍存在，增量账本只消除了普通放置的占用重算，没有消除所有规模瓶颈。


## 三场景对照（2026-10-09）

compare现输出scenario列及cpu_used；保留packing旧场景，并增加full_nodes与packing_and_preemption。原CSV保留旧来源；新结果见 ../../docs/measurements/2026-10-09-e4-scenarios.csv。

- packing：binpack可直接接纳，无需驱逐；说明装箱收益。
- full_nodes：低优先任务占满两节点；binpack拒绝紧急任务，两种preempt接纳且驱逐1个；说明驱逐收益，不作为组合独有收益。
- packing_and_preemption：2/6/2/6单位低优先任务后到8单位紧急任务。binpack使用16资源但拒绝紧急；preempt接纳后使用12资源、驱逐1个；preempt-binpack接纳后使用16资源、驱逐2个，Pending有2个。这说明本到达序列中组合可保留更多驻留资源，代价是更多驱逐，并非所有指标都更优。

9项顶层测试race通过，组合场景回归同时核对接纳、占用和驱逐。module路径同步experiments，抢占仍为贪心离线实验。

## 打断资源量

Result.InterruptedResources()分别累计Preempted事件的CPU和Memory，多次驱逐同任务会重复累计；不把Rejected计为已运行任务打断。它不测已消耗的CPU时间、丢失进度或实际恢复成本，也不把两种单位相加成总分。新CSV见 ../../docs/measurements/2026-10-09-e4-interruption.csv；组合场景preempt打断6 CPU/6 Memory并拒绝1项，preempt-binpack打断8/8且拒绝0项。组合有更多驻留量，也承担更多打断，不能只靠驱逐次数论优劣。

## 未服务的需求量（第五轮）

Result.UnservedResources()按最终Rejected+Pending分别累加CPU和Memory，不累加历史Preempted，避免同任务多次驱逐重复计入未服务需求。组合场景preempt未服务12 CPU/12 Memory，preempt-binpack为8/8；同时仍报告打断6/6与8/8。数据见 ../../docs/measurements/2026-10-09-e4-unserved.csv；两类资源不混成一个总分。
