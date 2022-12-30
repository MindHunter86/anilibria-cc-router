# -*- coding: utf-8 -*-
# vim: ft=Dockerfile

FROM golang:1.19.1-alpine AS build
LABEL maintainer="mindhunter86 <mindhunter86@vkom.cc>"

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w -X 'main.version=docker_release'" -o /anilibria-cc-router

RUN apk add --no-cache upx \
  && upx -9 -k /anilibria-cc-router \
  && apk del upx


FROM alpine
LABEL maintainer="mindhunter86 <mindhunter86@vkom.cc>"

WORKDIR /

COPY --from=build /anilibria-cc-router /usr/local/bin/anilibria-cc-router

USER nobody
ENTRYPOINT ["/usr/local/bin/anilibria-cc-router"]
