FROM node:22-alpine AS frontend
WORKDIR /src
COPY web/frontend/package*.json web/frontend/
RUN cd web/frontend && npm ci
COPY web/frontend web/frontend
COPY buf.yaml buf.gen.yaml ./
COPY proto proto
RUN cd web/frontend && npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/rementor ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -o /out/rementorctl ./cmd/rementorctl \
 && CGO_ENABLED=0 go build -trimpath -o /out/mock-stack ./examples/mock-stack

FROM alpine:3.22
RUN apk add --no-cache nginx
COPY --from=build /out/rementor /usr/local/bin/rementor
COPY --from=build /out/rementorctl /usr/local/bin/rementorctl
COPY --from=build /out/mock-stack /usr/local/bin/mock-stack
COPY --from=frontend /src/cmd/server/dist /usr/local/share/rementor/dist
COPY examples/docker/nginx.conf /etc/nginx/nginx.conf
COPY examples/docker/entrypoint.sh /usr/local/bin/rementor-demo
RUN adduser -D -u 10001 rementor \
 && mkdir -p /var/lib/rementor /var/cache/rementor /var/config/rementor/nginx /var/log/nginx /tmp/nginx \
 && chown -R rementor:rementor /var/lib/rementor /var/lib/nginx /var/log/nginx /var/cache/rementor /var/config /tmp/nginx
USER rementor
ENV XDG_DATA_HOME=/var/lib \
    XDG_CACHE_HOME=/var/cache \
    XDG_CONFIG_HOME=/var/config \
    REMENTOR_FRONTEND_DIST=/usr/local/share/rementor/dist \
    REMENTOR_NGINX_LISTEN_HOST=0.0.0.0 \
    REMENTOR_NGINX_LISTEN_PORTS=8080
EXPOSE 8080
ENTRYPOINT ["rementor-demo"]
