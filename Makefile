BINARY     := claude-code-approvals
CMD        := ./cmd/$(BINARY)
LAUNCHD_ID := com.vokomarov.cc-approvals
PLIST_SRC  := launchd/$(LAUNCHD_ID).plist
PLIST_DST  := $(HOME)/Library/LaunchAgents/$(LAUNCHD_ID).plist

.PHONY: build install reinstall test test-race lint vet \
        service-install service-reload service-stop service-start \
        health on off logs logs-err

## Development

build:
	go build $(CMD)

install:
	go install $(CMD)

# Rebuild, reinstall binary, update plist, restart daemon — the main "I made changes" target
reinstall: install
	cp $(PLIST_SRC) $(PLIST_DST)
	launchctl unload $(PLIST_DST)
	launchctl load $(PLIST_DST)
	@sleep 1 && $(MAKE) health

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run

vet:
	go vet ./...

## Service management

service-install:
	mkdir -p logs
	cp $(PLIST_SRC) $(PLIST_DST)
	launchctl load $(PLIST_DST)

service-reload:
	cp $(PLIST_SRC) $(PLIST_DST)
	launchctl kickstart -k gui/$$(id -u)/$(LAUNCHD_ID)

service-stop:
	launchctl stop $(LAUNCHD_ID)

service-start:
	launchctl start $(LAUNCHD_ID)

## Runtime

health:
	@curl -sf http://localhost:9753/health && echo || echo "daemon not responding"

on:
	$(BINARY) on

off:
	$(BINARY) off

logs:
	tail -f logs/cc-approvals.log

logs-err:
	tail -f logs/cc-approvals-error.log
