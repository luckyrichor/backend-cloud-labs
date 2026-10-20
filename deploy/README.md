# P0 实验环境

在 tx 仓库根目录运行，Go 模块各自独立，无 go.work。

```bash
python3 scripts/init-deploy.py
bash scripts/build-deploy.sh
BCL_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose -f deploy/compose.yaml build
 docker compose -f deploy/compose.yaml up -d --wait --wait-timeout 120
python3 scripts/smoke-deploy.py
python3 scripts/with-p0-env.py go -C tools/p0-probe run .
python3 scripts/with-p0-env.py go -C loadgen run ./cmd/loadgen --repo .. --selftest --output ../.local/p0/loadgen-selftest.json
```

先提交源码，构建和 loadgen 默认拒绝脏工作区。开发构建显式使用 `--development`，不作为正式测量证据。Dockerfile 接收 tx 本机静态二进制，记录实际编译器与二进制哈希；不是跨架构镜像。运行镜像使用非 root 用户。MySQL 初始化要求密钥可被容器 mysql 用户读取：宿主父目录 0700、单文件 0444，client-env.json 0600；不可输出其中内容。更换已有卷的密码需要单独迁移，不覆盖已有密钥。数据库仅用于本项目实验。

业务端口仅发布到 tx 的 127.0.0.1：E1 18086、E2 18082、E3 18083、E5 18085；管理端口分别 19086、19082、19083、19085。MySQL 13306，Redis 16379。默认单独启动服务仍监听原有 loopback 地址；管理 listener 默认也是 loopback。容器内因 bridge 转发绑定 0.0.0.0，但宿主发布始终 loopback；同 Docker 网络不是安全边界，不要把其他工作负载加入该网络。

E2 环境变量 `ROOM_PER_IP_CONNECTIONS`、`ROOM_PER_IP_IDENTITIES`、`ROOM_MAX_CONNECTIONS`、`ROOM_MAX_IDENTITIES` 默认分别 16、16、256、128。非法非正数启动失败。旧构造函数仍保持两种每 IP 配额联动的兼容语义。服务监听环境变量分别 GUIDE_ADDRESS、ROOM_ADDRESS、RULE_ADDRESS、CMDB_ADDRESS；管理端口使用 ADMIN_ADDRESS。

E3 HTTP POST /evaluate 调用现有 C++ AST 引擎独立进程，限制表达式/请求体/并发/执行时间。返回 parse_and_evaluate_ns 只包含子进程内解析和求值；不是 S4/S5 基准。E4 compare CSV 新增 schedule_ns，仅示例墙钟计时，不构成策略性能结论。

loadgen 是 P0 骨架：HTTP 或单次 TCP 消息、固定到达、并发上界、HdrHistogram 微秒原始快照；到达至完成延迟包含调度排队。超额请求记 dropped 并使结果 invalid；错误响应独立计数。它尚不是 P6 多连接轨迹机器人。bench_run 登记在运行后进行，结果 registry_status=registered 才代表成功；pending 表示未确认登记，不得纳入故事结论。当前 repeat_no=1，完整多轮实验编排在后续阶段实现。

## Mac 补采集（在 Mac 的本项目干净工作区执行）

```bash
git pull --ff-only
python3 scripts/collect-env.py --role mac --output .local/p0/env-mac.json
# 下列转发由 Mac 发起；tx 需要是已配置的 SSH 主机名
ssh -N -L 18086:127.0.0.1:18086 -L 18082:127.0.0.1:18082 -L 18083:127.0.0.1:18083 -L 18085:127.0.0.1:18085 tx
# 转发运行期间，另一个 Mac 终端执行
python3 scripts/collect-env.py --role mac --target 127.0.0.1 --ports 18086 18082 18083 18085 --samples 20 --output .local/p0/env-mac-tunnel.json
```

应对 tx 实际 SSH 主机地址执行 TCP 采样、ICMP 回显和应用层验证。当前 Mac 路由经过 utun5，TCP connect 快速成功但实际业务响应超时，因此只凭 connect 不判断服务可达。转发采样必须标记 SSH 隧道；不把 tx loopback 当作 Mac→tx 测量。2026-10-20 已通过 ssh luomac 补齐真实 Mac 采集及转发验证，证据见 docs/measurements/2026-10-20-p0-mac/，当前等 CP1 审阅。

Prometheus/Grafana 的部署决定见 P0 交付报告。禁止把预存 swap 推断为本栈造成的内存不足；保留栈前后内存与 swap 活动原始记录。

Prometheus 19190 与 Grafana 13100 同样仅供 loopback/SSH 转发访问。监控容器各限制 256 MiB，Prometheus 仅抓本项目四个管理端口，保留 2 天且不超过 256 MB；实际镜像 digest 与空载换页记录见正式证据。Grafana 管理口令位于私有 .local/deploy/grafana_admin_password；不要复制到报告或群聊。后续性能实验同机监控开销必须保持一致并注明。

推荐在 Mac 干净副本使用自动探测脚本，临时转发会选取空闲端口，并在 finally 中关闭：

```bash
python3 scripts/probe-mac-network.py --ssh-host tx --output .local/p0/network-mac-tx.json
```

该脚本验证实际服务响应，且保留本地 TCP 接入、ICMP 和路由的区别。不会启动 Docker，不修改用户系统设置。
