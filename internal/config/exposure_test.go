package config

import "testing"

// The exposure line is the shared voice of two surfaces (config UI + launch
// lines), so its exact wording is pinned: a drift here is a user-facing drift.
func TestExposureLine(t *testing.T) {
	cases := []struct {
		name string
		e    Exposure
		want string
	}{
		{
			name: "bare box at launch",
			e:    Exposure{Workspace: true},
			want: "/workspace rw · network open",
		},
		{
			name: "bare config in the UI",
			e:    Exposure{},
			want: "network open",
		},
		{
			name: "typical open box",
			e:    Exposure{Workspace: true, Mounts: 2, Ports: 1, Env: 4},
			want: "/workspace rw · 2 host mounts · 1 port · 4 env vars · network open",
		},
		{
			name: "singulars",
			e:    Exposure{Mounts: 1, Ports: 1, Env: 1},
			want: "1 host mount · 1 port · 1 env var · network open",
		},
		{
			name: "disabled mounts split out",
			e:    Exposure{Mounts: 2, DisabledMounts: 1},
			want: "2 host mounts (+1 disabled) · network open",
		},
		{
			name: "only disabled mounts still shown",
			e:    Exposure{DisabledMounts: 1},
			want: "0 host mounts (+1 disabled) · network open",
		},
		{
			name: "firewalled",
			e:    Exposure{Posture: "deny-by-default", Egress: 6},
			want: "network deny-by-default · egress 6 hosts",
		},
		{
			name: "maximally locked",
			e:    Exposure{Posture: "deny-by-default"},
			want: "network deny-by-default · egress none",
		},
		{
			name: "posture degraded by raw config",
			e:    Exposure{Posture: "deny-by-default", Egress: 1, RawRunArgs: true, RawBuild: true},
			want: "network deny-by-default · egress 1 host  (declared; raw run_args + raw build lines present — not guaranteed)",
		},
		{
			// An open network is the default world, not a grant: raw config
			// degrades nothing because nothing is claimed.
			name: "raw config without a posture claims nothing",
			e:    Exposure{RawRunArgs: true},
			want: "network open",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.Line(); got != tc.want {
				t.Errorf("Line() = %q, want %q", got, tc.want)
			}
		})
	}
}
