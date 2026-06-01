package logic

import (
	"context"
	"os"
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/match-controller/internal/config"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
)

func TestNewMatchScanLogic(t *testing.T) {
	if os.Getenv("RM_MONITOR_INTEGRATION") == "" {
		t.Skip("set RM_MONITOR_INTEGRATION=1 to run live monitor scan")
	}
	var c config.Config
	app.MustLoadConfig("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	err := NewMatchScanLogic(context.TODO(), svcCtx).MatchScan()
	if err != nil {
		t.Errorf("MatchScan failed: %v", err)
	} else {
		t.Log("MatchScan succeeded")
	}
}

func TestWinnersFromDelta(t *testing.T) {
	prev := processedSnapshot{Status: "STARTED", RedWinGameCount: 0, BlueWinGameCount: 0}
	cur := scannedMatch{RedWinGameCount: 2, BlueWinGameCount: 1}
	got := winnersFromDelta(prev, cur, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 winners, got %d", len(got))
	}
	if got[0] != "red" || got[1] != "red" || got[2] != "blue" {
		t.Fatalf("unexpected winners: %#v", got)
	}
}

func TestWinnersFromDeltaDoesNotInventDraw(t *testing.T) {
	prev := processedSnapshot{Status: "STARTED", RedWinGameCount: 0, BlueWinGameCount: 2}
	cur := scannedMatch{Status: "DONE", RedWinGameCount: 0, BlueWinGameCount: 2}
	got := winnersFromDelta(prev, cur, 1)
	if len(got) != 0 {
		t.Fatalf("expected no invented winner without score delta, got %#v", got)
	}
}

func TestMatchDecidedPreventsNextRound(t *testing.T) {
	m := scannedMatch{TotalRounds: 3, RedWinGameCount: 0, BlueWinGameCount: 2}
	if !matchDecided(m) {
		t.Fatal("BO3 should be decided after two wins")
	}
	if got := m.RoundNo(); got != 3 {
		t.Fatalf("source score would point at round %d; caller must suppress it when decided", got)
	}
}

func TestAuthoritativeWinnersReplacesTrailingDraw(t *testing.T) {
	blue := matchround.WinnerBlue
	draw := matchround.WinnerDraw
	rounds := []*ent.MatchRound{
		{RoundNo: 1, Winner: &blue},
		{RoundNo: 2, Winner: &blue},
		{RoundNo: 3, Winner: &draw},
	}
	got := authoritativeWinners(rounds, 0, 2)
	if len(got) != 2 || got[0] != matchround.WinnerBlue || got[1] != matchround.WinnerBlue {
		t.Fatalf("unexpected authoritative winners: %#v", got)
	}
}

func TestNormalizeMatchResult(t *testing.T) {
	for _, value := range []string{"", "EMPTY", "PENDING"} {
		if got := normalizeMatchResult(value); got != "UNKNOWN" {
			t.Fatalf("normalizeMatchResult(%q) = %q", value, got)
		}
	}
	if got := normalizeMatchResult("BLUE"); got != "BLUE" {
		t.Fatalf("normalizeMatchResult(BLUE) = %q", got)
	}
}

func TestTeamNeedsUpdateOnlyOnChangedFields(t *testing.T) {
	existing := &ent.Team{ID: "team-1", Name: "A", SchoolName: "S", SchoolLogo: "logo"}
	if teamNeedsUpdate(existing, scannedTeam{ID: "team-1", Name: "A", SchoolName: "S", SchoolLogo: "logo"}) {
		t.Fatal("same team payload should not update")
	}
	if !teamNeedsUpdate(existing, scannedTeam{ID: "team-1", Name: "B", SchoolName: "S", SchoolLogo: "logo"}) {
		t.Fatal("changed team payload should update")
	}
}

func TestMatchNeedsUpdateOnlyOnChangedFields(t *testing.T) {
	slug := "slug-1"
	winner := "winner"
	loser := "loser"
	existing := &ent.Match{
		ID:                    "match-1",
		Event:                 "event",
		Zone:                  "zone",
		Order:                 1,
		MatchType:             "BO3",
		MatchSlug:             &slug,
		TotalRounds:           3,
		Priority:              7,
		Result:                match.ResultUNKNOWN,
		WinnerPlaceholderName: &winner,
		LoserPlaceholderName:  &loser,
		LatestStatus:          "STARTED",
		Edges: ent.MatchEdges{
			RedTeam:  &ent.Team{ID: "red"},
			BlueTeam: &ent.Team{ID: "blue"},
		},
	}
	next := scannedMatch{
		ID:              "match-1",
		Event:           "event",
		Zone:            "zone",
		Order:           1,
		Status:          "STARTED",
		MatchType:       "BO3",
		MatchSlug:       "slug-1",
		TotalRounds:     3,
		Result:          "UNKNOWN",
		WinnerPlacehold: "winner",
		LoserPlacehold:  "loser",
		RedTeam:         scannedTeam{ID: "red"},
		BlueTeam:        scannedTeam{ID: "blue"},
	}
	if matchNeedsUpdate(existing, next, "STARTED", 7) {
		t.Fatal("same match payload should not update")
	}
	next.Result = "RED"
	if !matchNeedsUpdate(existing, next, "DONE", 7) {
		t.Fatal("changed match payload should update")
	}
}
