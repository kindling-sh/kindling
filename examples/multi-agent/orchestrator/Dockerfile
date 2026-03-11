FROM node:20-alpine
WORKDIR /app
ENV npm_config_cache=/tmp/.npm
COPY package*.json ./
RUN npm install
COPY tsconfig.json ./
COPY src ./src
RUN npx tsc
EXPOSE 3000
CMD ["node", "dist/index.js"]
