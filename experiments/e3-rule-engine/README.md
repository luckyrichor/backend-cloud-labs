# E3：规则表达式 AST（W8 / W9 维护）

Codex，2026-10-07。独立 C++20 / CMake，无外部依赖。

支持数字、变量、括号、单目负号/非、四则、比较、&&/||，按优先级解析，
逻辑运算短路。未知变量、除零、非有限数、错误语法报错。
W9维护增加/验收输入4096字符、AST256节点、嵌套64层限制，防止无限递归；
缓存 AST 与每次解析的求值语义相同。数字使用 C strtod；默认 C locale，
实验不提供字符串、函数、任意代码执行或业务沙箱。

```bash
cmake -S experiments/e3-rule-engine -B build/e3 -DCMAKE_BUILD_TYPE=Release
cmake --build build/e3 -j2
ctest --test-dir build/e3 --output-on-failure
build/e3/rule_bench > docs/measurements/w8-expression.csv
```

222条检查包含201个独立算术期望值、短路和异常边界。固定表达式
`stock>0 && price*0.8<=100`，两模式各10轮×100000次，变化price防止常量折叠。
每轮校验和均60012。CSV记录的是整轮均值，不是单次延迟p95。
设备：TX Ubuntu，AMD EPYC 7K62虚拟4CPU，gcc13.3，CMake3.28，Release。
测量包括查变量/修改变量，不隔离时钟和云主机调度影响；不是生产性能结论。
基线backend-cloud-labs@68f0767 + 本轮修改，源码SHA256见测量元数据。
