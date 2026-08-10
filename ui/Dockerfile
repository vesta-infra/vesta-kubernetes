FROM --platform=$BUILDPLATFORM node:20-alpine AS builder
ARG APP_VERSION=dev
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
ENV VITE_APP_VERSION=$APP_VERSION
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
