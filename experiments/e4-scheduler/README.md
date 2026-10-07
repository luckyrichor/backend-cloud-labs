# E4：简化资源调度器（W10）

Codex，2026-10-07。独立 Go module，CPU/内存双资源、确定性输入顺序。

- spread：优先已有CPU/内存占用率之和最小的可放置节点。
- binpack：优先占用率之和最大的可放置节点，尽量保留空节点。
- preempt：先spread，放不下时仅驱逐严格低优先级任务，成功容纳才提交驱逐。
  每节点按优先级逐个选择候选，选这些方案中驱逐数量最少的节点；
  这是贪心近似，不保证在所有子集中找到最优驱逐集合。

实际对比负载：两节点各8CPU/8内存，两低优先任务各4/4，再到一个高优先8/8。
spread拒绝紧急任务；binpack容纳3项；preempt容纳紧急任务并驱逐1项，另留1项。
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
