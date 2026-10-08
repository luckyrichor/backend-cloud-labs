# E3：规则表达式 AST 与批量过滤

Codex，2026-10-08。独立 C++20 / CMake，无外部依赖。

支持数字、变量、括号、单目负号/非、四则、比较、&&/||，按优先级解析，
逻辑运算短路。未知变量、除零、非有限数、错误语法报错。
W9维护增加/验收输入4096字符、AST256节点、嵌套64层限制，防止无限递归；
缓存 AST 与每次解析的求值语义相同。数字使用 C strtod；默认 C locale，
实验不提供字符串、函数、任意代码执行或业务沙箱。

```bash
cmake -S experiments/e3-rule-engine -B build/e3 -DCMAKE_BUILD_TYPE=Release
cmake --build build/e3 -j2
ctest --test-dir build/e3 --output-on-failure
build/e3/rule_bench > docs/measurements/2026-10-08-e3-batch.csv
```

222条检查包含201个独立算术期望值、短路和异常边界。固定表达式
`stock>0 && price*0.8<=100`，两模式各10轮×100000次，变化price防止常量折叠。
每轮校验和均60012。CSV记录的是整轮均值，不是单次延迟p95。
设备：TX Ubuntu，AMD EPYC 7K62虚拟4CPU，gcc13.3，CMake3.28，Release。
测量包括查变量/修改变量，不隔离时钟和云主机调度影响；不是生产性能结论。
基线backend-cloud-labs@68f0767 + 本轮修改，源码SHA256见测量元数据。


## 批量接口与枚举执行（2026-10-08）

解析时把运算符转换为 `Op` 枚举，求值使用 switch，不再逐节点比较字符串；原始字符串实现冻结在 string_baseline.hpp，仅用于差分测试和性能对照。解析器输入/节点/嵌套限制与逻辑短路保留。未实现字节码或 SIMD。

```cpp
auto rule = rules::Parser("stock>0 && price*0.8<=100").compile();
std::vector<rules::Variables> documents = {
  {{"stock",3},{"price",100}}, {{"stock",0}}};
auto values = rule->evaluate_batch(documents); // {1, 0}
auto indices = rule->filter_batch(documents); // {0}，按输入顺序返回
```

接口使用 span<const Variables>，不复制输入，复用已解析的 AST，内部仍逐文档顺序求值。空输入返回空；任意非零有限结果匹配。某文档出错时抛出带从0开始下标的 runtime_error，不返回部分结果、不修改输入。可按块调用以控制输出内存；输出预留最多 N 个元素，输入仍为 unordered_map，不宣称列式执行、线程并行或服务容量。

原222条检查保留；新增19568条差分/批量检查，对照旧字符串实现的数值与错误，包含所有运算符、短路、10000文档过滤、空批次、输入不变、错误下标和非有限值。Release CTest 2/2通过；ASan/UBSan Debug CTest 2/2通过，detect_leaks=0，未验证泄漏检测。

新 benchmark 比较 string_scalar、enum_scalar、enum_batch、parse_enum_scalar；每种都返回相同完整下标向量并预留N容量。输入构建与规则预编译在计时外，结果分配/过滤在计时内；全量结果一致性、checksum计算在计时外。两种分布：全求右侧与75%文档左侧短路；文档规模1000/10000/100000。每组预热后10轮，轮换模式顺序，每轮处理100万文档（按规模重复批次）。不包含HTTP、数据读取或并行处理；CSV是整轮均值，不是单文档p95。历史w8-expression.csv保持原口径，不与新数据混算。


### 本轮 TX 实测

| 数据分布 | 文档数 | 字符串单条 ns/doc | 枚举单条 ns/doc | 枚举批量 ns/doc |
|---|---:|---:|---:|---:|
| full_rhs | 1000 | 137.00 | 44.82 | 39.95 |
| full_rhs | 10000 | 137.92 | 46.03 | 40.91 |
| full_rhs | 100000 | 140.28 | 51.28 | 47.49 |
| early_short_circuit | 1000 | 69.75 | 28.25 | 24.35 |
| early_short_circuit | 10000 | 72.45 | 29.01 | 25.57 |
| early_short_circuit | 100000 | 77.97 | 34.00 | 28.21 |

每格为10轮平均耗时的中位数；枚举单条相对旧字符串约2.29–3.06倍，枚举批量约2.76–3.43倍。主要收益来自运算符枚举；批量不等于并行/SIMD。重解析结果也在原始CSV中。云主机共享4 vCPU，无绑核；开始几轮与sanitizer编译/测试重叠，记录了干扰，不把单次比例当作稳定生产保证。原始CSV和包含工具链、源码摘要、最小/最大轮均值的JSON均在 docs/measurements/2026-10-08-e3-batch.*。

复算摘要：`python3 experiments/e3-rule-engine/scripts/summarize.py`（仓库根目录）。复测时建议先完成构建/测试再单独跑benchmark。历史计时口径保留；此处规模最大为10万常驻合成文档，重复至每轮100万次求值，不代表100万独立文档。
