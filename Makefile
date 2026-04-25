GIT_REF := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)@$(shell git rev-parse --short HEAD 2>/dev/null)

.PHONY: run build-apk analyze test bump

run:
	flutter run --dart-define=GIT_REF=$(GIT_REF)

build-apk:
	flutter build apk --release --dart-define=GIT_REF=$(GIT_REF)

analyze:
	flutter analyze

test:
	flutter test

# Bump mobile + hub to the same version. Updates pubspec.yaml's version:
# line (X.Y.Z-alpha+N) and hub/internal/buildinfo/buildinfo.go's
# Version constant in one shot. Usage:
#   make bump VERSION=1.0.262-alpha
# The build-number suffix (+N) is derived as MAJOR*10000 + MINOR*100 +
# PATCH so it stays unique and monotonic for Android packaging.
bump:
ifndef VERSION
	$(error VERSION is required, e.g. make bump VERSION=1.0.262-alpha)
endif
	@core=$$(echo "$(VERSION)" | sed 's/-.*//'); \
	major=$$(echo $$core | cut -d. -f1); \
	minor=$$(echo $$core | cut -d. -f2); \
	patch=$$(echo $$core | cut -d. -f3); \
	build=$$(( major * 10000 + minor * 100 + patch )); \
	sed -i.bak "s/^version: .*/version: $(VERSION)+$$build/" pubspec.yaml && rm pubspec.yaml.bak; \
	sed -i.bak "s/^const Version = .*/const Version = \"$(VERSION)\"/" hub/internal/buildinfo/buildinfo.go && rm hub/internal/buildinfo/buildinfo.go.bak; \
	echo "bumped to $(VERSION) (build $$build)"; \
	grep '^version:' pubspec.yaml; \
	grep '^const Version' hub/internal/buildinfo/buildinfo.go
