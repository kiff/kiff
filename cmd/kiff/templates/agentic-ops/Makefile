# agentic-ops starter Makefile
#
# Targets
#   make demo          : alias for demo-offline
#   make demo-offline  : start the server, run the unguarded agent, run
#                        the same agent through KIFF, shut down. No AWS
#                        creds needed.
#   make demo-bedrock  : same flow with real Agno + AwsBedrock inference.
#                        Requires AWS env (see agent/.env.example).
#   make test          : `go test ./...`
#   make build         : build ./bin/server
#   make clean         : remove ./bin and any stale port files
#
# Env loading order (later overrides earlier):
#   1. ../.env  (if present at the parent directory; useful when this
#               project sits next to a kiff checkout)
#   2. agent/.env  (per-project overrides)

PROJECT_DIR := $(shell pwd)
BIN_DIR     := $(PROJECT_DIR)/bin
SERVER_BIN  := $(BIN_DIR)/server
PORT_FILE   := $(PROJECT_DIR)/.demo-port
SERVER_LOG  := $(PROJECT_DIR)/.demo-server.log
PYTHON      ?= python3

ifneq (,$(wildcard $(PROJECT_DIR)/../.env))
include $(PROJECT_DIR)/../.env
export
endif

ifneq (,$(wildcard $(PROJECT_DIR)/agent/.env))
include $(PROJECT_DIR)/agent/.env
export
endif

.PHONY: demo demo-offline demo-bedrock test build clean help

help:
	@echo "Targets: demo demo-offline demo-bedrock test build clean"

build: $(SERVER_BIN)

$(SERVER_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(SERVER_BIN) ./cmd/server

test:
	go test ./...

demo: demo-offline

demo-offline: build
	@AGNO_MODEL_PROVIDER=offline \
	  SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  PROJECT_DIR=$(PROJECT_DIR) \
	  PYTHON=$(PYTHON) \
	  bash $(PROJECT_DIR)/scripts/demo.sh

demo-bedrock: build
	@AGNO_MODEL_PROVIDER=bedrock \
	  SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  PROJECT_DIR=$(PROJECT_DIR) \
	  PYTHON=$(PYTHON) \
	  bash $(PROJECT_DIR)/scripts/demo.sh

clean:
	rm -rf $(BIN_DIR) $(PORT_FILE) $(SERVER_LOG)
