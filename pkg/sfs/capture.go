package sfs

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Capture starts a live packet capture and returns its pcap stream. tcpdump
// does the one privileged step -- opening the capture device -- and writes pcap
// to the pipe this returns; the caller reads it with ReadPorts. BPF access
// (ChmodBPF, or running under sudo) is what tcpdump needs; nothing here does.
//
// ports is the game port set from DetectGamePorts; empty falls back to the
// SmartFoxServer range. The returned stop closes the pipe and stops tcpdump.
func Capture(iface string, ports []int) (stream io.ReadCloser, stop func(), err error) {
	args := append([]string{"-i", iface, "-s", "0", "-U", "-w", "-"}, captureFilter(ports)...)
	cmd := exec.Command("tcpdump", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start tcpdump (needs BPF access -- ChmodBPF or sudo): %w", err)
	}
	stop = func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return out.(io.ReadCloser), stop, nil
}

// captureFilter builds the tcpdump BPF filter arguments for the ports, or the
// range when none were detected.
func captureFilter(ports []int) []string {
	if len(ports) == 0 {
		return []string{"tcp", "portrange", fmt.Sprintf("%d-%d", GamePortLo, GamePortHi)}
	}
	terms := make([]string, len(ports))
	for i, p := range ports {
		terms[i] = fmt.Sprintf("port %d", p)
	}
	return strings.Fields("tcp and (" + strings.Join(terms, " or ") + ")")
}
