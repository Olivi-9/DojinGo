APP     := ehbot
CMD     := ./cmd/ehbot
OUTDIR  := build

TARGETS := \
	linux/amd64 \
	linux/arm64 \
	linux/arm \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

build:
	@mkdir -p $(OUTDIR)
	@$(foreach target,$(TARGETS), \
		$(eval OS   := $(word 1,$(subst /, ,$(target)))) \
		$(eval ARCH := $(word 2,$(subst /, ,$(target)))) \
		$(eval EXT  := $(if $(filter windows,$(OS)),.exe,)) \
		echo "Building $(OS)/$(ARCH)..." ; \
		CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build \
			-o $(OUTDIR)/$(APP)-$(OS)-$(ARCH)$(EXT) $(CMD) ; \
	)

clean:
	rm -rf $(OUTDIR)

.PHONY: build clean