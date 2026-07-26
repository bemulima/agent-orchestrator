FROM node:24-alpine AS dependencies
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:24-alpine AS builder
WORKDIR /app
ARG ORCHESTRATOR_API_URL=http://orchestrator:8080
ARG NEXT_PUBLIC_ORCHESTRATOR_EVENTS_URL=http://127.0.0.1:8080/api/v1/events
ENV ORCHESTRATOR_API_URL=${ORCHESTRATOR_API_URL}
ENV NEXT_PUBLIC_ORCHESTRATOR_EVENTS_URL=${NEXT_PUBLIC_ORCHESTRATOR_EVENTS_URL}
COPY --from=dependencies /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM node:24-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3000
RUN addgroup -S orchestrator && adduser -S orchestrator -G orchestrator
COPY --from=builder --chown=orchestrator:orchestrator /app/.next/standalone ./
COPY --from=builder --chown=orchestrator:orchestrator /app/.next/static ./.next/static
USER orchestrator
EXPOSE 3000
CMD ["node", "server.js"]
