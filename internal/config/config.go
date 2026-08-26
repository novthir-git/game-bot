// Package config 加载 games/<游戏>/config/ 下的三个 YAML。
//
// 约定：每个文件旁边可以放一个同名的 local.yaml 覆盖层，用于安装路径、
// 端口这类因机器而异的设置。local.yaml 已被 .gitignore 忽略，不会进仓库。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------- device.yaml ----------

type Device struct {
	ADB struct {
		Binary            string `yaml:"binary"`
		Host              string `yaml:"host"`
		Port              int    `yaml:"port"`
		ConnectTimeoutSec int    `yaml:"connect_timeout_sec"`
	} `yaml:"adb"`

	Display struct {
		BaseWidth  int `yaml:"base_width"`
		BaseHeight int `yaml:"base_height"`
		BaseDPI    int `yaml:"base_dpi"`
	} `yaml:"display"`

	Capture struct {
		Method        string `yaml:"method"`
		MinIntervalMs int    `yaml:"min_interval_ms"`
	} `yaml:"capture"`

	Input struct {
		Method           string `yaml:"method"`
		PostClickDelayMs int    `yaml:"post_click_delay_ms"`
		TapJitterPx      int    `yaml:"tap_jitter_px"`
	} `yaml:"input"`
}

// Serial 返回 adb 用的设备标识，形如 127.0.0.1:7555。
func (d *Device) Serial() string { return fmt.Sprintf("%s:%d", d.ADB.Host, d.ADB.Port) }

func (d *Device) ConnectTimeout() time.Duration {
	return time.Duration(d.ADB.ConnectTimeoutSec) * time.Second
}
func (d *Device) MinCaptureInterval() time.Duration {
	return time.Duration(d.Capture.MinIntervalMs) * time.Millisecond
}
func (d *Device) PostClickDelay() time.Duration {
	return time.Duration(d.Input.PostClickDelayMs) * time.Millisecond
}

func (d *Device) validate() error {
	if d.ADB.Host == "" {
		return fmt.Errorf("adb.host 不能为空")
	}
	if d.ADB.Port <= 0 || d.ADB.Port > 65535 {
		return fmt.Errorf("adb.port 非法: %d", d.ADB.Port)
	}
	if d.Display.BaseWidth <= 0 || d.Display.BaseHeight <= 0 {
		return fmt.Errorf("display.base_width / base_height 必须为正数")
	}
	if d.ADB.ConnectTimeoutSec <= 0 {
		d.ADB.ConnectTimeoutSec = 10
	}
	return nil
}

// ---------- game.yaml ----------

type Game struct {
	App struct {
		Name             string `yaml:"name"`
		Platform         string `yaml:"platform"`
		Package          string `yaml:"package"`
		MainActivity     string `yaml:"main_activity"`
		LaunchTimeoutSec int    `yaml:"launch_timeout_sec"`
	} `yaml:"app"`

	Matching struct {
		DefaultThreshold float64 `yaml:"default_threshold"`
		Grayscale        bool    `yaml:"grayscale"`
	} `yaml:"matching"`

	OCR struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"ocr"`

	// Anchors 是「界面名 -> 模板路径」，模板路径相对 templates/ 目录。
	Anchors map[string]string `yaml:"anchors"`

	Recovery struct {
		MaxMissBeforeRecover int      `yaml:"max_miss_before_recover"`
		Steps                []string `yaml:"steps"`
		PressBackTimes       int      `yaml:"press_back_times"`
	} `yaml:"recovery"`
}

func (g *Game) LaunchTimeout() time.Duration {
	return time.Duration(g.App.LaunchTimeoutSec) * time.Second
}

func (g *Game) validate() error {
	if g.Matching.DefaultThreshold <= 0 || g.Matching.DefaultThreshold > 1 {
		return fmt.Errorf("matching.default_threshold 应落在 (0,1]，当前 %v", g.Matching.DefaultThreshold)
	}
	if g.OCR.Enabled {
		// 本项目未实现 OCR。配置里写 true 会让人以为它在工作，必须直接拒绝启动。
		return fmt.Errorf("ocr.enabled 为 true，但本项目不包含 OCR 实现。" +
			"数值判定请改用比色 / 模板匹配 / 数字模板，详见 config/game.yaml 中的说明")
	}
	if g.Recovery.MaxMissBeforeRecover <= 0 {
		g.Recovery.MaxMissBeforeRecover = 5
	}
	// 恢复流程的终点是「回到主界面」，没有这个锚点就无法判断是否已恢复。
	if g.Anchors[AnchorMain] == "" {
		return fmt.Errorf("anchors.%s 必须配置：异常恢复要靠它判断是否已回到主界面", AnchorMain)
	}
	return nil
}

// AnchorMain 是主界面锚点在 anchors 里的键名。
// 它是唯一被框架强制要求的锚点，其余锚点属于按需声明，由任务自行使用。
const AnchorMain = "main_screen"

// ---------- tasks.yaml ----------

type Task struct {
	Enabled     bool   `yaml:"enabled"`
	Priority    int    `yaml:"priority"`
	Description string `yaml:"description"`

	// 以下为任务特有字段，只有相关任务会读取。
	TargetCount        int  `yaml:"target_count"`
	RelistIntervalSec  int  `yaml:"relist_interval_sec"`
	IntervalSec        int  `yaml:"interval_sec"`
	ToleranceSec       int  `yaml:"tolerance_sec"`
	PreferHighestPrice bool `yaml:"prefer_highest_price"`
	ResetDaily         bool `yaml:"reset_daily"`
}

func (t Task) Interval() time.Duration  { return time.Duration(t.IntervalSec) * time.Second }
func (t Task) Tolerance() time.Duration { return time.Duration(t.ToleranceSec) * time.Second }
func (t Task) RelistInterval() time.Duration {
	return time.Duration(t.RelistIntervalSec) * time.Second
}

type Tasks struct {
	Tasks map[string]Task `yaml:"tasks"`

	Scheduler struct {
		SingleAccountOnly bool  `yaml:"single_account_only"`
		TickIntervalSec   int   `yaml:"tick_interval_sec"`
		IdleJitterSec     []int `yaml:"idle_jitter_sec"`
	} `yaml:"scheduler"`

	Logging struct {
		Level                   string `yaml:"level"`
		SaveScreenshotOnFailure bool   `yaml:"save_screenshot_on_failure"`
		RetainDays              int    `yaml:"retain_days"`
	} `yaml:"logging"`
}

func (t *Tasks) TickInterval() time.Duration {
	return time.Duration(t.Scheduler.TickIntervalSec) * time.Second
}

// JitterRange 返回每轮之间的随机休息区间。
func (t *Tasks) JitterRange() (lo, hi time.Duration) {
	j := t.Scheduler.IdleJitterSec
	if len(j) != 2 || j[0] < 0 || j[1] < j[0] {
		return 0, 0
	}
	return time.Duration(j[0]) * time.Second, time.Duration(j[1]) * time.Second
}

func (t *Tasks) validate() error {
	if t.Scheduler.TickIntervalSec <= 0 {
		t.Scheduler.TickIntervalSec = 5
	}
	if !t.Scheduler.SingleAccountOnly {
		// 本项目只支持单账号。允许它被静默关掉，等于默许多开刷号，
		// 而多开刷号正是本项目明确排除的用途，所以在这里硬拦。
		return fmt.Errorf("scheduler.single_account_only 必须为 true：本项目只支持单账号运行")
	}
	for name, task := range t.Tasks {
		if !task.Enabled {
			continue
		}
		if task.IntervalSec < 0 || task.RelistIntervalSec < 0 {
			return fmt.Errorf("任务 %s 的时间间隔不能为负", name)
		}
	}
	return nil
}

// ---------- 加载 ----------

// Bundle 是一个游戏的全部配置及其所在目录。
type Bundle struct {
	Dir    string // games/<游戏>
	Device Device
	Game   Game
	Tasks  Tasks
}

// TemplatesDir 返回该游戏的模板图根目录。
func (b *Bundle) TemplatesDir() string { return filepath.Join(b.Dir, "templates") }

// LogsDir 返回该游戏的日志目录。
func (b *Bundle) LogsDir() string { return filepath.Join(b.Dir, "logs") }

// StatePath 返回任务进度的持久化文件路径。
// 放在 logs/ 下而不是游戏目录根部，因为它和日志一样属于运行产物，不该进仓库。
func (b *Bundle) StatePath() string { return filepath.Join(b.LogsDir(), "state.json") }

// Load 读取 gameDir/config/ 下的 device.yaml、game.yaml、tasks.yaml，
// 并叠加同目录的 local.yaml（若存在）。
func Load(gameDir string) (*Bundle, error) {
	b := &Bundle{Dir: gameDir}
	cfgDir := filepath.Join(gameDir, "config")

	files := []struct {
		file string
		dst  any
	}{
		{"device.yaml", &b.Device},
		{"game.yaml", &b.Game},
		{"tasks.yaml", &b.Tasks},
	}

	// 第一遍严格解析：基础配置里出现结构体上没有的字段直接报错。
	// 一个拼错的字段名如果被静默忽略，排查起来会非常费时间。
	raws := make([][]byte, len(files))
	for i, item := range files {
		path := filepath.Join(cfgDir, item.file)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", path, err)
		}
		raws[i] = raw
		if err := decodeStrict(path, raw, item.dst); err != nil {
			return nil, err
		}
	}

	// local.yaml 是单一覆盖层，可以覆盖上面任意文件里的字段。
	local := filepath.Join(cfgDir, "local.yaml")
	if overlay, err := os.ReadFile(local); err == nil {
		var unknown [][]string
		for i, item := range files {
			added, err := applyOverlay(raws[i], overlay, item.dst)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", local, err)
			}
			unknown = append(unknown, added)
		}
		// local.yaml 走宽松解码，写错的字段名不会报错只会被忽略——
		// 而这正是最难排查的一类配置问题。三个文件里都不存在的键路径，
		// 必然是笔误或不存在的设置项，在这里拦下来。
		if bad := intersect(unknown); len(bad) > 0 {
			sort.Strings(bad)
			return nil, fmt.Errorf("%s 中有 %d 个键在任何配置文件里都不存在，"+
				"可能是拼写错误: %s", local, len(bad), strings.Join(bad, ", "))
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 %s: %w", local, err)
	}

	if err := b.Device.validate(); err != nil {
		return nil, fmt.Errorf("device.yaml: %w", err)
	}
	if err := b.Game.validate(); err != nil {
		return nil, fmt.Errorf("game.yaml: %w", err)
	}
	if err := b.Tasks.validate(); err != nil {
		return nil, fmt.Errorf("tasks.yaml: %w", err)
	}
	return b, nil
}

// decodeStrict 解析基础配置，未知字段一律报错。
func decodeStrict(path string, data []byte, dst any) error {
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("解析 %s: %w", path, err)
	}
	return nil
}
