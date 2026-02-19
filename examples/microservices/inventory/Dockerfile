# Single-stage — no build step, just install + run.
FROM node:20-alpine

WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --production 2>/dev/null || npm install --production
COPY config.js server.js ./

# Port is hardcoded in config.js — blame the original dev.
EXPOSE 3000
CMD ["node", "server.js"]
