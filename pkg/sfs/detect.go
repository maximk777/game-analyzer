package sfs

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Endpoint is a game server the client is connected to.
type Endpoint struct {
	IP   string
	Port int
}

// lsofConn matches the remote end of an established TCP line from lsof, e.g.
// "192.168.10.12:52522->172.65.236.57:7003 (ESTABLISHED)".
var lsofConn = regexp.MustCompile(`->([0-9.]+):(\d+) \(ESTABLISHED\)`)

// DetectGamePorts finds the game server connections the CoinPoker client holds
// right now, by asking the OS which sockets it has open. The game port is not
// fixed -- it is handed to the client per table (seen on :7000, :7002, :7003) --
// so it must be read from the live client rather than assumed.
//
// The established TCP connections to anything other than :443 are the game
// links; :443 is the REST/CDN/websocket traffic, which carries nothing we read.
// Returns the distinct endpoints, or an empty slice if the client is not
// connected (not running, or sat in the lobby).
func DetectGamePorts() ([]Endpoint, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:ESTABLISHED").Output()
	if err != nil {
		// lsof exits non-zero when it has nothing to report on some of the file
		// descriptors it probed, even as it prints the lines we want; the output
		// is still usable, so parse it rather than failing.
		if len(out) == 0 {
			return nil, err
		}
	}
	return parseEndpoints(string(out)), nil
}

// parseEndpoints pulls the game endpoints out of lsof output: lines belonging to
// a CoinPoker process, established, to a remote port other than 443.
func parseEndpoints(lsofOut string) []Endpoint {
	seen := make(map[Endpoint]bool)
	var out []Endpoint
	for _, line := range strings.Split(lsofOut, "\n") {
		if !strings.Contains(line, "CoinPoker") {
			continue
		}
		m := lsofConn.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		port, err := strconv.Atoi(m[2])
		if err != nil || port == 443 {
			continue
		}
		e := Endpoint{IP: m[1], Port: port}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// Ports returns the distinct ports of a set of endpoints.
func Ports(eps []Endpoint) []int {
	seen := make(map[int]bool)
	var out []int
	for _, e := range eps {
		if !seen[e.Port] {
			seen[e.Port] = true
			out = append(out, e.Port)
		}
	}
	return out
}
