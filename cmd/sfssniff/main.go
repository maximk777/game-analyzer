// Command sfssniff reconstructs the live poker table from CoinPoker's game
// traffic on TCP :7000, as a replacement for reading the table off the screen.
//
// It reads a pcap stream: either a file captured earlier (-r), or a live capture
// it starts itself by running tcpdump (the default). tcpdump does the one step
// that needs privilege -- opening the capture device -- and this process, joined
// to it by a pipe, does the rest unprivileged: reassembly, JSON extraction, and
// rebuilding each table's roster.
//
//	# from a saved capture
//	sfssniff -r /tmp/cp_7000.pcap
//
//	# live (needs BPF access: ChmodBPF, or run under sudo)
//	sfssniff -i en0
//
//	# dump every decoded message as JSON instead of the roster view
//	sfssniff -r /tmp/cp_7000.pcap -json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/google/gopacket"

	"poker-game-analyzer/pkg/sfs"
)

func main() {
	iface := flag.String("i", "en0", "capture interface for live capture")
	pcapFile := flag.String("r", "", "read from a pcap file instead of capturing live")
	dumpJSON := flag.Bool("json", false, "print each decoded message as JSON, not the roster")
	dumpRaw := flag.Bool("raw", false, "print every JSON object found, recognised or not (protocol discovery)")
	portFlag := flag.String("port", "", "game port(s), comma-separated; default: auto-detect from the running client")
	flag.Parse()

	ports := parsePorts(*portFlag)
	if *pcapFile == "" && len(ports) == 0 {
		// Live capture with no port given: ask the client which port it is on,
		// since the port is assigned per table and is not always :7000.
		eps, err := sfs.DetectGamePorts()
		if err != nil {
			fmt.Fprintln(os.Stderr, "sfssniff: port detection failed:", err)
		}
		ports = sfs.Ports(eps)
		if len(ports) == 0 {
			fmt.Fprintln(os.Stderr, "sfssniff: no game connection found -- is a table open? falling back to range",
				sfs.GamePortLo, "-", sfs.GamePortHi)
		} else {
			fmt.Fprintf(os.Stderr, "sfssniff: detected game port(s) %v\n", ports)
		}
	}

	src, cleanup, err := openSource(*pcapFile, *iface, ports)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sfssniff:", err)
		os.Exit(1)
	}
	defer cleanup()

	h := newHandler(*dumpJSON)
	h.dumpRaw = *dumpRaw
	if err := sfs.ReadPorts(src, ports, h.factory); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "sfssniff:", err)
		os.Exit(1)
	}
}

// parsePorts reads a comma-separated port list.
func parsePorts(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// bpfFilter builds the tcpdump filter for the given ports, or the port range
// when none were detected.
func bpfFilter(ports []int) string {
	if len(ports) == 0 {
		return fmt.Sprintf("tcp portrange %d-%d", sfs.GamePortLo, sfs.GamePortHi)
	}
	terms := make([]string, len(ports))
	for i, p := range ports {
		terms[i] = fmt.Sprintf("port %d", p)
	}
	return "tcp and (" + strings.Join(terms, " or ") + ")"
}

// openSource returns the pcap byte stream to read, and a cleanup to run when
// done. For a file it is just the open file. For live capture it starts tcpdump
// writing pcap to stdout and returns that pipe, wiring Ctrl-C to stop tcpdump so
// a live run ends cleanly.
func openSource(pcapFile, iface string, ports []int) (io.Reader, func(), error) {
	if pcapFile != "" {
		f, err := os.Open(pcapFile)
		if err != nil {
			return nil, nil, err
		}
		return f, func() { f.Close() }, nil
	}

	// -s 0: whole packet. -U: unbuffered, so messages surface as they arrive
	// rather than in blocks. -w -: pcap to stdout.
	filter := strings.Fields(bpfFilter(ports))
	args := append([]string{"-i", iface, "-s", "0", "-U", "-w", "-"}, filter...)
	cmd := exec.Command("tcpdump", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start tcpdump (need BPF access -- ChmodBPF or sudo): %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}()

	cleanup := func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}
	return out, cleanup, nil
}

// handler owns one roster per connection (one table) and prints changes. It is
// the seam where, later, roster changes become pkg/table events feeding the
// decision pipeline; for now it prints, which is what proves the decode.
type handler struct {
	mu       sync.Mutex
	dumpJSON bool
	dumpRaw  bool
	rosters  map[gopacket.Flow]*sfs.Roster
}

func newHandler(dumpJSON bool) *handler {
	return &handler{dumpJSON: dumpJSON, rosters: make(map[gopacket.Flow]*sfs.Roster)}
}

// factory is a sfs.ConsumerFactory: one Scanner per connection, all routing
// their messages back to this handler.
func (h *handler) factory(flow gopacket.Flow) sfs.StreamConsumer {
	sc := sfs.NewScanner(flow, h.onMessage)
	if h.dumpRaw {
		sc.SetRawHandler(func(flow gopacket.Flow, dir sfs.Direction, obj []byte) {
			h.mu.Lock()
			fmt.Printf("%s %s %s\n", flow, dir, obj)
			h.mu.Unlock()
		})
	}
	return sc
}

func (h *handler) onMessage(flow gopacket.Flow, dir sfs.Direction, m sfs.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.dumpJSON {
		fmt.Printf("%s %s %s\n", flow, dir, m.Raw)
		return
	}

	r, ok := h.rosters[flow]
	if !ok {
		r = sfs.NewRoster()
		h.rosters[flow] = r
	}
	if r.Apply(m) {
		fmt.Printf("── %s ──\n%s\n", flow, r)
	}

	// Dealer chat and showdown are worth surfacing even when they change no
	// roster field: they are the action in words and the cards at the end.
	switch m.Kind {
	case sfs.KindDealerChat:
		fmt.Printf("   dealer: %s\n", m.DealerChat.DealerMessage)
	case sfs.KindShowdown:
		out, _ := json.Marshal(m.Showdown)
		fmt.Printf("   showdown: %s\n", out)
	}
}
