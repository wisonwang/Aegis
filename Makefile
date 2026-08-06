APP := aegis
CONFIG ?= conf/config.demo.json
VERSION ?= dev

.PHONY: help build run dev test test-py vet security-scan ci-smoke release-artifacts release-sbom release-scan release-sign release-prepare clean skill-pack skill-install-global skill-evals-check mcp-e2e mcp-e2e-admin

help:
	@echo "Available targets:"
	@echo "  make build   - build the local binary"
	@echo "  make run     - build and run with $(CONFIG)"
	@echo "  make dev     - go run with debug-friendly local logging"
	@echo "  make test    - run Go tests"
	@echo "  make test-py - run the pytest end-to-end suite (test/)"
	@echo "  make vet     - run go vet"
	@echo "  make security-scan - run govulncheck (blocking) and gosec (advisory)"
	@echo "  make ci-smoke - run the local CI-equivalent smoke suite"
	@echo "  make release-artifacts VERSION=vX.Y.Z - build release archives + checksums"
	@echo "  make release-sbom - generate an SPDX SBOM into dist/"
	@echo "  make release-scan - scan for vulnerabilities (Trivy/Grype)"
	@echo "  make release-sign VERSION=vX.Y.Z - sign release artifacts (cosign)"
	@echo "  make release-prepare VERSION=vX.Y.Z - ci-smoke + artifacts + SBOM + scan"
	@echo "  make skill-pack - package aegis-mcp as an importable TRAE skill zip"
	@echo "  make skill-install-global - install aegis-mcp into ~/.trae-cn/skills"
	@echo "  make skill-evals-check - validate structured eval cases"
	@echo "  make mcp-e2e - run the scenario-driven MCP end-to-end test"
	@echo "  make mcp-e2e-admin - run the MCP end-to-end test as admin"
	@echo "  make clean   - remove local build artifacts"

build:
	go build -o $(APP) ./cmd/aegis

run: build
	./$(APP) -config $(CONFIG)

dev:
	AEGIS_LOG_FORMAT=text AEGIS_LOG_LEVEL=debug go run ./cmd/aegis -config $(CONFIG)

test:
	go test ./...

test-py:
	cd test && python3 -m pytest -q

vet:
	go vet ./...

security-scan:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	@echo "Running advisory gosec scan (non-blocking)..."
	- go run github.com/securego/gosec/v2/cmd/gosec@latest ./...

ci-smoke: build vet test
	python3 -m py_compile examples/dataapi/agent.py examples/mcp/client.py scripts/mcp_e2e_scenario.py
	./scripts/mcp_e2e_scenario.py
	cd test && python3 -m pytest -q

release-artifacts:
	./scripts/build_release_artifacts.sh "$(VERSION)"
	./scripts/generate_checksums.sh

release-sbom:
	./scripts/generate_sbom.sh

release-scan:
	./scripts/scan_vulnerabilities.sh

release-sign:
	./scripts/sign_artifacts.sh "$(VERSION)"

release-prepare: ci-smoke
	./scripts/build_release_artifacts.sh "$(VERSION)"
	./scripts/generate_sbom.sh
	./scripts/scan_vulnerabilities.sh
	./scripts/generate_checksums.sh

skill-pack:
	./scripts/package_skill.sh aegis-mcp dist/aegis-mcp-skill.zip

skill-install-global:
	./scripts/install_skill_global.sh aegis-mcp

skill-evals-check:
	./scripts/validate_skill_eval_cases.py aegis-mcp/evals/cases.json

mcp-e2e:
	./scripts/mcp_e2e_scenario.py

mcp-e2e-admin:
	./scripts/mcp_e2e_scenario.py --mode admin

clean:
	rm -f ./$(APP)
