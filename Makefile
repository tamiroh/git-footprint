.PHONY: format
format:
	gofmt -w .

.PHONY: check
check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "not gofmt-formatted (run 'make format'):"; \
		echo "$$files"; \
		exit 1; \
	fi
