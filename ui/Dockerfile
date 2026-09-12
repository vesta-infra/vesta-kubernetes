FROM --platform=$BUILDPLATFORM node:20-alpine AS builder
ARG APP_VERSION=dev
WORKDIR /app
COPY package.json package-lock.json ./
# npm ci, not npm install: it installs exactly what the lockfile pins and fails loudly if
# the lockfile and package.json disagree. `npm install` silently re-resolves, which is how
# a lockfile pinning @vitejs/plugin-react 4 against vite 8 -- a combination npm refuses to
# resolve from scratch -- survived long enough to break the image build while every local
# build kept working off an already-populated node_modules.
#
# The lockfile is no longer optional (no `*`): building without one would re-resolve and
# put us back where we started.
RUN npm ci
COPY . .
ENV VITE_APP_VERSION=$APP_VERSION
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
