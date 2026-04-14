# Git Submodule 集成指南

本文档说明如何将 Sub2API 作为 Git Submodule 集成到母项目中。

## 在母项目中添加 Sub2API

```bash
# 在母项目根目录执行
git submodule add <your-sub2api-repo-url> sub2api
git submodule update --init --recursive
```

## 部署配置

### 方式 1: 使用本地构建（推荐用于开发）

```bash
cd sub2api/deploy
cp .env.example .env
# 编辑 .env 配置文件
nano .env

# 使用本地构建启动
docker-compose -f docker-compose.local.yml up -d
```

### 方式 2: 使用官方镜像

```bash
cd sub2api/deploy
cp .env.example .env
# 编辑 .env 配置文件

# 使用官方镜像启动
docker-compose up -d
```

### 方式 3: 一键部署脚本

```bash
cd sub2api/deploy
./docker-deploy.sh
```

## 环境变量配置

**重要**: 部署前必须配置以下环境变量：

### 必需配置

1. **数据库配置**
   - `POSTGRES_USER`: PostgreSQL 用户名
   - `POSTGRES_PASSWORD`: PostgreSQL 密码（建议使用强密码）
   - `POSTGRES_DB`: 数据库名称

2. **管理员账号**
   - `ADMIN_EMAIL`: 管理员邮箱
   - `ADMIN_PASSWORD`: 管理员密码（建议使用强密码）

3. **JWT 密钥**
   - `JWT_SECRET`: JWT 签名密钥（必须设置固定值，否则重启后所有用户会被登出）
   ```bash
   # 生成方式
   openssl rand -hex 32
   ```

4. **TOTP 加密密钥**
   - `TOTP_ENCRYPTION_KEY`: 双因素认证加密密钥（必须设置固定值）
   ```bash
   # 生成方式
   openssl rand -hex 32
   ```

### 可选配置

- `SERVER_PORT`: 服务端口（默认 9999）
- `REDIS_PASSWORD`: Redis 密码
- `LOG_LEVEL`: 日志级别（debug/info/warn/error）
- 更多配置请参考 `deploy/.env.example`

## 母项目 Docker Compose 集成示例

如果母项目也使用 Docker Compose，可以这样集成：

```yaml
# 母项目的 docker-compose.yml
version: '3.8'

services:
  # 其他服务...
  
  # 引用 Sub2API 服务
  sub2api:
    extends:
      file: ./sub2api/deploy/docker-compose.yml
      service: sub2api
    networks:
      - your-network
    # 可以覆盖端口等配置
    ports:
      - "9999:8080"

networks:
  your-network:
    driver: bridge
```

## 更新 Submodule

```bash
# 在母项目根目录执行
cd sub2api
git pull origin main
cd ..
git add sub2api
git commit -m "chore: update sub2api submodule"
```

## 克隆包含 Submodule 的母项目

```bash
# 方式 1: 克隆时同时初始化 submodule
git clone --recurse-submodules <mother-project-url>

# 方式 2: 克隆后初始化 submodule
git clone <mother-project-url>
cd <mother-project>
git submodule update --init --recursive
```

## 注意事项

1. **环境变量隔离**: 每个 submodule 的 `.env` 文件已被 `.gitignore` 忽略，需要在部署时单独配置
2. **数据持久化**: 默认使用 Docker volumes，数据存储在 `deploy/postgres_data` 和 `deploy/redis_data`
3. **端口冲突**: 确保母项目中没有其他服务占用相同端口
4. **网络配置**: 如需与母项目其他服务通信，需要配置共享网络
5. **密钥管理**: `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY` 必须设置固定值并妥善保管

## 故障排查

### 查看日志
```bash
cd sub2api/deploy
docker-compose logs -f sub2api
```

### 重启服务
```bash
cd sub2api/deploy
docker-compose restart sub2api
```

### 完全重建
```bash
cd sub2api/deploy
docker-compose down
docker-compose up -d --build
```

## 更多信息

- 完整部署文档: `deploy/DOCKER.md`
- 配置示例: `deploy/.env.example`
- 项目主页: https://github.com/Wei-Shaw/sub2api
