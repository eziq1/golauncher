package timer

import (
	"testing"
	"time"
)

// testNow is a Thursday morning.
var testNow = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

func TestParseTimer(t *testing.T) {
	const (
		s = time.Second
		m = time.Minute
		h = time.Hour
	)
	tests := []struct {
		in    string
		d     time.Duration
		label string
	}{
		{"25m", 25 * m, ""},
		{"25M", 25 * m, ""},
		{"1h30m", 90 * m, ""},
		{"1h 30m", 90 * m, ""},
		{"90s", 90 * s, ""},
		{"1.5h", 90 * m, ""},
		{"2h5m10s", 2*h + 5*m + 10*s, ""},
		{"25 min", 25 * m, ""},
		{"25 minutes", 25 * m, ""},
		{"1 hour 30 minutes", 90 * m, ""},
		{"30 sec", 30 * s, ""},
		{"2 hrs", 2 * h, ""},
		{"1 day", 24 * h, ""},
		{"7d", 7 * 24 * h, ""},
		{"25:00", 25 * m, ""},
		{"1:30", 90 * s, ""},
		{"1:30:00", 90 * m, ""},
		{"25", 25 * m, ""},
		{"2.5", 150 * s, ""},
		{"25m 喝水", 25 * m, "喝水"},
		{"25m Tea Time", 25 * m, "Tea Time"},
		{"25 Tea", 25 * m, "Tea"},
		{"1h30m 喝水", 90 * m, "喝水"},
		{"喝水 25m", 25 * m, "喝水"},
		{"Tea 25", 25 * m, "Tea"},
		{"pasta 10 min", 10 * m, "pasta"},
		{"25m: stretch", 25 * m, "stretch"},
		{"５分钟", 5 * m, ""},
		{"25分钟", 25 * m, ""},
		{"25分", 25 * m, ""},
		{"25分钟喝水", 25 * m, "喝水"},
		{"喝水25分钟", 25 * m, "喝水"},
		{"1小时30分钟", 90 * m, ""},
		{"1小时30分", 90 * m, ""},
		{"1小时 30分钟", 90 * m, ""},
		{"5分30秒", 5*m + 30*s, ""},
		{"90秒", 90 * s, ""},
		{"1.5小时", 90 * m, ""},
		{"1个小时", h, ""},
		{"半小时", 30 * m, ""},
		{"半个小时", 30 * m, ""},
		{"半分钟", 30 * s, ""},
		{"一个半小时", 90 * m, ""},
		{"1个半小时", 90 * m, ""},
		{"一刻钟", 15 * m, ""},
		{"三刻钟", 45 * m, ""},
		{"十分钟", 10 * m, ""},
		{"两小时", 2 * h, ""},
		{"二十五分钟", 25 * m, ""},
		{"一百二十秒", 120 * s, ""},
		{"两个钟头", 2 * h, ""},
		{"3天", 72 * h, ""},
		{"十五分钟 煮鸡蛋", 15 * m, "煮鸡蛋"},
	}
	for _, tt := range tests {
		d, label, ok := parseTimer(tt.in)
		if !ok || d != tt.d || label != tt.label {
			t.Errorf("parseTimer(%q) = %v, %q, %v; want %v, %q", tt.in, d, label, ok, tt.d, tt.label)
		}
	}
}

func TestParseTimerInvalid(t *testing.T) {
	for _, in := range []string{
		"", " ", "abc", "25x", "x25m", "0", "0m", "0:00", "8d", "200h", "1:60", "1:5", "25:00:61",
		"m", "h30", "半", "二三分钟", "1..5h", "-5m", "tea", "百分钟",
	} {
		if d, label, ok := parseTimer(in); ok {
			t.Errorf("parseTimer(%q) = %v, %q; want no result", in, d, label)
		}
	}
}

func TestParseReminder(t *testing.T) {
	day := func(d, hh, mm int) time.Time { return time.Date(2026, 10, d, hh, mm, 0, 0, time.UTC) }
	tests := []struct {
		in      string
		at      time.Time
		label   string
		rolled  bool
		defTime bool
		in_     time.Duration
	}{
		{in: "15:30 开会", at: day(15, 15, 30), label: "开会"},
		{in: "15:30开会", at: day(15, 15, 30), label: "开会"},
		{in: "9:00 standup", at: day(16, 9, 0), label: "standup", rolled: true},
		{in: "9am standup", at: day(16, 9, 0), label: "standup", rolled: true},
		{in: "9 am standup", at: day(16, 9, 0), label: "standup", rolled: true},
		{in: "3pm Call Mom", at: day(15, 15, 0), label: "Call Mom"},
		{in: "9:30pm", at: day(15, 21, 30)},
		{in: "12pm lunch", at: day(15, 12, 0), label: "lunch"},
		{in: "12am", at: day(16, 0, 0), rolled: true},
		{in: "noon lunch", at: day(15, 12, 0), label: "lunch"},
		{in: "me at 3pm to call mom", at: day(15, 15, 0), label: "call mom"},
		{in: "tomorrow 9:00 report", at: day(16, 9, 0), label: "report"},
		{in: "tomorrow at 9am report", at: day(16, 9, 0), label: "report"},
		{in: "3pm tomorrow report", at: day(16, 15, 0), label: "report"},
		{in: "today 15:00", at: day(15, 15, 0)},
		{in: "today 9:00", at: day(16, 9, 0), rolled: true},
		{in: "tonight 8pm movie", at: day(15, 20, 0), label: "movie"},
		{in: "tomorrow report", at: day(16, 9, 0), label: "report", defTime: true},
		{in: "friday 5pm drinks", at: day(16, 17, 0), label: "drinks"},
		{in: "fri 5pm", at: day(16, 17, 0)},
		{in: "thursday 9am", at: day(22, 9, 0)}, // today has passed: next week
		{in: "thursday 11am", at: day(15, 11, 0)},
		{in: "next monday 9am", at: day(19, 9, 0)},
		{in: "next thursday 9am", at: day(22, 9, 0)},
		{in: "3pm sunday", at: day(18, 15, 0)},
		{in: "2026-10-20 9:00 dentist", at: day(20, 9, 0), label: "dentist"},
		{in: "2026-10-20 dentist", at: day(20, 9, 0), label: "dentist", defTime: true},
		{in: "in 20m tea", at: testNow.Add(20 * time.Minute), label: "tea", in_: 20 * time.Minute},
		{in: "in 20 tea", at: testNow.Add(20 * time.Minute), label: "tea", in_: 20 * time.Minute},
		{in: "in 1h", at: testNow.Add(time.Hour), in_: time.Hour},
		{in: "明天 9点 交周报", at: day(16, 9, 0), label: "交周报"},
		{in: "明天9点交周报", at: day(16, 9, 0), label: "交周报"},
		{in: "我明天9点交周报", at: day(16, 9, 0), label: "交周报"},
		{in: "明天9点提醒我交周报", at: day(16, 9, 0), label: "交周报"},
		{in: "明天上午9点半 开会", at: day(16, 9, 30), label: "开会"},
		{in: "下午3点 开会", at: day(15, 15, 0), label: "开会"},
		{in: "下午三点开会", at: day(15, 15, 0), label: "开会"},
		{in: "晚上8点", at: day(15, 20, 0)},
		{in: "今晚8点 看电影", at: day(15, 20, 0), label: "看电影"},
		{in: "中午12点 吃饭", at: day(15, 12, 0), label: "吃饭"},
		{in: "中午1点", at: day(15, 13, 0)},
		{in: "凌晨2点", at: day(16, 2, 0), rolled: true},
		{in: "11点一刻", at: day(15, 11, 15)},
		{in: "11点三刻", at: day(15, 11, 45)},
		{in: "11点30", at: day(15, 11, 30)},
		{in: "11点30分", at: day(15, 11, 30)},
		{in: "15时30分 开会", at: day(15, 15, 30), label: "开会"},
		{in: "15点", at: day(15, 15, 0)},
		{in: "9点钟", at: day(16, 9, 0), rolled: true},
		{in: "后天 9:00", at: day(17, 9, 0)},
		{in: "大后天 9:00", at: day(18, 9, 0)},
		{in: "明早8点", at: day(16, 8, 0)},
		{in: "明晚 跑步", at: day(16, 20, 0), label: "跑步", defTime: true},
		{in: "周五 下午5点", at: day(16, 17, 0)},
		{in: "星期日 10:00", at: day(18, 10, 0)},
		{in: "礼拜天 10:00", at: day(18, 10, 0)},
		{in: "下周一 9点 例会", at: day(19, 9, 0), label: "例会"},
		{in: "下个星期四 9:00", at: day(22, 9, 0)},
		{in: "10月20日 下午3点 体检", at: day(20, 15, 0), label: "体检"},
		{in: "10月20号 9:00", at: day(20, 9, 0)},
		{in: "2026年10月20日 9:00", at: day(20, 9, 0)},
		{in: "10月1日 9:00", at: time.Date(2027, 10, 1, 9, 0, 0, 0, time.UTC)}, // passed this year
		{in: "20分钟后 喝水", at: testNow.Add(20 * time.Minute), label: "喝水", in_: 20 * time.Minute},
		{in: "20分钟后喝水", at: testNow.Add(20 * time.Minute), label: "喝水", in_: 20 * time.Minute},
		{in: "半小时以后", at: testNow.Add(30 * time.Minute), in_: 30 * time.Minute},
		{in: "１５：３０ 开会", at: day(15, 15, 30), label: "开会"},
		{in: "24点", at: day(16, 0, 0), rolled: true},
	}
	for _, tt := range tests {
		r, ok := parseReminder(tt.in, testNow)
		if !ok {
			t.Errorf("parseReminder(%q): no result", tt.in)
			continue
		}
		if !r.At.Equal(tt.at) || r.Label != tt.label || r.Rolled != tt.rolled || r.DefaultTime != tt.defTime || r.In != tt.in_ {
			t.Errorf("parseReminder(%q) = %v %q rolled=%v default=%v in=%v; want %v %q rolled=%v default=%v in=%v",
				tt.in, r.At, r.Label, r.Rolled, r.DefaultTime, r.In, tt.at, tt.label, tt.rolled, tt.defTime, tt.in_)
		}
	}
}

func TestParseReminderInvalid(t *testing.T) {
	for _, in := range []string{
		"", "me", "开会", "9 standup", "25:00", "9:70", "9点70分", "13pm", "上午15点", "today", "今天",
		"2026-10-01 9:00", "2026-02-30 9:00", "2月30日 9:00", "in", "in tea", "in 0m", "in 8d",
		"15:3", "15:300", "1530", "下午3pm", "x15:30", "9:30px",
	} {
		if r, ok := parseReminder(in, testNow); ok {
			t.Errorf("parseReminder(%q) = %+v; want no result", in, r)
		}
	}
}

// FuzzParse checks that no input makes the parsers panic and that results
// are in range.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"25m", "1:30:00", "半小时", "明天9点交周报", "in 20m", "下周一 9点", "10月20日", "12am", "一个半小时"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if d, _, ok := parseTimer(s); ok && (d <= 0 || d > maxDuration) {
			t.Fatalf("parseTimer(%q) = %v", s, d)
		}
		if r, ok := parseReminder(s, testNow); ok && !r.At.After(testNow) {
			t.Fatalf("parseReminder(%q) = %v, not after now", s, r.At)
		}
	})
}
