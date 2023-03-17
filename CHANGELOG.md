**unreleased**

# v0.0.13

* feat: increase retry window min:2 max:10
* fix: clarify error message
* feat: update logging messages add req_id
* fix(goreleaser): deprecated syntax

# v0.0.12

* feat: add `/_index_template/` for elastiflow

# v0.0.11

* feat: add `/_component_template/` for elastiflow

# v0.0.10

* feat: expand handling of / to support cua elastic search plugin

# v0.0.9

* fix: do not auth / and /health

# v0.0.8

* fix: remove default configs from docker images so env vars will be used

# v0.0.7

* feat: add `/health` end point

# v0.0.6

* fix: add `check_target` to example config
* fix: `skip_verify` -> `tls_skip_verify` -- clarify option name
* fix: correct build tags
* feat: add env vars for config when in container
* feat: provide more information when `-version` requested e.g. `c3-exporter v0.0.6-devel (branch:main commit:5d85d3c build_date:2022-12-22T16:31:33Z build_tag:v0.0.5)`
* chore: change snapshot name template
* chore: ensure all defaults reflected in example config
* doc: add brief install pointers and configuration details

# v0.0.5

* fix: allow TLS12 to be used AWS doesn't support TLS13 for LBs
* feat: add per-request id
* feat: update messages to include request_id

# v0.0.4

* feat: add support for data-prepper OTEL config [C3-426]
* build: update go-apiclient v0.7.18 -> v0.7.21
* build: update go-trapmetrics v0.0.9 -> v0.0.10

# v0.0.3

* feat: add ingestion tracking histograms

# v0.0.2

* feat: add ingestion tracking metrics

# v0.0.1

* initial
