SWIFT_SHARED := pkg/capture/table_vision.swift pkg/capture/card_templates.swift

.PHONY: all vision assets test test-race clean

all: assets vision

# Card references, read out of the installed client. Optional: without them the
# recogniser falls back to text recognition. Nothing is redistributed -- the
# assets land under bin/, which is not in the repository.
assets:
	@python3 tools/extract_client_assets.py

# The Swift binaries each compile the shared table analyser alongside their own
# entry point, so table_vision.swift stays the single source of card
# recognition for the live agent, the offline harness and the recorder.
vision: bin/mac_vision_agent bin/parse_image bin/diag_recorder

bin/mac_vision_agent: $(SWIFT_SHARED) pkg/capture/mac_vision_agent.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

bin/parse_image: $(SWIFT_SHARED) pkg/capture/parse_image_tool.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

bin/diag_recorder: $(SWIFT_SHARED) pkg/capture/diag_recorder.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f bin/mac_vision_agent bin/parse_image bin/diag_recorder
