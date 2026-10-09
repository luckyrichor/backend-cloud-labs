# E3：规则表达式 AST 与批量过滤

最后更新：2026-10-09；Codex；本轮源码与测量来源统一见仓库根目录 docs/measurements/2026-10-09-fifth-review.json。

独立 C++20/CMake，无外部依赖。支持十进制数字、变量、括号、单目负号/非、四则、比较与 &&/||，逻辑短路。数字文法是 `digits[.digits]` 或 `.digits`，可附带 `e/E[+/-]digits`；例如 `.5`、`1.`、`1e-2`。扫描后由 locale 独立的 `from_chars` 转换，拒绝十六进制、残缺指数、溢出/下溢及非有限数。未知变量、除零、非法语法均报错。

输入4096字符、AST256节点、嵌套64层。提供单条/批量与名称/槽位两类执行方式；没有字符串、函数、任意代码执行。

## 当前接口与运行

```cpp
auto rule = rules::Parser("stock>0 && price*0.8<=100").compile();
std::vector<rules::Variables> documents = {{{"stock",3},{"price",100}}, {{"stock",0}}};
auto values = rule->evaluate_batch(documents); // {1,0}
auto matches = rule->filter_batch(documents); // {0}
std::vector<std::string> schema = {"stock", "price"};
const auto bound = rule->bind_variables(schema);
std::array<double,2> row = {3,100};
auto accepted = bound.evaluate(row); // 1
```

解析时运算符转为Op枚举，执行用switch。名称接口运行时查unordered_map；Bound在绑定阶段解析数组下标、独立拥有私有树，运行期只暴露const evaluate，原树修改/销毁不影响Bound。schema必须唯一且包含所有引用变量；求值仅检查实际访问的slot，短路可跳过未访问的缺失/非法值。

批量输入是只读span，顺序执行，返回值或原始下标；错误附从0开始的文档下标、失败立即返回异常且不提供部分结果。输出预留最多N元素，可分块控制内存。

```bash
# 仓库根目录
cmake -S experiments/e3-rule-engine -B build/e3 -DCMAKE_BUILD_TYPE=Release
cmake --build build/e3 -j2
ctest --test-dir build/e3 --output-on-failure
# 当前源码的两套不同基准，不混用历史CSV
build/e3/rule_bench > .local/e3-current-batch.csv
build/e3/rule_slot_bench > .local/e3-current-slots.csv
```

当前回归包括原222条检查、19568条字符串/枚举/批量差分、1500条名称/slot差分及边界、新十进制语法专项。string_baseline.hpp是冻结的旧实现，仅作测试与历史性能参考，不定义当前规则语言。Release与ASan/UBSan CTest4/4，泄漏检测关闭。

## 基准证据按版本分开

| 证据 | 对应阶段与口径 |
|---|---|
| w8-expression.csv / w8-w10-source.json | 早期两种解析模式，各10轮×10万求值；原始基线与源哈希见JSON。历史固定checksum60012，仅适用该脚本。 |
| 2026-10-08-e3-batch.csv/.json | 字符串/枚举/批量/重解析；两种短路分布，1000/10000/100000独立文档，每轮合计100万求值。元数据对应当时源码。 |
| 2026-10-09-e3-slots.csv / third-review.json | 第三轮原地绑定的历史计时；哈希47.6856、预数组14.51135、逐条转换34.85875 ns/doc。不是Bound的新测量。 |
| 2026-10-09-e3-bound-slots.csv / fifth-review.json | 本轮独立只读Bound复测，10万独立文档×10次/轮、10轮、3模式轮换；同轮checksum一致。 |

上述文件均在仓库根目录docs/measurements。slot基准的输入准备、绑定/树复制不在计时内；convert_then_slot将每条两个名字查找和数组准备计入。每行是整轮平均，分位数只能描述轮均值，不是请求p95。性能数值见[现状与边界](../../docs/status.md)。

## 为什么本轮不新增字节码

跳转指令做短路是可行的标准方案，不能把“做不到短路”作为理由。当前实验已比较字符串、枚举和槽位，目标是测清哪些开销被移出了热路径；没有AST与VM的同口径数据证明字节码值得替换。新增VM应以相同短路、异常、数值边界的差分测试和同输入计时作验收，结果可能快也可能慢；不是完成一个接口名就算优化。

仓库AGENTS的“AST/字节码”是训练方向表述；已归档岗位15原文要求规则引擎与规模/性能，未规定必须同时实现AST和字节码。面试可以解释两种执行表示的取舍，但不能把未实现VM写成成果。

## 覆盖边界

最大实测独立文档10万；有顺序批量和slot数组，不含倒排/范围/位图索引、候选裁剪、多线程、分片/分布式检索或SIMD。这能说明单机表达式正确性与热路径优化，不能证明百亿DOC检索能力。
