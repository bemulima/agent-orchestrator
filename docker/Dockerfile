FROM golang:1.23-alpine AS builder

WORKDIR /app
ARG VERSION=dev
ARG COMMIT=none
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/course-dev-orchestrator ./cmd/course-dev-orchestrator

FROM node:22-alpine AS runner-builder

WORKDIR /runner
COPY runner/package.json runner/package-lock.json ./
RUN npm ci
COPY runner/tsconfig.json ./
COPY runner/src ./src
RUN npm run build \
    && npm prune --omit=dev

FROM alpine:3.21

RUN apk add --no-cache bubblewrap ca-certificates git nodejs npm openssh-client \
    && addgroup -S orchestrator \
    && adduser -S -G orchestrator orchestrator \
    && mkdir -p /data/repositories /data/worktrees /data/codex /projects \
    && chown -R orchestrator:orchestrator /data /projects
WORKDIR /app
COPY --from=builder /out/course-dev-orchestrator /app/course-dev-orchestrator
COPY --from=runner-builder --chown=orchestrator:orchestrator /runner /app/runner
USER orchestrator
EXPOSE 8080
ENTRYPOINT ["/app/course-dev-orchestrator"]
CMD ["serve"]
