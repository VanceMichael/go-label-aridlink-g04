# BENZHI_README

这是一个采用 Go 后端与现代前端构建的全栈项目，用于承载 go-label-aridlink-g04 的业务操作、数据管理与服务交付。

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...
cd '/app/web' && npm install
cd '/app/web' && npm run build

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/api
cd '/app/web' && npm run dev

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
cd '/app/web' && npm test
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-221-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-221-arm64 linux/arm64
docker run -it benzhi-task-221-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-221-arm64:latest
```
