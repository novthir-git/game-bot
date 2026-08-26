# game-bot

基于图像识别的安卓模拟器游戏自动化框架。

宿主环境：**MuMu 模拟器 12**，语言：**Go**，识别路线：**截图 + 模板匹配 / 比色**

游戏画面由引擎渲染成单张 Canvas，取不到控件树，无法走 uiautomator 路线，只能靠图像识别。

## 技术取舍

**零 cgo，零外部 CV 依赖。** 整个项目只依赖 `gopkg.in/yaml.v3`，其余全是标准库：

| 能力 | 实现 |
|---|---|
| ADB 连接 | 调用 adb 可执行文件 |
| 截图 | `screencap` 原始格式，自解析头部，不走 PNG 编码 |
| 模板匹配 | 手写 ZNCC，与 OpenCV 的 `TM_CCOEFF_NORMED` 同量纲 |
| 比色 / 进度条 | 切片索引 |
| 任务调度 | 单循环 + 优先级，`time` 标准库 |
| 日志 | `log/slog` |

**不使用 OCR。** bot 需要的是状态判断而非数值读取；计数和计时一律由程序自己维护，
不从屏幕上读。详见 `games/my_garden_world/config/game.yaml` 的 `ocr` 段。

## 构建

```
go build ./...
go test ./...
```

## 使用

```
gardenbot doctor    检查 adb、设备连接、分辨率、前台包名，并列出缺失的模板图
gardenbot capture   截一张图保存到本地，用于制作模板
gardenbot run       启动调度器，按 tasks.yaml 执行已启用的任务
```

首次使用顺序：

```
1. gardenbot doctor                       确认设备连通，并采集游戏包名
2. gardenbot capture                      截图
3. 按 templates/README.md 裁剪模板图
4. go run ./cmd/tune -screen <截图> -template <模板>    核对匹配分数、定阈值
5. gardenbot run
```

`cmd/tune` 是离线调参工具：给它一张截图和一张模板，它报告匹配分数、位置，
以及**最高分与次高分的差距**——差距大小决定这张模板够不够有辨识度，
比单看最高分有用得多。加 `-out` 可以输出标注图。

## 目录结构

```
cmd/
  gardenbot/       主程序：doctor / capture / run
  tune/            模板调参工具
internal/
  config/          配置加载，支持 local.yaml 覆盖层
  device/          ADB 封装、screencap 解析、坐标归一化
  vision/          ZNCC 模板匹配、比色、进度条测量、模板缓存
  task/            识别原语（Find/Click/WaitFor/WaitVanish）、调度器、异常恢复
games/             每个游戏一个独立目录
  my_garden_world/ 我的花园世界
    README.md      游戏档案 + 自动化目标
    docs/          玩法与可行性分析
    config/        设备、游戏、任务配置
    templates/     模板图（按界面分目录）
    tasks/         该游戏的任务实现
    logs/          运行日志与失败截图（不入库）
```

新增一个游戏 = 建一个 `games/<名字>/` 目录，写配置、截模板、实现任务并注册。
`internal/` 下的东西与具体游戏无关，不需要改。

## 使用边界

本仓库仅做 **单账号、基于截图识别的外部自动化**，用于替代重复点击。

不做也不接受以下用途：内存读写 / 注入 hook、绕过反作弊检测、多开刷号套取实物或现金奖励。
`scheduler.single_account_only` 为 false 时程序会拒绝启动。

使用者需自行确认所自动化的游戏其用户协议是否允许，并自行承担账号风险。
