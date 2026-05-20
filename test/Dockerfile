FROM golang:1.25-bookworm

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        librados-dev \
        librbd-dev \
        libcephfs-dev && \
    rm -rf /var/lib/apt/lists/*

ENV CGO_ENABLED=1
WORKDIR /src
