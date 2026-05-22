# support-ops demo Makefile
#
# Targets
#   make demo          : alias for demo-offline (deterministic, no AWS).
#   make demo-offline  : 5-ticket batch through KIFF using the offline
#                        fixture. Produces five distinct outcomes:
#                        auto_executed, approval_required (granted→
#                        executed), blocked_consent_missing, escalated,
#                        closed.
#   make demo-bedrock  : same script with real Agno + AwsBedrock
#                        inference. Provide creds in kiff-framework/.env
#                        or examples/support-ops/agent/.env.
#   make test          : `go test ./examples/support-ops/...`
#   make build         : build the demo server binary into ./bin
#   make clean         : remove ./bin and any stale port files

EXAMPLE_DIR  := $(shell pwd)
REPO_ROOT    := $(abspath $(EXAMPLE_DIR)/../..)
BIN_DIR      := $(EXAMPLE_DIR)/bin
SERVER_BIN   := $(BIN_DIR)/support-ops-server
PORT_FILE    := $(EXAMPLE_DIR)/.demo-port
SERVER_LOG   := $(EXAMPLE_DIR)/.demo-server.log
PYTHON       ?= python3

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
	cd $(REPO_ROOT) && go build -o $(SERVER_BIN) ./examples/support-ops/server

test:
	cd $(REPO_ROOT) && go test ./examples/support-ops/...

demo: demo-offline

demo-offline: build
	@AGNO_MODEL_PROVIDER=offline \
	  SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  EXAMPLE_DIR=$(EXAMPLE_DIR) \
	  PYTHON=$(PYTHON) \
	  bash $(EXAMPLE_DIR)/scripts/demo.sh

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
