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
| 进度持久化 | JSON 状态文件，临时文件加改名保证原子性 |
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
gardenbot crop      从截图里裁出模板，并立刻验证它在原图中是否唯一
gardenbot run       启动调度器，按 tasks.yaml 执行已启用的任务
```

首次使用顺序：

```
1. gardenbot doctor                                  确认设备连通，并采集游戏包名
2. gardenbot capture                                 截图
3. gardenbot crop -screen <截图> -grid grid.png      生成坐标网格图，照着读区域坐标
4. gardenbot crop -screen <截图> -rect x,y,w,h -o main/btn_xxx.png
                                                     裁模板，保存后自动验证唯一性
5. gardenbot run
```

**不需要为每个模板单独截图。** 一张完整截图可以裁出好几个模板——
主界面那一张就能裁出主界面锚点和各个功能入口。13 个模板大约只要 5 张截图。

`crop` 保存后会把模板放回原截图里搜一遍，报告最高分与次高分的差距。
差距过小说明这块区域在画面里不唯一，运行时会点错地方——这种错误在日志里
表现为「点了但没反应」，极难倒推，所以裁剪当场就要拦下来。
纯色区域会被直接拒绝：它在 ZNCC 下分母为零，永远匹配不上。

`cmd/tune` 是离线调参工具：给它一张截图和一张模板，它报告匹配分数、位置，
以及**最高分与次高分的差距**——差距大小决定这张模板够不够有辨识度，
比单看最高分有用得多。加 `-out` 可以输出标注图。

`crop` 用于制作模板并当场验证，`tune` 用于事后在别的截图上复核已有模板
（比如某张模板在夜晚场景下匹配不上时，拿夜晚的截图跑一遍看分数掉到了多少）。

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
  state/           任务进度持久化，进程重启后自动恢复
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
