# Refund scenario Makefile
#
#   make demo   : start the server, run the curl walkthrough, shut down
#   make test   : go test ./...
#   make verify : kiff verify ./domain (requires the kiff CLI on PATH)
#   make build  : build ./bin/server
#   make clean  : remove build artifacts and stale demo files

PROJECT_DIR := $(shell pwd)
BIN_DIR     := $(PROJECT_DIR)/bin
SERVER_BIN  := $(BIN_DIR)/server
PORT_FILE   := $(PROJECT_DIR)/.demo-port
SERVER_LOG  := $(PROJECT_DIR)/.demo-server.log

.PHONY: demo test verify build clean help

help:
	@echo "Targets: demo test verify build clean"

build: $(SERVER_BIN)

$(SERVER_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(SERVER_BIN) ./cmd/server

test:
	go test ./...

verify:
	kiff verify ./domain

demo: build
	@SERVER_BIN=$(SERVER_BIN) \
	  PORT_FILE=$(PORT_FILE) \
	  SERVER_LOG=$(SERVER_LOG) \
	  bash $(PROJECT_DIR)/scripts/demo.sh

clean:
	rm -rf $(BIN_DIR) $(PORT_FILE) $(SERVER_LOG) $(PROJECT_DIR)/data
