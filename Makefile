.PHONY: all overlay agent sniff server ui app harness bench-guard test test-race clean

all: overlay

# The HUD's window: a floating panel that shows only while CoinPoker is in
# front, and steps aside when you switch to anything else. A browser tab cannot
# float above one app and hide behind another, which is the whole point.
bin/overlay: tools/overlay/overlay.swift
	@mkdir -p bin
	swiftc -O $< -o $@

overlay: bin/overlay

# Running the thing, in two halves.
#
# The agent reads the game wire (SmartFoxServer traffic off :700x, no OCR),
# rebuilds the table, decides, and serves the HUD over HTTP/WebSocket. The
# overlay is the window that shows what it decided, pinned to the table.
#
#   make agent HERO_ID=712757     the reader, decisions and HTTP/WS endpoint
#   make ui                       the floating panel, pinned to CoinPoker
#   make app  HERO_ID=712757      both
#
# HERO_ID (or HERO_NAME) tells the agent which seat is yours. It needs BPF
# access for live capture -- install ChmodBPF once, or run under sudo.
PORT ?= 8080
HERO_ID ?=
HERO_NAME ?=

agent:
	go run ./cmd/sfsagent -port $(PORT) \
		$(if $(HERO_ID),-hero-id $(HERO_ID),) $(if $(HERO_NAME),-hero-name $(HERO_NAME),)

# The table reader on its own, in the terminal: the reconstructed roster and
# every decoded message. Useful for checking the capture without the HUD.
sniff:
	go run ./cmd/sfssniff $(if $(PCAP),-r $(PCAP),)

# The plain server, no wire source -- for working on the HUD itself.
server:
	go run ./cmd/server --port $(PORT)

ui: bin/overlay
	bin/overlay --url http://localhost:$(PORT)/hud.html --match CoinPoker

app: bin/overlay
	@echo "starting agent on :$(PORT) and the overlay -- Ctrl-C to stop both"
	@( go run ./cmd/sfsagent -port $(PORT) \
		$(if $(HERO_ID),-hero-id $(HERO_ID),) $(if $(HERO_NAME),-hero-name $(HERO_NAME),) & \
	   AGENT=$$!; \
	   sleep 2; bin/overlay --url http://localhost:$(PORT)/hud.html --match CoinPoker; \
	   kill $$AGENT 2>/dev/null )

# The harness: plays the advisor out over whole hands against simulated
# opponents and reports what following it would have won.
#
#   make harness                     the standard run, against the population
#   make harness FIELD=pro,pro,pro,pro,pro    against a table of regulars
#   make harness HANDS=5000 LINEUPS=64        a longer, tighter measurement
#
# The first candidate is the baseline every other is compared against, hand by
# hand on identical decks. See docs/HARNESS.md.
HANDS ?= 2500
LINEUPS ?= 32
SEED ?= 1
FIELD ?=
CANDIDATES ?= pro,tool:stats,novice:stats@0.9

# Built, not `go run`. A long run and an edit to the source overlap sooner or
# later, and `go run` compiles at the moment it starts: two runs launched from
# one command line then measure two different programs. That has already
# happened once and the numbers looked plausible.
bin/harness: $(shell find pkg cmd -name '*.go' 2>/dev/null)
	@mkdir -p bin
	go build -o $@ ./cmd/harness

harness: bin/harness
	bin/harness -hands $(HANDS) -lineups $(LINEUPS) -seed $(SEED) \
		-candidates $(CANDIDATES) $(if $(FIELD),-field $(FIELD),) \
		-stack-min 100 -stack-max 100

# The same run, with a gate. Exits non-zero if GUARD has fallen more than two
# combined standard errors below its last recorded run of the same shape.
#
#   make bench-guard GUARD=tool:stats
GUARD ?= tool:stats

bench-guard: bin/harness
	bin/harness -hands $(HANDS) -lineups $(LINEUPS) -seed $(SEED) \
		-candidates $(CANDIDATES) $(if $(FIELD),-field $(FIELD),) \
		-stack-min 100 -stack-max 100 -guard $(GUARD)

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f bin/overlay bin/harness
