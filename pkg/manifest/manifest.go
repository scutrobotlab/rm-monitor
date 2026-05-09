package manifest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	fmt.Fprintf(&buf, "# %d. %s-%s VS %s-%s\n\n", m.Order, red.SchoolName, red.Name, blue.SchoolName, blue.Name)
	fmt.Fprintf(&buf, "- Event: %s\n", m.Event)
	fmt.Fprintf(&buf, "- Zone: %s\n", m.Zone)
	fmt.Fprintf(&buf, "- Match ID: %s\n", m.ID)
	fmt.Fprintf(&buf, "- Match Type: %s\n", m.MatchType)
	if m.MatchSlug != nil && *m.MatchSlug != "" {
		fmt.Fprintf(&buf, "- Match Slug: %s\n", *m.MatchSlug)
	}
	fmt.Fprintf(&buf, "- Red: %s - %s\n", red.SchoolName, red.Name)
	fmt.Fprintf(&buf, "- Blue: %s - %s\n", blue.SchoolName, blue.Name)
	fmt.Fprintf(&buf, "- Current Status: %s\n", m.LatestStatus)
	fmt.Fprintf(&buf, "- Winner: %s\n\n", matchWinner(m.Edges.Rounds))
	buf.WriteString("## Rounds\n\n")
	buf.WriteString("| Round | Status | Winner | Started At | Ended At |\n")
	buf.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range m.Edges.Rounds {
		fmt.Fprintf(&buf, "| %d | %s | %s | %s | %s |\n",
			r.RoundNo,
			r.Status,
			roundWinner(r),
			formatTime(r.StartedAt),
			formatOptionalTime(r.EndedAt),
		)
	}
	return buf.String()
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

func roundWinner(r *ent.MatchRound) string {
	if r.Winner == nil {
		return ""
	}
	return string(*r.Winner)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}
