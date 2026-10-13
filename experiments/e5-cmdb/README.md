# E5 CMDB：配置项 / 关系图 / 审计回溯

独立Go模块，纯标准库。配置项支持service/host/database与字符串属性；有向关系支持依赖、部署等kind。变更统一POST /changes，以expected_revision对全图做CAS，actor/reason必填。CRUD及link/unlink原子更新图和审计；删除仍有关联的配置项拒绝，要求显式解绑，不静默丢关系。间接环允许，遍历用visited避免无限循环。

```bash
cd experiments/e5-cmdb
mkdir -p .local
go run ./cmd/cmdb -state .local/cmdb.json
```

只监听127.0.0.1:8085，演示无鉴权；actor由请求提供，不是真实审计身份。不要暴露公网。

- `POST /changes`: `{expected_revision,actor,reason,operation,item?,relation?}`，operation=create/update/delete/link/unlink。
- `GET /state?revision=N`: 当前图或历史图，revision=0为空初始图。
- `GET /graph?root=api&depth=3&direction=out&revision=N`: 0..10层，out/in/both，返回访问节点的诱导子图，稳定排序。
- `GET /audit?offset=0&limit=20`: limit1..100，每事件带before/after快照、时间、完整变更和版本。

示例配置项：`{"id":"api","kind":"service","name":"shopping-api","attributes":{"owner":"commerce"}}`。关系：`{"from":"api","to":"db","kind":"depends_on"}`。历史快照不被后续更新覆盖，重启加载检查版本连续性与快照链一致。存储整个图+完整快照历史，通过同目录临时文件write/fsync/rename原子替换；失败不发布内存变更。保证正常进程重启可回溯，不声称电源故障无损（目录未fsync）、防篡改、跨进程并发、数据库事务级生产能力。一个文件只能由一个Store进程写；内存锁只协调本实例。

以全图版本而非每配置项版本避免关系变更竞态，代价是无关写也会冲突。完整快照审计空间随历史和图规模增长，是讲清模型与变更语义的实验；生产应换数据库追加事件 / 快照归档与认证身份。不做海量图性能结论。

`go test -race ./...; go vet ./...` 覆盖循环/反向/历史图、原子重启、对象副本隔离、写失败不提交、20并发CAS、HTTP输入/冲突与解绑删除。
