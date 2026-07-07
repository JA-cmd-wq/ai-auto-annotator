# 前端：Node 构建静态文件，nginx 托管并把 /api 反代到后端。
# 学生浏览器只访问一个地址，天然同源，不存在跨域问题。
FROM node:18 AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --registry=https://registry.npmmirror.com
COPY . .
RUN node node_modules/vue-tsc/bin/vue-tsc.js --noEmit && node node_modules/vite/bin/vite.js build

FROM nginx:1.27-alpine
COPY --from=build /src/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
