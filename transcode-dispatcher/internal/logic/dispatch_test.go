package logic

import (
	"testing"
	"time"
)

func TestInAllowedWindowOvernight(t *testing.T) {
	cases := []struct {
		name string
		now  string
		want bool
	}{
		{name: "before start", now: "22:59", want: false},
		{name: "at start", now: "23:00", want: true},
		{name: "after midnight", now: "01:30", want: true},
		{name: "before end", now: "05:59", want: true},
		{name: "at end", now: "06:00", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			clock, err := time.Parse("15:04", tt.now)
			if err != nil {
				t.Fatal(err)
			}
			got, err := inAllowedWindow(clock, "23:00-06:00")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("inAllowedWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}
