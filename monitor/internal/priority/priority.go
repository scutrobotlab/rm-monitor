package priority

import (
	"strings"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func ForSchools(items []common.PriorityItem, schools ...string) int {
	maxPriority := 0
	for _, item := range items {
		school := strings.TrimSpace(item.School)
		if school == "" {
			continue
		}
		for _, candidate := range schools {
			if strings.TrimSpace(candidate) == school && item.Priority > maxPriority {
				maxPriority = item.Priority
			}
		}
	}
	return maxPriority
}
