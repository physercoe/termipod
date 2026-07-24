# The `bump` recipe uses bash-only `10#` base-10 arithmetic (so a leading-zero
# CalVer component like HHMM "08" is never read as octal); pin bash so it works
# where /bin/sh is dash.
SHELL := /bin/bash

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
# line (YYYY.MMDD.HHMM-alpha+N) and hub/internal/buildinfo/buildinfo.go's
# Version constant in one shot. Usage:
#   make bump VERSION=2026.722.219-alpha
# Versions are date-based CalVer YYYY.MMDD.HHMM (UTC build time). The
# build-number suffix (+N) is minutes-since-2020-01-01-UTC — a monotonic
# int32 Android versionCode that (unlike MAJOR*10000+MINOR*100+PATCH) does
# not regress at the day boundary where HHMM wraps 2359→0. CI recomputes
# the same value from the tag; keep the two formulas in sync.
bump:
ifndef VERSION
	$(error VERSION is required, e.g. make bump VERSION=1.0.262-alpha)
endif
	@core=$$(echo "$(VERSION)" | sed 's/-.*//'); \
	cvy=$$(echo $$core | cut -d. -f1); \
	cvmmdd=$$(echo $$core | cut -d. -f2); \
	cvhhmm=$$(echo $$core | cut -d. -f3); \
	cvmm=$$((10#$$cvmmdd/100)); cvdd=$$((10#$$cvmmdd%100)); \
	cvhh=$$((10#$$cvhhmm/100)); cvmi=$$((10#$$cvhhmm%100)); \
	cva=$$(( (14-cvmm)/12 )); cvyy=$$(( cvy+4800-cva )); cvm=$$(( cvmm+12*cva-3 )); \
	cvjdn=$$(( cvdd + (153*cvm+2)/5 + 365*cvyy + cvyy/4 - cvyy/100 + cvyy/400 - 32045 )); \
	build=$$(( (cvjdn-2458850)*1440 + cvhh*60 + cvmi )); \
	sed -i.bak "s/^version: .*/version: $(VERSION)+$$build/" pubspec.yaml && rm pubspec.yaml.bak; \
	sed -i.bak "s/^const Version = .*/const Version = \"$(VERSION)\"/" hub/internal/buildinfo/buildinfo.go && rm hub/internal/buildinfo/buildinfo.go.bak; \
	echo "bumped to $(VERSION) (build $$build)"; \
	grep '^version:' pubspec.yaml; \
	grep '^const Version' hub/internal/buildinfo/buildinfo.go
