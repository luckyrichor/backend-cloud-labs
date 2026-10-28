# S1网络与陈旧读口径调查（2026-10-28，北京时间）

只读调查；未改业务、部署或压测器代码，未启动S1/S2正式压测，未修改防火墙、代理、端口发布或云配置。仅新增本目录证据。历史分析源码为d2ca677，调查开始仓库HEAD为aec7d91。没有读取或输出云API凭据，云安全组未核实。本次是一次性调查，不更新开发阶段状态。

## 1. 可达性、监听与防火墙

**事实：当前Mac不能直接访问E1/E2/E5或S1实验服务端口。公网22/80/443可以完成对应应用协议；公网80和Tailscale80可以作为不用SSH转发的RTT对照。** TCP connect成功不是应用可达证明。公网和10.0.0.15都走Mac utun5；Tailscale100.87.80.92走utun4。未关闭或改变这些路由，本报告“直连”仅指不经过SSH转发，不表示绕过TUN/代理的物理链路。

当前tx身份VM-0-15-ubuntu，Mac身份luomac.local。逐端口、四种地址的原始记录在[mac-results.jsonl](mac-results.jsonl)首行，TLS/Redis补充在[mac-additional-protocol-probes.json](mac-additional-protocol-probes.json)。超时2秒，每个目标一次，只能证明本轮观测，不能证明永远不可达。

地址：公网IPv4 43.142.37.199；VPC 10.0.0.15；Tailscale 100.87.80.92；公网IPv6 2402:4e00:c032:5f00:6cf0:2914:5a6c:0。下表由原始数据逐端口生成。“HTTP超时”包括TCP已被本地路径接纳但没有远端HTTP响应，不能把它标成开放；E2原生JSON端口若只回非HTTP数据同样不能据HTTP超时判协议不通，但本轮Tailscale实测为拒绝、tx也无宿主监听。

| TCP端口 | 公网IPv4 | VPC内网 | Tailscale | 公网IPv6 |
|---|---|---|---|---|
| 22 | SSH banner | 超时 | SSH banner | SSH banner |
| 80 | 404 Not Found | 超时 | 404 Not Found | 超时 |
| 443 | 401 Unauthorized | 超时 | 401 Unauthorized | 超时 |
| 8082 | 超时 | 超时 | 拒绝 | 超时 |
| 8085 | 超时 | 超时 | 拒绝 | 超时 |
| 8086 | 超时 | 超时 | 拒绝 | 超时 |
| 9082 | 超时 | 超时 | 拒绝 | 超时 |
| 9085 | 超时 | 超时 | 拒绝 | 超时 |
| 9086 | 超时 | 超时 | 拒绝 | 超时 |
| 13100 | 超时 | 超时 | 拒绝 | 超时 |
| 13306 | 超时 | 超时 | 拒绝 | 超时 |
| 16379 | 超时 | 超时 | 拒绝 | 超时 |
| 18082 | 超时 | 超时 | 拒绝 | 超时 |
| 18083 | 超时 | 超时 | 拒绝 | 超时 |
| 18085 | 超时 | 超时 | 拒绝 | 超时 |
| 18086 | 超时 | 超时 | 拒绝 | 超时 |
| 18096 | 超时 | 超时 | 拒绝 | 超时 |
| 19082 | 超时 | 超时 | 拒绝 | 超时 |
| 19083 | 超时 | 超时 | 拒绝 | 超时 |
| 19085 | 超时 | 超时 | 拒绝 | 超时 |
| 19086 | 超时 | 超时 | 拒绝 | 超时 |
| 19096 | 超时 | 超时 | 拒绝 | 超时 |
| 19190 | 超时 | 超时 | 拒绝 | 超时 |

证据命令与结果：

- `ss -lnt`、`docker ps --format '{{.Names}} {{.Ports}}'`、`docker inspect ... --format '{{.Name}} {{json .HostConfig.PortBindings}}'`：[tx-state.json](tx-state.json)。三服务容器内部地址为0.0.0.0，宿主发布却是127.0.0.1，见白名单过滤后的[container-listeners.json](container-listeners.json)。不是“容器内服务只监听127.0.0.1”，也不是“三服务没有端口映射”，而是**映射仅接受宿主loopback**。原生8082/8085/8086和9082/9085/9086没有同号宿主发布。
- E1=127.0.0.1:18086→8086，admin19086→9086；E2=18082→8082、19082→9082；E5=18085→8085、19085→9085。E3=18083/19083，MySQL13306、Redis16379、Prometheus19190、Grafana13100也仅loopback。
- S1临时E1曾用127.0.0.1:18096→8086、19096→9086：`scripts/s1-suite.py:start_e1`，当前容器已移除、这两个宿主监听不存在。专用Redis bcl-s1-redis没有宿主发布。Mac随机本地端口是SSH入口，不是tx公网映射，历史业务目标详见[mac-historical-local-targets.json](mac-historical-local-targets.json)；对应tx目标只有18096，控制端19096和13306。不能把Mac随机端口拿到tx公网探测。
- `sudo -n ufw status verbose`：inactive。`sudo -n iptables -S`：IPv4 INPUT/FORWARD/OUTPUT默认ACCEPT；无本次业务端口专门拒绝；22存在源IP黑名单，18443另有白名单链（只放行指定来源和loopback，其他拒绝）；Tailscale链另限制伪造100.64/10来源。IPv6 INPUT默认ACCEPT、Tailscale规则另列。**主机层没有“仅放行若干业务端口”的清单，按默认策略其他普通入站被允许；这不是云安全组允许的证明。** Docker raw/NAT规则还限定127.0.0.1发布端口的目标地址/入口。原始规则见tx-state.json，不应为了直连删除Docker保护规则。
- 云安全组/轻量云防火墙：**未核实**。`command -v tccli`返回1，无CLI；本次没有访问云账户凭据或控制台。22/80/443应用可达是事实，但既不能据此反推完整安全组规则，也不能把目标服务不通单独归因于云安全组。
- 公网IPv6：只有22拿到SSH banner；80/业务端口无应用响应，443 TLS未成功。内网10.0.0.15本轮所有被测端口均无有效应用协议回复；Mac没有验证到VPC私网直达路径。

## 2. 为什么走SSH、RTT和抖动证据

**事实：当时有直连尝试，但不是同一S1正式矩阵期间重新做的直连A/B试验。** 2026-10-20 P0曾从真实Mac探测公网18082/18083/18085/18086：TCP connect成功、应用请求超时；SSH转发四服务响应成功。证据`../2026-10-20-p0-mac/network-mac-tx.json`、`summary.json`和`docs/progress.md`的2026-10-20记录。S1沿用这一已验证路径。没有找到S1最终四流正式矩阵前独立再次验证业务直连的记录，因此“当时直连S1实验端口18096也实测失败”应标**未核实**。

2026-10-27单隧道先导中C在1500req/s时客户端p99约1.39s、服务端约50ms且丢到达，记录在`docs/progress.md`“停止单隧道试跑，修正链路并重新校准”。随后使用四条业务SSH连接和独立控制连接，降低一条SSH流承载所有业务的风险；不是因为四流已被证明无抖动。真实代码`scripts/s1/mac-worker.py`显式ControlMaster=no、ControlPath=none，每业务流最多256 HTTP连接，总在途1536。每条SSH内仍复用多个direct-tcpip channel到同一外层TCP连接。

### 本次空载诊断

执行：`ssh -o BatchMode=yes -o ConnectTimeout=10 luomac python3 - < docs/measurements/2026-10-28-s1-network-audit/mac-audit.py`。源码[mac-audit.py](mac-audit.py)完整保留。单并发串行，每次请求后间隔20ms，不调用loadgen，不读写商品，不重置指标；背景应用照常运行，因此是“无专用发压的空载诊断”，不是完全空闲的物理主机。

HTTP对象为`GET /healthz`，在nginx80和E1业务端口上都返回静态404（本应用此路径不是健康检查接口），完整读取响应。用于网络/转发基线，不声称是GET商品性能或ICMP RTT。公网和SSH80用相同Host/path/响应；TLS401仅用于443可达性探测，不参与RTT。HTTPConnection直开socket，没有使用环境HTTP代理，但操作系统TUN仍存在。所有采样都是请求开始至响应体读完；复用组包含第一个冷连接样本。顺序执行、不同时间段，未做随机交叉或同一时刻配对。

| 路径 | 成功/尝试 | p50 ms | p99 ms | 最大ms |
|---|---:|---:|---:|---:|
| direct-http80-43.142.37.199 | 1000/1000 | 29.880 | 158.401 | 462.320 |
| direct-http80-100.87.80.92 | 1000/1000 | 29.362 | 108.894 | 259.549 |
| ssh-http80-reuse | 1000/1000 | 32.498 | 109.150 | 268.926 |
| ssh-e1-health-reuse | 1000/1000 | 32.188 | 101.985 | 222.066 |
| ssh-e1-health-fresh | 1000/1000 | 72.511 | 161.455 | 208.657 |

原始5000条时间值、状态、响应长度及cleanup记录均在mac-results.jsonl；p50/p99使用nearest-rank（排序后ceil(n*p)第n项），max直接取最大值。空载SSH80没有比公网80更差的p99；**推断：原有长尾不能全部归因于SSH**。这不证明负载时SSH没有排队，也不能将两组p99相减当作隧道开销。fresh与reuse增加了建HTTP连接/SSH channel的步骤，分布差异支持连接建立有成本，但不是对TCP多流队头阻塞的单独识别。

已用S1原CSV离线拆解，结果[s1-client-delay.json](s1-client-delay.json)：有效主矩阵成功窗口GET按请求计算`sent-planned`、`done-sent`和`done-planned`，再按运行取p99，各策略运行p99的中位数如下（混合不同档位，诊断用，非策略收益比较）：

| 策略 | 调度前等待p99中位ms | 发出至完成p99中位ms | 总p99中位ms |
|---|---:|---:|---:|
| A | 1.040 | 529.121 | 529.586 |
| B | 0.908 | 417.362 | 417.754 |
| C | 0.601 | 581.118 | 581.795 |
| D | 0.602 | 474.054 | 474.308 |

`sent`在`doS1`之前写入，之后仍包含HTTP连接池取得连接、SSH channel、网络、服务端、客户端解码。没有httptrace/GetConn/GotConn/首字节、逐请求SSH channel或TCP序列对应，因此**未核实各阶段排队比例；没有证据把几百毫秒全归于SSH/HOL**。服务端与客户端p99不是同一个请求样本，也不能直接相减求网络p99。服务端约3ms也不是所有S1条件的统一值，原报告保留各档位表。

辅助网络证据：`timeout 45 sudo -n tcpdump -n -tt -l -i eth0 'tcp port 22 or tcp port 80'`，只保留传输头（剥除HTTP自动解码），[tx-packets.txt](tx-packets.txt)及[tcpdump-status.txt](tcpdump-status.txt)显示2526包、内核丢包0，观察到直连80请求确实抵达tx且迅速回复。13个完全相同序列范围的重复发送见[packet-summary.json](packet-summary.json)，这是传输重发线索，捕获并非整个实验、没有请求映射，不可当S1隧道因果证明。`ss -ti '( sport = :22 )'`快照[ssh-tcp-state.txt](ssh-tcp-state.txt)中一条空闲SSH连接有bytes_retrans=36/retrans累计1，而活跃采样连接该快照没有该计数；不能据此宣称活跃隧道重传造成了尾延迟。

## 3. S1陈旧读口径、可重算范围、version-only成本

**事实：ACK口径是为了用同一客户端单调时钟避开跨机时钟比较。** `loadgen/s1.go:summarizeS1`及`docs/design-decisions.md`明确其为保守下界；它把源已提交但写响应尚未到Mac的窗口排除。方案A.2要求写端提交日志；本实现没有完成全key提交真值采集，**这是验收口径缺口，不是A.2的等价实现**。历史报告已注明下界，但不能以此下界的0判定提交口径也为0。

**事实：不能用现有trace重算全部key正式窗口A/B/C/D的A.2陈旧率。** `cmd/guide/main.go`只读取一个GUIDE_TRACE_ID，`internal/observe/observe.go:FileTracer.Begin`对其他ID返回0，start_e1把它设为popularity第一个ID（1851455）。trace没有每行商品ID，因为设计就是单key；op只有进程内含义，没有client request ID或formal-window标识。CSV覆盖全key，但没有服务器read_start/提交时间及两边请求映射；bypass写不经过E1，服务trace没有它的提交事件。现有updated_at在SQL执行前生成，也不是提交日志。

即使是热点，write_commit事件发生在`Store.Put`成功返回之后，是服务观测到的已提交版本时间，晚于真实MySQL提交；它仍遗漏提交到事件记录之间的窗口。t_ns是同一进程的单调时间，支持同进程排序，不能将不同尝试的t_ns拼接。事件实际文件顺序可能受锁调度影响，重算按t_ns排序。

补充离线重算：只取有效主矩阵、每次尝试全程（包括预建/预热），以`read_start`前严格小于该时刻的最大服务观测write_commit版本为下界，终点版本取cache_hit/cache_validated/fill；初始版本1。脚本[recount-hot-trace.py](recount-hot-trace.py)与逐轮结果[hot-trace-recount.json](hot-trace-recount.json)已保存，不重新发压。

| 主矩阵策略 | 有效运行 | 热点完整读 | 观测陈旧数 | 热点全程陈旧率 |
|---|---:|---:|---:|---:|
| A | 33 | 29920 | 2 | 0.006684% |
| B | 34 | 31002 | 1 | 0.003226% |
| C | 36 | 32777 | 0 | 0% |
| D | 34 | 31128 | 0 | 0% |

缺失终点0；这些是热点事件池化比例，**不是全部key、不是正式窗口、不是精确数据库提交时刻口径，也不够比较策略优劣**。反向、剖析、本机校准另列JSON；bypass源提交缺失，输出stale/ratio=null，不能报0。

version-only：`internal/catalog/mysql.go:Get`目前取title/price/stock/version/updated_at；表PRIMARY KEY(id)，version没有独立覆盖索引。**推断，未实测：**只投影version可减少结果传输、字段解码/分配，但仍访问同一聚簇索引页和主键点查，命中也必须每次访问源，不能假设消除源往返或磁盘页读取。若只是先查version，再在缓存未命中时补全行，以现有C缓存命中率41.7–48.7%粗估，源查询次数可能由每读1次增为1.51–1.58次（+51–58%，还没算版本不符），不保证更快；命中率数据见`../2026-10-27-s1/groups.csv`。如果先查缓存，miss直接全行、hit才查version，则版本匹配时仍约1次源查询，mismatch需第二次，预计降低传输/解码但源QPS基本不降。缓存命中不等于版本匹配，以上是简化估算，不预测p99改善百分比，实际需另测。现有C热点source区间p50约346–421μs包含连接池/驱动，不能把它全当可节省的SQL行读取时间。

## 4. S2建议与需要开放的端口

**推断/建议：在tx上运行单一观测协调进程，使用同一单调时钟，直接经loopback访问源与两个E1实例；Mac只负责启动和取结果。** 所有商品ID/请求ID、写前/提交返回、读取调用起点/返回版本、GTID或binlog位置、消费事件/版本均记录，明确“提交返回口径”和真实DB提交不是同一时刻。写提交时刻至少以commit调用前后区间包围，不把返回后的日志当真实提交瞬间；缓存可见性轮询保留“最后旧值/首次新值”两端和轮询间隔。报告窗口上下界与时间分辨率，避免给亚毫秒伪精度。若服务进程分别记录，必须使用同一机器公共CLOCK_MONOTONIC基准或校准原点，不能直接比较各进程time.Since各自start的数字。

本方案无需用户新增公网端口。先补足A.2提交真值/全key覆盖再开展S2正式实验。S1已有数据不足以补回这个缺口，不能通过改统计脚本假装补齐。

如果仍要Mac直接访问业务：需要管理员**先修改宿主发布绑定**，再核对/放行云规则，建议优先绑定tx的Tailscale地址而非全公网；限定Mac来源。具体用途：TCP18096=保留S1命名的实验E1实例1；TCP18097=建议新增的S2实例2（尚未配置/监听）；若直测现有服务，TCP18086=E1，18082=E2 TCP/JSON，18085=E5 HTTP。19096/19097（实验管理）、19086/19082/19085（现有管理）保留loopback，通过SSH管理即可；MySQL13306、Redis16379不需要对公网开放。S2不要求开放E2/E5。这里是需要用户/管理员实施的选项，本次没有改任何绑定或放行规则。

## 复核与范围

临时SSH隧道由mac-audit.py finally清理，mac-results.jsonl最后一行确认进程退出；没有后台正式发压。新增文件不影响旧S1结果或其校验清单。Mac/TX原有其他进程、服务与数据没有重置。全量项目测试没有重跑，因为没有业务代码修改。
