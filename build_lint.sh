#!/usr/bin/env bash

echo
echo "Running Lint (Linux specific)"
GOOS=linux golangci-lint run -c ./.golangci.yml || exit 1

