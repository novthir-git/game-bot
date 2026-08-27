# 开发计划

> 更新于 2026-08-26，对应 commit `a08e8e9`。
> 每完成一项就勾掉，并把「待验证的假设」里被证实/证伪的结论回填到对应配置。

## 当前状态

| 项 | 状态 |
|---|---|
| 框架（device / vision / task / config / state） | ✅ 完成，46 个测试全绿 |
| 工具链（doctor / capture / crop / tune / run） | ✅ 完成 |
| P0 三个任务的流程代码 | ✅ 写完，**未经真机验证** |
| 模板图 | ❌ 0/13 —— **唯一阻塞项** |
| `app.package` / `main_activity` | ❌ 未填 |

---

## 阶段 0：模板采集（阻塞项）

这一步只能在装了 MuMu 的机器上做，代码侧没有任何可以提前完成的部分。

### 0.1 固定模拟器

- [ ] MuMu「设置 → 显示」改为**手机版 1280×720 / DPI 240**，之后不再改动

> 分辨率或 DPI 一变，全部模板作废。框架会缩放截图兜底，但缩放会让边缘变糊，
> 锐利描边的按钮首当其冲，识别率明显下降。

### 0.2 采集包名

- [ ] 把游戏切到前台，运行 `gardenbot doctor`
- [ ] 把输出里的 `app.package` / `main_activity` 片段粘进 `config/game.yaml`

### 0.3 截图与裁剪

需要 **5 张完整截图**，从中裁出 **13 个模板**。不是一个模板截一次图。

```
gardenbot capture                                     截完整截图
gardenbot crop -screen <截图> -grid grid.png          生成坐标网格图，读出区域坐标
gardenbot crop -screen <截图> -rect x,y,w,h -o <路径>   裁剪并自动验证
```

- [ ] **主界面** → `main/anchor_main.png`、`main/btn_flower_rack.png`、`main/btn_pearl.png`、`main/btn_waterwheel.png`、`resources/icon_pearl_ready.png`
- [ ] **花架界面（架上有货）** → `flower_rack/anchor_rack.png`、`flower_rack/btn_delist.png`
- [ ] **花架界面（架上空着）** → `flower_rack/state_slot_empty.png`、`flower_rack/btn_list.png`、`flower_rack/btn_confirm.png`
- [ ] **水车界面** → `resources/btn_collect_water.png`
- [ ] **珍珠采集界面** → `resources/btn_collect_pearl.png`
- [ ] **任意弹窗** → `common/btn_close.png`

裁剪原则见 `templates/README.md`。每裁一张就看 `crop` 输出的
**最高分与次高分之差**：≥0.15 可用，0.05~0.15 需收窄或加 ROI，<0.05 必须重裁。

### 验收标准

```
gardenbot doctor
```

`[结论]` 一段显示「环境就绪」。

---

## 阶段 1：首次真机跑通

模板齐了之后的第一轮调试。**逐个任务单独开启**，不要一次全开——
同时开三个任务，出问题时分不清是哪一个的模板或时序不对。

- [ ] 只开 `waterwheel_collect`，跑通一轮
- [ ] 只开 `pearl_harvest`，跑通一轮
- [ ] 只开 `flower_rack_cycle`，跑通至少三轮（覆盖「下架 → 重上」的完整循环）
- [ ] 三个一起开，连续跑 2 小时不卡死
- [ ] 中途 Ctrl-C，确认日志里是正常退出而非「任务失败」
- [ ] 重启进程，确认花架进度从 `logs/state.json` 恢复

### 预期需要调整的

| 位置 | 参数 | 说明 |
|---|---|---|
| `config/game.yaml` | `matching.default_threshold` | 0.78 是估计值，看实际匹配分数再定 |
| `tasks/tasks.go` | `waitPanel` / `waitAction` | 界面动画时长只能实测 |
| `config/tasks.yaml` | `relist_interval_sec` | 攻略说 4 分钟，需实测 |
| 各任务的 `s.Find` 调用 | 加 `task.WithROI(...)` | `crop` 的输出直接给了可用的 `vision.Rect` |

---

## 阶段 2：P1 任务

按顺序做，每个都在 `tasks.yaml` 里有对应开关。

- [ ] **`popup_guard`** —— 通用弹窗兜底。先做这个：它决定能否长时间无人值守，
      其余任务的稳定性都依赖它
- [ ] **`daily_quests`** —— 花灵密令小屋日常任务 → 领 100 体力。
      需要补 `daily/anchor_daily.png` 及若干模板
- [ ] **`submit_orders`** —— 居民订单 / 顾客订单 / 宫廷特供

---

## 阶段 3：P2

- [ ] **`event_rotation`** —— 四大活动轮转（百花成蜜 → 花笺集芳 → 碎玉成瓶 → 莳花纪闻）。
      每个活动界面不同，需按配置驱动的任务表做成独立状态机模块，不要硬编码

---

## 待验证的假设

这些写进代码或配置时是按公开攻略取的值，**只有真机能确认**。
验证后请回填到对应位置，并更新本文件。

| 假设 | 当前取值 | 位置 | 验证方式 |
|---|---|---|---|
| 花架目标次数是 135 | `target_count: 135` | `tasks.yaml` | 看游戏内任务描述 |
| 135 次是**每日**任务而非一次性累计 | `reset_daily: true` | `tasks.yaml` | 跨天后看计数是否清零。取 true 是更安全的一侧 |
| 上架后 4 分钟可下架 | `relist_interval_sec: 255` | `tasks.yaml` | 实测下架按钮何时变为可用 |
| 珍珠每 2 小时一次 | `interval_sec: 7200` | `tasks.yaml` | 实测 |
| 水车 1 小时不会溢出 | `interval_sec: 3600` | `tasks.yaml` | 看水车容量与产出速率 |
| 订单**带花卉图标**而非纯文字 | 未实现 | —— | 决定订单任务能否避开 OCR |
| 最高价花艺品的选取方式 | 未实现 | `flowerrack.go` | 若列表默认按价格排序，点第一项即可；否则需要「最高价」标识模板 |

---

## 明确不做

| 项 | 原因 |
|---|---|
| 激励视频广告自动播放 | 时长不定、关闭按钮位置随机、多为第三方 SDK 界面且可能跳出外部应用。收益只有每日一次 4 小时双倍，实现成本却最高 |
| OCR | bot 需要的是状态判断而非数值读取。计数与计时由程序自己维护，不读屏 |
| 多开 / 批量账号 | `scheduler.single_account_only` 为 false 时程序拒绝启动 |
| 内存读写、注入 hook、绕过反作弊 | 超出本项目范围 |

> 本游戏的鲜花可兑换线下实物配送。以套取实物奖励为目标的自动化不在本项目范围内，
> 详见 `analysis.md` 的「风险与边界」。

---

## 已知技术债

按严重度排序。都不阻塞阶段 0/1，但值得记着。

1. **`prefer_highest_price` 未实现** —— 配置里有这个开关，代码目前走默认上架按钮。
   要等看到实际界面才能定实现方式。**这是配置与实现不一致，优先补上或先删掉该配置项。**
2. **恢复流程未经真机验证** —— `Session.Recover` 的四级升级（关弹窗 → 点空白 →
   连按返回 → 重启应用）只在代码层面走通，实际弹窗的关闭方式可能都不一样。
3. **`FindAll` 的复算余量是启发式** —— 取 `max(4*maxN, 32)`。抽样误差极端时
   理论上仍可能漏掉真正的最高分。目前没有更好的界，若发现漏检再调。
4. **全屏搜索约 350ms** —— ROI 搜索 16ms。对分钟级节奏够用，
   但任务里应尽量带 ROI，别习惯性全屏搜。
5. **`state.json` 目前只有花架在用** —— 其他任务若也需要跨重启的状态，
   照 `flowerrack.go` 的 `load`/`save` 模式加即可。
6. **`daily/anchor_daily.png` 已从必需清单移除** —— 等 `daily_quests` 启用时
   它会重新成为必需，届时由该任务的 `RequiredTemplates()` 声明。
