# 以 Go 镜像为基础，再装 Node.js，两套工具链都保留
FROM golang:1.26.0

# 安装 Node.js 20（前端构建需要）
RUN apt-get update && apt-get install -y curl \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Go 依赖
COPY go.mod go.sum ./
RUN go mod download

# 前端依赖（按检测到的实际目录：web/）
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm install

# 复制所有项目文件
COPY . .

# 预编译后端 + 构建前端，确认两边都能通过
RUN go build ./... && cd /app/web && npm run build

CMD ["bash"]
