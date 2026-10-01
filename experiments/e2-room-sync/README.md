# E2 房间状态同步

最后更新：2026-10-02（北京时间）；Codex；backend-cloud-labs@8519ae7 + 本轮工作树修改。

独立 Go module，零第三方依赖。默认 `go run ./cmd/room` 监听 127.0.0.1:8082，可用 ROOM_ADDRESS 修改。协议为 TCP 长连接上的每行一个 JSON，默认每 50ms 一个权威帧。

## 协议

首次连接发送 `{"type":"join"}`。服务端 welcome 返回随机 id 与 token，仅发给该连接，然后立即发完整 snapshot。移动输入：`{"type":"move","seq":1,"dx":1,"dy":0}`，增量范围 [-1,1]，seq 正整数且递增。帧循环有界处理输入，再给全体连接同一帧状态 `{type:snapshot,frame,players:{id:{x,y,seq}}}`。seq 是已应用输入的确认值；同号/旧号不重复移动。

重连发送 `{"type":"join","id":"原ID","token":"原token"}`，恢复位置和 seq，关闭原连接；排队中的旧连接输入也被 connection 身份检查拒绝。token 是演示会话恢复凭据，不是用户账号认证。保留 token，后续输入使用大于 snapshot.seq 的序号。

握手超时 5 秒、输入空闲超时 30 秒、写超时 2 秒；64KiB 单行上限；容量 256 输入队列和每连接容量 4 快照队列。慢写/队列满只关闭相关连接；有限输入处理避免一帧无限循环。取消服务时关闭 listener/连接并等待 handler/writer/frame goroutine 结束。

## W6 / W7 证据

W6：真实 TCP 两客户端同帧状态相同，断线恢复、重复输入去重。W7 维持：错误 token/非法握手拒绝、连接替换后旧输入失效、慢队列不阻塞帧、退出能回收 worker 的专项测试。3 个测试通过 `go test -race -count=1 ./...`，`go vet ./...` 无错误。

根目录 `sg docker -c 'bash scripts/bootstrap.sh'` 重建并验收 E1/E2；E1 自动创建独占临时 Redis、随机 loopback 端口，退出时删除容器。E2 可独立执行 go test，不需要 Docker。

范围：一个房间、最多 128 个保留玩家身份、内存权威状态；重启丢失状态。不包含跨节点房间迁移、持久化、生产账号/TLS、回滚预测、真实游戏引擎客户端，也不代表这些能力已完成。
