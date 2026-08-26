# 模板图规范

## 采集流程

```
gardenbot capture                                    截一张完整截图
gardenbot crop -screen <截图> -grid grid.png         生成坐标网格图，读出区域坐标
gardenbot crop -screen <截图> -rect x,y,w,h -o <路径>  裁剪并自动验证
```

一张完整截图能裁出多个模板，不需要一个模板截一次图。13 个模板大约 5 张截图即可：

| 截这张 | 能裁出 |
|---|---|
| 主界面 | `main/anchor_main.png`、`main/btn_flower_rack.png`、`main/btn_pearl.png`、`main/btn_waterwheel.png`、`resources/icon_pearl_ready.png` |
| 花架界面（架上有货） | `flower_rack/anchor_rack.png`、`flower_rack/btn_delist.png` |
| 花架界面（架上空着） | `flower_rack/state_slot_empty.png`、`flower_rack/btn_list.png`、`flower_rack/btn_confirm.png` |
| 水车界面 | `resources/btn_collect_water.png` |
| 珍珠采集界面 | `resources/btn_collect_pearl.png` |
| 任意弹窗 | `common/btn_close.png` |

## 采集要求

所有模板图必须在 **1280x720 / DPI 240**（MuMu「设置 - 显示」固定为手机版）下截取，
与 `config/device.yaml` 的 `display.base_*` 保持一致。分辨率不一致会导致全部匹配失效。

## 截取原则

本游戏是水彩国风，低对比度、渐变背景、花朵摇曳动画与粒子特效都会干扰匹配。因此：

1. **只截高对比区域** —— 按钮上的图标或文字，不要整块半透明面板
2. **避开动画区域** —— 花朵、水面、光效、飘落粒子所在位置一律不取
3. **尽量小** —— 能唯一确定目标的最小区域即可，越大越容易被背景变化打断
4. **避开数字** —— 数量会变，把数字部分裁掉，只留固定的图标或标签

## 命名规范

```
<用途>_<对象>[_<状态>].png
```

- 界面锚点：`anchor_<界面名>.png` —— 用于判断当前处于哪个界面
- 按钮：    `btn_<功能>.png` —— 例：`btn_confirm.png`、`btn_close.png`
- 状态判定：`state_<对象>_<状态>.png` —— 例：`state_rack_empty.png`、`state_pearl_ready.png`
- 图标：    `icon_<对象>.png` —— 例：`icon_water.png`

全部小写，下划线分隔，不用中文文件名。

## 目录划分

| 目录 | 内容 |
|---|---|
| `common/` | 跨界面通用：弹窗关闭 ✕、返回、确认/取消、主界面回归按钮 |
| `main/` | 主界面锚点与各功能入口 |
| `flower_rack/` | 花架：上架、下架、售价选择、槽位空/满状态 |
| `daily/` | 花灵密令小屋、日常任务列表、领取按钮 |
| `resources/` | 水车、珍珠采集点、资源栏图标 |

## 调参提示

默认匹配阈值 `0.78`（见 `config/game.yaml` 的 `matching.default_threshold`），默认转灰度。

`crop` 保存后会自动把模板放回原截图搜一遍，看输出里的**最高分与次高分之差**：

| 差距 | 含义 |
|---|---|
| ≥ 0.15 | 辨识度充足，默认阈值可用 |
| 0.05 ~ 0.15 | 偏小，画面稍有变化就可能误判；收窄范围或加 ROI |
| < 0.05 | 在画面里不唯一，运行时几乎一定点错地方，必须重裁 |

若某张图匹配不稳定，按顺序尝试：
1. 缩小截取范围，只留最有辨识度的部分
2. 加 ROI 限定搜索区域——`crop` 的输出里直接给了可用的 `vision.Rect`
3. 单独为该模板降低阈值（不要全局降，会引发误匹配）
4. 该元素本身有动画 —— 换一个静止的参照物做锚点
