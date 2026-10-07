# Build context: repository root (needs server/ and web/).
FROM golang:1.26-alpine AS build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/quack ./cmd/quack

FROM node:26-alpine AS web
WORKDIR /src/web
COPY web/ ./
RUN if [ -f package.json ]; then corepack enable && pnpm install --frozen-lockfile && pnpm build; else mkdir -p dist; fi

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/quack /quack
COPY --from=web /src/web/dist /web
ENV QUACK_WEB_DIR=/web
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/quack"]
CMD ["serve"]
