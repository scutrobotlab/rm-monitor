package types

const (
	MatchStatusSTARTED = "STARTED"
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
	BlueTeam         Team
	RedTeam          Team
	BlueWinGameCount int64
	RedWinGameCount  int64
	TotalRounds      int64
	MatchType        string
	MatchSlug        string
	ZoneName         string
	EventName        string
	Report           string
	Result           string
	WinnerText       string
	WinnerPlacehold  string
	LoserPlacehold   string
}
