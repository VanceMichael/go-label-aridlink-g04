# BENZHI_README

这是一个 Go 全栈项目，主要用途为：AridLink is an operations backend for joint drought, desertification and land-degradation programs， It coordinates partner organizations, restoration sites, field monitoring, intervention work, evidence review, grant milestones, warnings, technology transfer and training， PostgreSQL transactions, optimistic versions, durable leases, an outbox and audit records preserve the cross-module invariants。

## 项目说明

- 项目：VanceMichael/go-label-aridlink-g04
- 项目用途：AridLink is an operations backend for joint drought, desertification and land-degradation programs. It coordinates partner organizations, restoration sites, field monitoring, intervention work, evidence review, grant milestones, warnings, technology transfer and training. PostgreSQL transactions, optimistic versions, durable leases, an outbox and audit records preserve the cross-module invariants.
- Go 工具链：`golang:1.26.0`
- 前端工具链：Node.js 20

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
./build_benzhi_docker.sh benzhi-task-208-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-208-arm64 linux/arm64
docker run -it benzhi-task-208-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-208-arm64:latest
```

## 题目验证命令

1. 预期退出码 1：`go test ./integration -run '^TestG04Task07CampaignSubmitAtomicity$' -count=1`
