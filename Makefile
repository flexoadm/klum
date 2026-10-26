.DEFAULT_GOAL := build

TARGETS := build ci default package test validate validate-ci test-integration

$(TARGETS):
	./scripts/$@

generate:
	go generate .

.PHONY: $(TARGETS) generate
