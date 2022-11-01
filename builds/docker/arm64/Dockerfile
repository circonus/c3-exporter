FROM arm64v8/alpine:latest
RUN echo 'hosts: files dns' >> /etc/nsswitch.conf
RUN apk add --no-cache ca-certificates && \
    update-ca-certificates

COPY c3-exporterd /

# NOTE: mount custom config as /c3-exporter.yaml
COPY etc/example-c3-exporter.yaml /c3-exporter.yaml

# NOTE: the default port, use -p to map alternative configured
EXPOSE 19200/tcp

ENTRYPOINT ["/c3-exporterd"]
