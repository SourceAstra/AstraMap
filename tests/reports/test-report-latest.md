# AstraMap 多语言测试报告

- **执行时间**: 2026-07-16 20:50:45
- **结论**: **失败**

## 汇总

| 总用例 | 已测试 | 通过 | 失败 | 未测试 | 通过率 | 完成率 |
|---:|---:|---:|---:|---:|---:|---:|
| 217 | 217 | 197 | 20 | 0 | 90.8% | 100.0% |

## 详细结果

| 状态 | 测试项 | 详情 |
|---|---|---|
| ✓ | DETECT-GO .go→go | 实际值匹配预期 |
| ✓ | DETECT-TS .ts→typescript | 实际值匹配预期 |
| ✓ | DETECT-PY .py→python | 实际值匹配预期 |
| ✓ | DETECT-JAVA .java→java | 实际值匹配预期 |
| ✓ | DETECT-C .c→c | 实际值匹配预期 |
| ✓ | DETECT-CPP .cpp→cpp | 实际值匹配预期 |
| ✓ | DETECT-H-C .h(纯C项目)→c | 实际值匹配预期 |
| ✓ | DETECT-H-CPP .h(含C++项目)不被索引 | 实际值匹配预期 |
| ✓ | [go] func_declaration: add(kind=function) | 值大于阈值 |
| ✓ | [go] method_declaration: Point(kind=struct) | 值大于阈值 |
| ✓ | [go] method_declaration: Distance(kind=method) | 值大于阈值 |
| ✓ | [go] interface_definition: Writer(kind=interface) | 值大于阈值 |
| ✓ | [go] struct_definition: User(kind=struct) | 值大于阈值 |
| ✓ | [go] simple_call: add(kind=function) | 值大于阈值 |
| ✓ | [go] simple_call: main(kind=function) | 值大于阈值 |
| ✓ | [go] simple_call: main→add | 值大于阈值 |
| ✓ | [go] method_call: Point(kind=struct) | 值大于阈值 |
| ✓ | [go] method_call: Distance(kind=method) | 值大于阈值 |
| ✓ | [go] method_call: main(kind=function) | 值大于阈值 |
| ✓ | [go] method_call: main→Distance | 值大于阈值 |
| ✓ | [go] pointer_receiver_method: Counter(kind=struct) | 值大于阈值 |
| ✓ | [go] pointer_receiver_method: Inc(kind=method) | 值大于阈值 |
| ✓ | [go] func_multi_param: Calculate(kind=function) | 值大于阈值 |
| ✓ | [go] multiple_functions: Add(kind=function) | 值大于阈值 |
| ✓ | [go] multiple_functions: Subtract(kind=function) | 值大于阈值 |
| ✓ | [go] cross_file_call: Calculate(kind=function) | 值大于阈值 |
| ✓ | [go] cross_file_call: Use(kind=function) | 值大于阈值 |
| ✓ | [go] cross_file_call: Use→Calculate | 值大于阈值 |
| ✓ | [python] class_definition: Point(kind=class) | 值大于阈值 |
| ✗ | [python] function_definition: greet(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] simple_call: add(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] simple_call: main(kind=method) | 值应大于%s，实际=%s |
| ✓ | [python] simple_call: main→add | 值大于阈值 |
| ✓ | [python] nested_class: Outer(kind=class) | 值大于阈值 |
| ✓ | [python] nested_class: Inner(kind=class) | 值大于阈值 |
| ✓ | [python] static_method: Utils(kind=class) | 值大于阈值 |
| ✓ | [python] static_method: helper(kind=method) | 值大于阈值 |
| ✓ | [python] class_method: Factory(kind=class) | 值大于阈值 |
| ✓ | [python] class_method: create(kind=method) | 值大于阈值 |
| ✗ | [python] async_function: fetch_data(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] decorator_function: my_decorator(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] decorator_function: wrapper(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] decorator_function: decorated(kind=method) | 值应大于%s，实际=%s |
| ✓ | [python] multiple_classes: Dog(kind=class) | 值大于阈值 |
| ✓ | [python] multiple_classes: Cat(kind=class) | 值大于阈值 |
| ✓ | [python] multiple_classes: bark(kind=method) | 值大于阈值 |
| ✓ | [python] multiple_classes: meow(kind=method) | 值大于阈值 |
| ✓ | [typescript] interface_definition: User(kind=interface) | 值大于阈值 |
| ✓ | [typescript] class_definition: Point(kind=class) | 值大于阈值 |
| ✓ | [typescript] class_definition: distance(kind=method) | 值大于阈值 |
| ✓ | [typescript] function_declaration: add(kind=function) | 值大于阈值 |
| ✓ | [typescript] generic_function: identity(kind=function) | 值大于阈值 |
| ✓ | [typescript] simple_call: greet(kind=function) | 值大于阈值 |
| ✓ | [typescript] simple_call: main(kind=function) | 值大于阈值 |
| ✓ | [typescript] class_with_method_call: Calculator(kind=class) | 值大于阈值 |
| ✓ | [typescript] class_with_method_call: add(kind=method) | 值大于阈值 |
| ✓ | [typescript] class_with_method_call: compute(kind=method) | 值大于阈值 |
| ✓ | [typescript] export_function: parse(kind=function) | 值大于阈值 |
| ✓ | [typescript] default_export_class: App(kind=class) | 值大于阈值 |
| ✓ | [typescript] multiple_interfaces: Serializable(kind=interface) | 值大于阈值 |
| ✓ | [typescript] multiple_interfaces: Comparable(kind=interface) | 值大于阈值 |
| ✓ | [typescript] namespace_with_function: abs(kind=function) | 值大于阈值 |
| ✓ | [c] function_definition: add(kind=function) | 值大于阈值 |
| ✓ | [c] struct_definition: Point(kind=type) | 值大于阈值 |
| ✓ | [c] macro_definition: MAX_SIZE(kind=macro) | 值大于阈值 |
| ✓ | [c] macro_definition: ADD(kind=macro) | 值大于阈值 |
| ✓ | [c] enum_definition: Color(kind=enum) | 值大于阈值 |
| ✓ | [c] typedef_simple: Handle(kind=type) | 值大于阈值 |
| ✓ | [c] simple_call: add(kind=function) | 值大于阈值 |
| ✓ | [c] simple_call: main(kind=function) | 值大于阈值 |
| ✓ | [c] simple_call: main→add | 值大于阈值 |
| ✓ | [c] object_macro: VERSION(kind=macro) | 值大于阈值 |
| ✓ | [c] object_macro: PI(kind=macro) | 值大于阈值 |
| ✓ | [c] multi_function: max(kind=function) | 值大于阈值 |
| ✓ | [c] multi_function: min(kind=function) | 值大于阈值 |
| ✓ | [c] multi_function: clamp(kind=function) | 值大于阈值 |
| ✓ | [c] typedef_struct_named: Node(kind=struct) | 值大于阈值 |
| ✓ | [cpp] function_definition: add(kind=function) | 值大于阈值 |
| ✓ | [cpp] namespace_function: abs(kind=function) | 值大于阈值 |
| ✓ | [cpp] simple_call: add(kind=function) | 值大于阈值 |
| ✓ | [cpp] simple_call: main(kind=function) | 值大于阈值 |
| ✓ | [cpp] simple_call: main→add | 值大于阈值 |
| ✓ | [cpp] template_function: max(kind=function) | 值大于阈值 |
| ✓ | [cpp] class_with_methods: Calculator(kind=class) | 值大于阈值 |
| ✗ | [cpp] class_with_methods: add(kind=function) | 值应大于%s，实际=%s |
| ✗ | [cpp] class_with_methods: subtract(kind=function) | 值应大于%s，实际=%s |
| ✓ | [cpp] enum_definition: Color(kind=enum) | 值大于阈值 |
| ✓ | [cpp] struct_definition: Record(kind=struct) | 值大于阈值 |
| ✓ | [cpp] multiple_functions: max(kind=function) | 值大于阈值 |
| ✓ | [cpp] multiple_functions: min(kind=function) | 值大于阈值 |
| ✓ | [java] class_definition: Point(kind=class) | 值大于阈值 |
| ✓ | [java] class_definition: distance(kind=method) | 值大于阈值 |
| ✓ | [java] interface_definition: Reader(kind=interface) | 值大于阈值 |
| ✓ | [java] method_call: Main(kind=class) | 值大于阈值 |
| ✓ | [java] method_call: main(kind=method) | 值大于阈值 |
| ✓ | [java] method_call: main→println | 值大于阈值 |
| ✓ | [java] inner_class: Builder(kind=class) | 值大于阈值 |
| ✓ | [java] inner_class: Config(kind=class) | 值大于阈值 |
| ✓ | [java] abstract_class: Base(kind=class) | 值大于阈值 |
| ✓ | [java] abstract_class: process(kind=method) | 值大于阈值 |
| ✓ | [java] abstract_class: init(kind=method) | 值大于阈值 |
| ✓ | [java] static_method: Utils(kind=class) | 值大于阈值 |
| ✓ | [java] static_method: calculate(kind=method) | 值大于阈值 |
| ✓ | [java] multiple_classes: Animal(kind=class) | 值大于阈值 |
| ✓ | [java] multiple_classes: Dog(kind=class) | 值大于阈值 |
| ✓ | [java] multiple_classes: speak(kind=method) | 值大于阈值 |
| ✓ | [java] constructor: Item(kind=class) | 值大于阈值 |
| ✓ | [go] cross_file_call: Calculate(kind=function) | 值大于阈值 |
| ✓ | [go] cross_file_call: Use(kind=function) | 值大于阈值 |
| ✓ | [go] cross_file_call: Use→Calculate | 值大于阈值 |
| ✓ | [go] embedded_struct: Base(kind=struct) | 值大于阈值 |
| ✓ | [go] embedded_struct: Extended(kind=struct) | 值大于阈值 |
| ✓ | [go] interface_satisfaction: Shape(kind=interface) | 值大于阈值 |
| ✓ | [go] interface_satisfaction: Circle(kind=struct) | 值大于阈值 |
| ✓ | [go] interface_satisfaction: Area(kind=method) | 值大于阈值 |
| ✓ | [go] multiple_return_values: ReadAll(kind=function) | 值大于阈值 |
| ✓ | [go] variadic_function: Sum(kind=function) | 值大于阈值 |
| ✗ | [python] cross_file_call: calculate(kind=method) | 值应大于%s，实际=%s |
| ✗ | [python] cross_file_call: process(kind=method) | 值应大于%s，实际=%s |
| ✓ | [python] class_inheritance: Animal(kind=class) | 值大于阈值 |
| ✓ | [python] class_inheritance: Dog(kind=class) | 值大于阈值 |
| ✓ | [python] class_inheritance: speak(kind=method) | 值大于阈值 |
| ✗ | [python] generator_function: fibonacci(kind=method) | 值应大于%s，实际=%s |
| ✓ | [python] property_decorator: Circle(kind=class) | 值大于阈值 |
| ✓ | [python] property_decorator: area(kind=method) | 值大于阈值 |
| ✓ | [python] context_manager: FileManager(kind=class) | 值大于阈值 |
| ✓ | [python] context_manager: __enter__(kind=method) | 值大于阈值 |
| ✓ | [python] context_manager: __exit__(kind=method) | 值大于阈值 |
| ✓ | [typescript] cross_file_call: calculate(kind=function) | 值大于阈值 |
| ✓ | [typescript] cross_file_call: process(kind=function) | 值大于阈值 |
| ✓ | [typescript] generic_class: Container(kind=class) | 值大于阈值 |
| ✓ | [typescript] generic_class: get(kind=method) | 值大于阈值 |
| ✓ | [typescript] interface_implementation: Printable(kind=interface) | 值大于阈值 |
| ✓ | [typescript] interface_implementation: Report(kind=class) | 值大于阈值 |
| ✓ | [typescript] interface_implementation: print(kind=method) | 值大于阈值 |
| ✓ | [typescript] namespace_with_export: fetch(kind=function) | 值大于阈值 |
| ✓ | [typescript] namespace_with_export: Client(kind=class) | 值大于阈值 |
| ✓ | [typescript] class_with_method: Shape(kind=class) | 值大于阈值 |
| ✓ | [typescript] class_with_method: area(kind=method) | 值大于阈值 |
| ✓ | [typescript] class_with_method: describe(kind=method) | 值大于阈值 |
| ✓ | [c] cross_file_call: calculate(kind=function) | 值大于阈值 |
| ✓ | [c] cross_file_call: main(kind=function) | 值大于阈值 |
| ✓ | [c] cross_file_call: main→calculate | 值大于阈值 |
| ✓ | [c] function_pointer_typedef: Callback(kind=type) | 值大于阈值 |
| ✓ | [c] conditional_compilation: debug_log(kind=macro) | 值大于阈值 |
| ✓ | [c] struct_with_function_pointer: Handler(kind=type) | 值大于阈值 |
| ✓ | [c] multi_include: VERSION(kind=macro) | 值大于阈值 |
| ✓ | [c] multi_include: Handle(kind=type) | 值大于阈值 |
| ✓ | [c] multi_include: create_handle(kind=function) | 值大于阈值 |
| ✓ | [cpp] cross_file_call: calculate(kind=function) | 值大于阈值 |
| ✓ | [cpp] cross_file_call: main(kind=function) | 值大于阈值 |
| ✓ | [cpp] class_inheritance: Shape(kind=class) | 值大于阈值 |
| ✓ | [cpp] class_inheritance: Circle(kind=class) | 值大于阈值 |
| ✗ | [cpp] class_inheritance: area(kind=function) | 值应大于%s，实际=%s |
| ✓ | [cpp] function_overload: process(kind=function) | 值大于阈值 |
| ✓ | [cpp] template_class: Stack(kind=class) | 值大于阈值 |
| ✗ | [cpp] template_class: push(kind=function) | 值应大于%s，实际=%s |
| ✗ | [cpp] template_class: pop(kind=function) | 值应大于%s，实际=%s |
| ✓ | [cpp] operator_overload: Vec(kind=class) | 值大于阈值 |
| ✗ | [cpp] operator_overload: operator+(kind=function) | 值应大于%s，实际=%s |
| ✓ | [java] cross_file_call: Calculator(kind=class) | 值大于阈值 |
| ✓ | [java] cross_file_call: calculate(kind=method) | 值大于阈值 |
| ✓ | [java] cross_file_call: Main(kind=class) | 值大于阈值 |
| ✓ | [java] cross_file_call: run(kind=method) | 值大于阈值 |
| ✓ | [java] cross_file_call: run→calculate | 值大于阈值 |
| ✓ | [java] class_inheritance: Animal(kind=class) | 值大于阈值 |
| ✓ | [java] class_inheritance: Dog(kind=class) | 值大于阈值 |
| ✓ | [java] class_inheritance: speak(kind=method) | 值大于阈值 |
| ✓ | [java] generic_class: Box(kind=class) | 值大于阈值 |
| ✓ | [java] generic_class: set(kind=method) | 值大于阈值 |
| ✓ | [java] generic_class: get(kind=method) | 值大于阈值 |
| ✓ | [java] interface_implementation: Printable(kind=interface) | 值大于阈值 |
| ✓ | [java] interface_implementation: Report(kind=class) | 值大于阈值 |
| ✓ | [java] interface_implementation: print(kind=method) | 值大于阈值 |
| ✓ | [java] method_overload: Processor(kind=class) | 值大于阈值 |
| ✓ | [java] method_overload: process(kind=method) | 值大于阈值 |
| ✓ | [multi] go_cross_file: Calculate(kind=function) | 值大于阈值 |
| ✓ | [multi] go_cross_file: Use(kind=function) | 值大于阈值 |
| ✓ | [multi] go_cross_file: Use→Calculate | 值大于阈值 |
| ✗ | [multi] python_cross_file: calculate(kind=method) | 值应大于%s，实际=%s |
| ✗ | [multi] python_cross_file: process(kind=method) | 值应大于%s，实际=%s |
| ✓ | [multi] typescript_cross_file: calculate(kind=function) | 值大于阈值 |
| ✓ | [multi] typescript_cross_file: process(kind=function) | 值大于阈值 |
| ✓ | [multi] c_cross_file: calculate(kind=function) | 值大于阈值 |
| ✓ | [multi] c_cross_file: main(kind=function) | 值大于阈值 |
| ✓ | [multi] c_cross_file: main→calculate | 值大于阈值 |
| ✓ | [multi] cpp_cross_file: calculate(kind=function) | 值大于阈值 |
| ✓ | [multi] cpp_cross_file: main(kind=function) | 值大于阈值 |
| ✓ | [multi] java_cross_file: Calculator(kind=class) | 值大于阈值 |
| ✓ | [multi] java_cross_file: calculate(kind=method) | 值大于阈值 |
| ✓ | [multi] java_cross_file: Main(kind=class) | 值大于阈值 |
| ✓ | [multi] java_cross_file: run(kind=method) | 值大于阈值 |
| ✗ | [multi] h_in_pure_c_project: add(kind=function) | 值应大于%s，实际=%s |
| ✓ | [multi] h_in_pure_c_project: main(kind=function) | 值大于阈值 |
| ✓ | [multi] h_in_cpp_project: add(kind=function) | 值大于阈值 |
| ✓ | [multi] h_in_cpp_project: main(kind=function) | 值大于阈值 |
| ✓ | [multi] go_interface_implementation: Shape(kind=interface) | 值大于阈值 |
| ✓ | [multi] go_interface_implementation: Circle(kind=struct) | 值大于阈值 |
| ✓ | [multi] go_interface_implementation: Area(kind=method) | 值大于阈值 |
| ✓ | [multi] java_interface_implementation: Printable(kind=interface) | 值大于阈值 |
| ✓ | [multi] java_interface_implementation: Report(kind=class) | 值大于阈值 |
| ✓ | [multi] java_interface_implementation: print(kind=method) | 值大于阈值 |
| ✓ | [multi] typescript_interface_implementation: Printable(kind=interface) | 值大于阈值 |
| ✓ | [multi] typescript_interface_implementation: Report(kind=class) | 值大于阈值 |
| ✓ | [multi] typescript_interface_implementation: print(kind=method) | 值大于阈值 |
| ✓ | [multi] cpp_abstract_class: Shape(kind=class) | 值大于阈值 |
| ✓ | [multi] cpp_abstract_class: Circle(kind=class) | 值大于阈值 |
| ✗ | [multi] cpp_abstract_class: area(kind=function) | 值应大于%s，实际=%s |
| ✓ | ERR-001 空文件: 0节点 | 实际值匹配预期 |
| ✓ | ERR-002 纯注释文件: 0节点 | 实际值匹配预期 |
| ✓ | ERR-003 语法错误: 不崩溃(exit=0) | 实际值匹配预期 |
| ✓ | ERR-004 二进制文件: 跳过 | 实际值匹配预期 |
| ✓ | ERR-005 无效UTF-8: 不崩溃(exit=0) | 实际值匹配预期 |
| ✓ | ERR-006 混合语言: Go文件>0 | 值大于阈值 |
| ✓ | ERR-006 混合语言: Python文件>0 | 值大于阈值 |
| ✓ | ERR-006 混合语言: C文件>0 | 值大于阈值 |
| ✓ | ERR-007 隐藏目录: 排除 | 实际值匹配预期 |
