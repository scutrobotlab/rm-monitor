package types

import "scutbot.cn/web/rm-monitor/monitor/types"

type RecordStartedEvent struct{}

type RecordCompletedEvent struct {
	Match *types.Match
	Path  string
	Role  string
}

const RecordCompletedSubject = "record:completed"
