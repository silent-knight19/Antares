# U2 demo image: builds Antares, the observatory bridge, and the SPA, then
# runs either binary. The production packaging lands at P10.1; this profile
# exists so `docker compose -f deploy/demo/docker-compose.demo.yml up` is a
# one-command dev console.
# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/antares ./cmd/antares \
 && CGO_ENABLED=0 go build -trimpath -o /out/observatory ./cmd/observatory

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM alpine:3.20
RUN adduser -D -u 10001 antares
COPY --from=build /out/antares /usr/local/bin/antares
COPY --from=build /out/observatory /usr/local/bin/observatory
COPY --from=web /web/dist /web/dist
USER 10001
EXPOSE 6380 6390
ENTRYPOINT ["/usr/local/bin/observatory"]
