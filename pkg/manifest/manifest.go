package manifest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
)

// WriteMatchReadme writes the match manifest into the match directory.
func WriteMatchReadme(ctx context.Context, client *ent.Client, conf common.RecordConf, matchID string) error {
	conf = conf.WithDefaults()
	m, err := client.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo())
		}).
		Only(ctx)
	if err != nil {
		return err
	}
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return err
	}
	matchDir, err := pathfmt.RenderMatchDir(conf.MatchNameTemplate, conf.MatchDirTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
	})
	if err != nil {
		return err
	}
	fullDir := filepath.Join(conf.BaseDir, filepath.FromSlash(matchDir))
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return err
	}
	unlock, err := lockDir(ctx, fullDir)
	if err != nil {
		return err
	}
	defer unlock()

	tmp := filepath.Join(fullDir, ".README.md.tmp")
	dst := filepath.Join(fullDir, "README.md")
	if err := os.WriteFile(tmp, []byte(renderReadme(m, red, blue)), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return errors.Wrap(err, "rename readme")
	}
	return nil
}

func lockDir(ctx context.Context, dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".README.md.lock")
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func renderReadme(m *ent.Match, red, blue *ent.Team) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s\n\n", markdownText(matchTitle(m, red, blue)))
	buf.WriteString("## 比赛信息\n\n")
	buf.WriteString("| 项目 | 内容 |\n")
	buf.WriteString("| --- | --- |\n")
	writeInfoRow(&buf, "赛事", m.Event)
	writeInfoRow(&buf, "赛区", m.Zone)
	writeInfoRow(&buf, "场次", fmt.Sprintf("%d", m.Order))
	writeInfoRow(&buf, "类型", m.MatchType)
	writeInfoRow(&buf, "红方", teamName(red))
	writeInfoRow(&buf, "蓝方", teamName(blue))
	writeInfoRow(&buf, "状态", displayStatus(m))
	writeInfoRow(&buf, "比分", scoreText(m.Edges.Rounds))
	writeInfoRow(&buf, "胜方", matchWinnerText(m.Edges.Rounds, red, blue))
	writeInfoRow(&buf, "开始时间", formatDisplayTime(firstStartedAt(m.Edges.Rounds)))
	writeInfoRow(&buf, "结束时间", formatOptionalDisplayTime(lastEndedAt(m.Edges.Rounds)))
	writeInfoRow(&buf, "Match ID", m.ID)
	if m.MatchSlug != nil && *m.MatchSlug != "" {
		writeInfoRow(&buf, "Match Slug", *m.MatchSlug)
	}

	buf.WriteString("\n## 小局历程\n\n")
	buf.WriteString("| 小局 | 状态 | 胜方 | 开始时间 | 结束时间 |\n")
	buf.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range m.Edges.Rounds {
		fmt.Fprintf(&buf, "| %d | %s | %s | %s | %s |\n",
			r.RoundNo,
			markdownCell(displayRoundStatus(r.Status)),
			markdownCell(roundWinnerText(r, red, blue)),
			markdownCell(formatDisplayTime(r.StartedAt)),
			markdownCell(formatOptionalDisplayTime(r.EndedAt)),
		)
	}
	return buf.String()
}

func writeInfoRow(buf *bytes.Buffer, key, value string) {
	fmt.Fprintf(buf, "| %s | %s |\n", markdownCell(key), markdownCell(value))
}

func matchTitle(m *ent.Match, red, blue *ent.Team) string {
	return fmt.Sprintf("%d. %s VS %s", m.Order, teamName(red), teamName(blue))
}

func teamName(t *ent.Team) string {
	if t == nil {
		return ""
	}
	switch {
	case t.SchoolName != "" && t.Name != "":
		return t.SchoolName + "-" + t.Name
	case t.SchoolName != "":
		return t.SchoolName
	default:
		return t.Name
	}
}

func matchWinner(rounds []*ent.MatchRound) string {
	redWins, blueWins := 0, 0
	for _, r := range rounds {
		if r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			redWins++
		case matchround.WinnerBlue:
			blueWins++
		}
	}
	switch {
	case redWins > blueWins:
		return "red"
	case blueWins > redWins:
		return "blue"
	case redWins == 0 && blueWins == 0:
		return ""
	default:
		return "draw"
	}
}

func matchWinnerText(rounds []*ent.MatchRound, red, blue *ent.Team) string {
	switch matchWinner(rounds) {
	case "red":
		return "红方（" + teamName(red) + "）"
	case "blue":
		return "蓝方（" + teamName(blue) + "）"
	case "draw":
		return "平局"
	default:
		return ""
	}
}

func scoreText(rounds []*ent.MatchRound) string {
	redWins, blueWins := 0, 0
	for _, r := range rounds {
		if r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			redWins++
		case matchround.WinnerBlue:
			blueWins++
		}
	}
	return fmt.Sprintf("红 %d - %d 蓝", redWins, blueWins)
}

func roundWinner(r *ent.MatchRound) string {
	if r.Winner == nil {
		return ""
	}
	return string(*r.Winner)
}

func roundWinnerText(r *ent.MatchRound, red, blue *ent.Team) string {
	switch roundWinner(r) {
	case string(matchround.WinnerRed):
		return "红方（" + teamName(red) + "）"
	case string(matchround.WinnerBlue):
		return "蓝方（" + teamName(blue) + "）"
	case string(matchround.WinnerDraw):
		return "平局"
	default:
		return ""
	}
}

func displayStatus(m *ent.Match) string {
	hasRound := false
	allEnded := true
	for _, r := range m.Edges.Rounds {
		hasRound = true
		if r.Status == matchround.StatusSTARTED {
			return "进行中"
		}
		if r.Status != matchround.StatusENDED {
			allEnded = false
		}
	}
	if hasRound && allEnded {
		return "已结束"
	}
	switch strings.ToUpper(m.LatestStatus) {
	case "DONE", "ENDED", "FINISHED":
		return "已结束"
	case "STARTED", "RUNNING":
		return "进行中"
	case "PENDING", "WAITING":
		return "未开始"
	default:
		return m.LatestStatus
	}
}

func displayRoundStatus(status matchround.Status) string {
	switch status {
	case matchround.StatusSTARTED:
		return "进行中"
	case matchround.StatusENDED:
		return "已结束"
	default:
		return string(status)
	}
}

func firstStartedAt(rounds []*ent.MatchRound) time.Time {
	var first time.Time
	for _, r := range rounds {
		if r.StartedAt.IsZero() {
			continue
		}
		if first.IsZero() || r.StartedAt.Before(first) {
			first = r.StartedAt
		}
	}
	return first
}

func lastEndedAt(rounds []*ent.MatchRound) *time.Time {
	var last *time.Time
	for _, r := range rounds {
		if r.EndedAt == nil || r.EndedAt.IsZero() {
			continue
		}
		if last == nil || r.EndedAt.After(*last) {
			v := *r.EndedAt
			last = &v
		}
	}
	return last
}

func formatDisplayTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatOptionalDisplayTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatDisplayTime(*t)
}

func markdownText(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}

func markdownCell(s string) string {
	s = markdownText(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "|", `\|`)
}
