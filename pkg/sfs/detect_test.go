package sfs

import "testing"

func TestParseEndpoints(t *testing.T) {
	// Real-shaped lsof output: the game link on :7003, plus :443 API/CDN noise
	// and an unrelated process, all of which must be filtered out.
	out := `COMMAND     PID  USER   FD   TYPE  DEVICE SIZE/OFF NODE NAME
CoinPoker 44201 max   143u  IPv6  0xf1   0t0  TCP 192.168.10.12:52522->172.65.236.57:7003 (ESTABLISHED)
CoinPoker 44243 max    10u  IPv4  0x8b   0t0  TCP 192.168.10.12:52521->34.160.81.0:443 (ESTABLISHED)
CoinPoker 43978 max    25u  IPv4  0xf7   0t0  TCP 192.168.10.12:52524->8.6.112.8:443 (ESTABLISHED)
Safari    12345 max    9u   IPv4  0x11   0t0  TCP 192.168.10.12:50000->1.2.3.4:7003 (ESTABLISHED)`
	eps := parseEndpoints(out)
	if len(eps) != 1 {
		t.Fatalf("got %d endpoints, want 1: %+v", len(eps), eps)
	}
	if eps[0].IP != "172.65.236.57" || eps[0].Port != 7003 {
		t.Errorf("endpoint = %+v, want 172.65.236.57:7003", eps[0])
	}
	if got := Ports(eps); len(got) != 1 || got[0] != 7003 {
		t.Errorf("ports = %v, want [7003]", got)
	}
}

func TestMatchPortsFallsBackToRange(t *testing.T) {
	m := MatchPorts(nil)
	if !m(7000) || !m(7010) || m(6999) || m(7011) {
		t.Error("empty MatchPorts should be the game range")
	}
	m = MatchPorts([]int{7003})
	if !m(7003) || m(7000) {
		t.Error("MatchPorts([7003]) should match only 7003")
	}
}
