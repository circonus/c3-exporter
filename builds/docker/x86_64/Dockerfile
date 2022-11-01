FROM gcr.io/distroless/static

COPY c3-exporterd /

# NOTE: mount custom config as /c3-exporter.yaml
COPY etc/example-c3-exporter.yaml /c3-exporter.yaml

# NOTE: the default port, use -p to map alternatives configured
EXPOSE 19200/tcp

ENTRYPOINT ["/c3-exporterd"]
