FROM nvidia/cuda:11.8.0-devel-ubuntu22.04

ARG TARGETARCH
ARG VERSION=0.0.0

WORKDIR /go/src/github.com/jmorganca/ollama
RUN apt-get update && apt-get install -y git build-essential cmake
ADD https://dl.google.com/go/go1.21.1.linux-$TARGETARCH.tar.gz /tmp/go1.21.1.tar.gz
RUN mkdir -p /usr/local && tar xz -C /usr/local </tmp/go1.21.1.tar.gz

COPY . .
ENV GOARCH=$TARGETARCH
RUN /usr/local/go/bin/go generate ./... \
    && /usr/local/go/bin/go build -ldflags "-linkmode=external -extldflags='-static' -X=github.com/jmorganca/ollama/version.Version=$VERSION -X=github.com/jmorganca/ollama/server.mode=release" .

FROM ubuntu:22.04
ENV OLLAMA_HOST 0.0.0.0

RUN apt-get update && apt-get install -y ca-certificates
RUN groupadd ollama && useradd -m -g ollama ollama

COPY --from=0 /go/src/github.com/jmorganca/ollama/ollama /bin/ollama

USER ollama:ollama
ENTRYPOINT ["/bin/ollama"]
CMD ["serve"]
