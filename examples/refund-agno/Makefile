# refund-agno demo Makefile
#
# Targets
#   make demo          : alias for demo-offline. Reproducible, no AWS needed.
#   make demo-offline  : full offline demo with the deterministic fixture.
#                        Spawns the Go server on a free port, runs the
#                        unguarded agent, runs the same agent through KIFF
#                        (auto grant + auto deny + audit), shuts down.
#   make demo-bedrock  : same demo, real Agno + AwsBedrock inference.
#                        Requires AGNO_MODEL_PROVIDER=bedrock plus AWS env
#                        (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
#                        AWS_REGION, BEDROCK_MODEL_ID) — provided via
#                        kiff-framework/.env or agent/.env.
#   make test          : `go test ./examples/refund-agno/...`. The Python
#                        side has no asynchronous logic to test; the Go
#                        integration tests cover the demo HTTP surface.
#   make build         : build the demo server binary into ./bin
#   make clean         : remove ./bin and any stale port files
#
# Env loading order (later overrides earlier):
#   1. kiff-framework/.env  (the framework root, where shared creds live)
#   2. examples/refund-agno/agent/.env  (per-example overrides, optional)

EXAMPLE_DIR  := $(shell pwd)
REPO_ROOT    := $(abspath $(EXAMPLE_DIR)/../..)
BIN_DIR      := $(EXAMPLE_DIR)/bin
SERVER_BIN   := $(BIN_DIR)/refund-agno-server
PORT_FILE    := $(EXAMPLE_DIR)/.demo-port
SERVER_LOG   := $(EXAMPLE_DIR)/.demo-server.log
PYTHON       ?= python3

# Load the framework-level .env first, then the per-example .env. `make`
# does not interpolate quoted values for us, so the script is responsible
# for guarding against malformed lines. Variables not defined here are
# inherited from the calling shell.
ifneq (,$(wildcard $(REPO_ROOT)/.env))
include $(REPO_ROOT)/.env
export
endif

ifneq (,$(wildcard $(EXAMPLE_DIR)/agent/.env))
include $(EXAMPLE_DIR)/agent/.env
export
endif

.PHONY: demo demo-offline demo-bedrock test build clean help

help:
	@echo "Targets: demo demo-offline demo-bedrock test build clean"

build: $(SERVER_BIN)

$(SERVER_BIN):
	@mkdir -p $(BIN_DIR)
	cd $(REPO_ROOT) && go build -o $(SERVER_BIN) ./examples/refund-agno/server

test:
	cd $(REPO_ROOT) && go test ./examples/refund-agno/...

# demo: default alias for the deterministic offline run.
demo: demo-offline

demo-offline: build
	@AGNO_MODEL_PROVIDER=offline \
	  SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  EXAMPLE_DIR=$(EXAMPLE_DIR) \
	  PYTHON=$(PYTHON) \
	  bash $(EXAMPLE_DIR)/scripts/demo.sh

# demo-bedrock: same script, real LLM inference. AGNO_MODEL_PROVIDER must be
# bedrock and AWS env must be present. The agent picks up its config from
# the environment loaded above.
demo-bedrock: build
	@AGNO_MODEL_PROVIDER=bedrock \
	  SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  EXAMPLE_DIR=$(EXAMPLE_DIR) \
	  PYTHON=$(PYTHON) \
	  bash $(EXAMPLE_DIR)/scripts/demo.sh

clean:
	rm -rf $(BIN_DIR) $(PORT_FILE) $(SERVER_LOG)
