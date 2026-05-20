# Build the manager binary
FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GIT_COMMIT='not set'
ARG GIT_TAG=development
ENV GIT_COMMIT=$GIT_COMMIT
ENV GIT_TAG=$GIT_TAG
# pacific, quincy, reef	
ENV CEPH_RELEASE="reef" 
ENV CPU_ARCH=$TARGETARCH

RUN echo $TARGETARCH

WORKDIR /build

# install build dependecies
RUN apt-get update && apt-get install -y gcc g++ librbd-dev librados-dev libcephfs-dev linux-headers-generic ceph-common

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY . .

# build app
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GO111MODULE=on \
    go build -ldflags="-X 'main.version=$GIT_TAG' -X 'main.commit=$GIT_COMMIT'" ./cmd/ceph-api

# --== workaround for multi-arch build ==--
# shared lib path contains system arch in its name: 
# - ".../aarch64-linux-gnu/..." for arm64 
# - ".../x64_86-linux-gnu/..." for amd64
# docker builtin $TARGETARCH envar cannot be used because it returns arch in different format
# and it is not possible to have any mapping in Dockfile.
# So here we copy required libs with full paths into "dependencies" dir 
# to COPY these libraries into target image on the next phase.
RUN mkdir dependencies
RUN cp -r --parents /usr/lib/*-linux-gnu/librbd.so* -t dependencies
RUN cp -r --parents /usr/lib/*-linux-gnu/librados.so* -t dependencies
RUN cp -r --parents /usr/lib/*-linux-gnu/ceph/libceph-common.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libfmt.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libboost_thread.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libboost_iostreams.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libblkid.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libudev.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libibverbs.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/librdmacm.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libz.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libbz2.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/liblzma.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libzstd.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libnl-route-3.so* -t dependencies
RUN cp -r --parents /lib/*-linux-gnu/libnl-3.so* -t dependencies


FROM gcr.io/distroless/cc-debian12

# copy shared libraries
COPY --from=builder /build/dependencies/usr/lib/ /usr/lib/
COPY --from=builder /build/dependencies/lib/ /lib/

# copy app bianry
COPY --from=builder /build/ceph-api /bin/ceph-api
WORKDIR /bin

CMD ["ceph-api"]
