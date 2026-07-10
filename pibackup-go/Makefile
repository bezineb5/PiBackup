# Makefile for PiBackup Go

BINARY_NAME := pibackup-go

.PHONY: all build clean install test help

all: build ## Build the project (default)

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary for Raspberry Pi (ARM6)
	@echo "Building PiBackup Go..."
	GOOS=linux GOARCH=arm GOARM=6 go build -o $(BINARY_NAME) .
	@if [ $$? -eq 0 ]; then \
		echo "Build successful!"; \
		echo "Binary: $(BINARY_NAME)"; \
		echo ""; \
		echo "To install:"; \
		echo "1. Copy pibackup-go to your Raspberry Pi"; \
		echo "2. Make it executable: chmod +x pibackup-go"; \
		echo "3. Install service: sudo cp pibackup-go.service /etc/systemd/system/"; \
		echo "4. Enable service: sudo systemctl enable pibackup-go.service"; \
		echo "5. Start service: sudo systemctl start pibackup-go.service"; \
	else \
		echo "Build failed!"; \
		exit 1; \
	fi

test: ## Run all Go tests
	@echo "Running tests..."
	go test -v ./...

clean: ## Remove the built binary
	rm -f $(BINARY_NAME)
	@echo "Cleaned up binary: $(BINARY_NAME)"

install: build ## Build and make binary executable
	@echo "Installing..."
	chmod +x $(BINARY_NAME)
	@echo "Remember to copy the binary to your Raspberry Pi and set up the service manually."
