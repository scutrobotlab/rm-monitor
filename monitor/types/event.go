package types

import "fmt"

const (
	MatchStatusDone    = "DONE"
	MatchStatusSTARTED = "STARTED"
	MatchStatusWaiting = "WAITING"
	// MatchStatusPending 待确定
	MatchStatusPending = "PENDING"
)

const (
	MatchResultBlueWin = "BLUE"
	MatchResultRedWin  = "RED"
	MatchResultDraw    = "DRAW"
)

type Team struct {
	Name       string
	SchoolName string
	SchoolLogo string
	Score      int64
}

type Match struct {
	Id               string
	Order            int64
	Status           string
	Result           string
	BlueTeam         Team
	RedTeam          Team
	BlueWinGameCount int64
	RedWinGameCount  int64
	TotalRounds      int64
	MatchType        string
	MatchSlug        string
	ZoneName         string
	EventName        string
}

func (m *Match) Round() int64 {
	return m.BlueWinGameCount + m.RedWinGameCount + 1
}

func (m *Match) GetMatchStartKey() string {
	return fmt.Sprintf("match:start:%s", m.Id)
}

func IsMatchStart(key string) bool {
	return key[:len("match:start:")] == "match:start:"
}

func (m *Match) GetMatchNewRoundKey() string {
	return fmt.Sprintf("match:newRound:%s", m.Id)
}

func IsMatchNewRound(key string) bool {
	return key[:len("match:newRound:")] == "match:newRound:"
}

func (m *Match) GetMatchDoneKey() string {
	return fmt.Sprintf("match:done:%s", m.Id)
}

func IsMatchDone(key string) bool {
	return key[:len("match:done:")] == "match:done:"
}

