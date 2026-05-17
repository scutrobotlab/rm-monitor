package main

import "testing"

func TestRemoteArtifactPath(t *testing.T) {
	tests := []struct {
		name string
		base string
		in   string
		want string
		ok   bool
	}{
		{name: "relative chinese path", base: "/records", in: "赛事/赛区/55. A-B VS C-D/Round-1/红方.flv", want: "赛事/赛区/55. A-B VS C-D/Round-1/红方.flv", ok: true},
		{name: "absolute under base", base: "/records", in: "/records/Event/Zone/file.flv", want: "Event/Zone/file.flv", ok: true},
		{name: "clean relative", base: "/records", in: "Event/Zone/../file.flv", want: "Event/file.flv", ok: true},
		{name: "outside absolute", base: "/records", in: "/tmp/file.flv", ok: false},
		{name: "escape relative", base: "/records", in: "../file.flv", ok: false},
		{name: "empty", base: "/records", in: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := remoteArtifactPath(tt.base, tt.in)
			if tt.ok && err != nil {
				t.Fatalf("remoteArtifactPath() error = %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("remoteArtifactPath() expected error, got %q", got)
			}
			if got != tt.want {
				t.Fatalf("remoteArtifactPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
