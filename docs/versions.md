# Pinned versions

Every tool, image and action is pinned. Images use `tag@sha256:digest`, actions
use a full commit SHA with the version in a comment, Go modules are locked by
`go.sum`. Dependabot proposes updates weekly (`.github/dependabot.yml`); a new
version is adopted only after it has been released for at least two weeks.

Where each pin lives: runtime images in `compose.yaml`, build images in
`Dockerfile`, CI tool images and actions in `.github/workflows/ci.yml`, Go
modules in `go.mod`. This file is the index; update it in the same change.

## Runtime stack (`compose.yaml`)

| Component | Version | Digest |
|---|---|---|
| OpenTelemetry Collector (contrib) | 0.162.0 | `sha256:39923a8e431bd1f57be82411999d389fcfe40857492e4365456d97a4c1f74be6` |
| Prometheus | v3.15.0 | `sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e` |
| Alertmanager | v0.34.1 | `sha256:e9733bafb1bdef9b00e25a21f8f99dc26a22224bf16641ad754d1649f4c3357a` |
| Tempo | 2.10.8 | `sha256:f0561deb1c68ec44d6e6e7e4487f30106c4e5e768642077695b37958b105812a` |
| Loki | 3.7.8 | `sha256:1107dd5274e0ada47e42472b7a7e71f3b2a2fe878878108f3e2f9e51528f0193` |
| Grafana | 13.2.3 | `sha256:b28bae15e219c998fb0e0424ed724930cc61b1f61fb404d47c862f9a23f9e572` |
| k6 | 2.3.0 | `sha256:2dd4756ec6e14e62778916e1eb850108e4b499304c02df56760fa0bdb0fba0b8` |

## Service images (`Dockerfile`)

| Stage | Image | Digest |
|---|---|---|
| build | golang 1.27.3-bookworm | `sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5` |
| runtime | gcr.io/distroless/static-debian13:nonroot | `sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99` |

## Go (`go.mod`)

| Module | Version |
|---|---|
| Go toolchain | 1.27.3 (`go 1.27`) |
| go.opentelemetry.io/otel, sdk, sdk/metric, sdk/log, OTLP trace/metric exporters | v1.47.0 |
| go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp | v0.23.0 |
| go.opentelemetry.io/contrib/bridges/otelslog | v0.21.0 |
| golang.org/x/net | v0.61.0 |

## CI (`.github/workflows/ci.yml`)

| Tool | Version | Pin |
|---|---|---|
| actions/checkout | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| actions/setup-go | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| golangci/golangci-lint-action | v9.3.0 (golangci-lint v2.15.0) | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` |
| actions/upload-artifact | v7.0.2 | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |
| github/codeql-action/upload-sarif | v4.39.1 | `9b3254085494b55a0f7c4c2b3d2d52cc712ce370` |
| govulncheck | v1.9.0 | module version |
| Gitleaks | v8.30.1 | `sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f` |
| hadolint | v2.15.1 | `sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d` |
| actionlint | 1.7.12 | `sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667` |
| promtool / amtool / otelcol validate | same images as the runtime stack | |
