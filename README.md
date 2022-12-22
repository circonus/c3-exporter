# C3 Exporter

The c3 exporter acts as a proxy between internal data collectors and data ingestion into the c3 infrastructure. It offers a lower number of egress points, payload compression to reduce egress bandwidth costs, and enhanced security.

## Installation

Download appropriate package from [release page](https://github.com/circonus/c3-exporter/releases) or use [Docker container](https://hub.docker.com/r/circonus/c3-exporter).

## Configuration

File, see `etc/example-c3-exporter.yaml`

Environment variables:

| env var | yaml key | default | required |
|---------|----------|---------|----------|
|`C3E_SVR_ADDRESS`|`server.listen_address`|":9200"|no|
|`C3E_SVR_CERT_FILE`|`server.cert_file`|""|no|
|`C3E_SVR_KEY_FILE`|`server.key_file`|""|no|
|`C3E_SVR_READ_TIMEOUT`|`server.read_timeout`|"60s"|no|
|`C3E_SVR_WRITE_TIMEOUT`|`server.write_timeout`|"60s"|no|
|`C3E_SVR_IDLE_TIMEOUT`|`server.idle_timeout`|"30s"|no|
|`C3E_SVR_READ_HEADER_TIMEOUT`|`server.read_header_timeout`|"5s"|no|
|`C3E_SVR_HANDLER_TIMEOUT`|`server.handler_timeout`|"30s"|no|
|`C3E_DEST_HOST`|`destination.host`|""|YES|
|`C3E_DEST_PORT`|`destination.port`|""|no|
|`C3E_DEST_CA_FILE`|`destination.ca_file`|""|no|
|`C3E_DEST_ENABLE_TLS`|`destination.enable_tls`|"false"|no|
|`C3E_DEST_TLS_SKIP_VERIFY`|`destination.tls_skip_verify`|"false"|no|
|`C3E_CIRC_CHECK_TARGET`|`circonus.check_target`|hostname|no|
|`C3E_CIRC_API_KEY`|`circonus.api_key`|""|YES|
|`C3E_CIRC_API_URL`|`circonus.api_url`|"https://api.circonus.com/"|no|
|`C3E_CIRC_FLUSH_INTERVAL`|`circonus.flush_interval`|"60s"|no|
|`C3E_DEBUG`|`debug`|"false"|no|
