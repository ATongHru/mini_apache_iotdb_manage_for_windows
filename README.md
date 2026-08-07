# Mini Apache IoTDB Manager for Windows

一个使用 Go 开发、可在 Windows 上直接运行的轻量级 Apache IoTDB 本地管理工具。程序启动后提供本地 Web 管理界面，用于连接 IoTDB、浏览数据库和设备、查看时序数据、执行 SQL，以及维护常用连接和查询。

## 功能

- 使用 Apache IoTDB 官方 Go Session 客户端连接 IoTDB RPC 服务；默认连接地址为 `127.0.0.1:6667`。
- 浏览 `SHOW DATABASES` 和 `SHOW DEVICES` 的结果；点击设备可打开数据浏览页并查询该设备的数据。
- SQL 控制台支持多个标签页，并可执行 `SELECT`、`SHOW`、`DESCRIBE`、`EXPLAIN`、`WITH` 等查询语句；查询结果以表格展示。
- 查询结果支持时间条件筛选、分页浏览、调整每页行数，以及复制为 TSV。
- 可直接执行 `INSERT`、`CREATE TIMESERIES`、`DELETE TIMESERIES`、`DELETE FROM`、`CREATE/DROP DATABASE`、`SET TTL` 等写入、删除和模式管理 SQL。
- 支持保存常用连接和 SQL 查询；连接信息仅保存在运行该程序的 Windows 用户配置目录中。
- 界面可查看本地管理服务的 CPU 与内存占用，并提供深色模式。
- 默认仅监听 `127.0.0.1:52014`，不会向局域网暴露管理界面；启动后会在 Chromium 系浏览器中打开独立窗口。

## 快速开始

1. 从 [Releases](https://github.com/ATongHru/mini_apache_iotdb_manage_for_windows/releases) 下载 `MiniApacheIoTDBManager.exe`。
2. 双击运行 `MiniApacheIoTDBManager.exe`。控制台窗口会保留，关闭该窗口即可停止本地服务。
3. 浏览器会自动打开 `http://127.0.0.1:52014`。
4. 在“连接 IoTDB”窗口填写服务器地址、端口、用户名和密码：
   - 主机默认：`127.0.0.1`
   - 端口默认：`6667`
   - 用户名默认：`root`
   - 密码默认：`root`
5. 连接成功后，可从左侧浏览数据库和设备，或使用 SQL 控制台执行操作。

如果浏览器没有自动打开，请访问控制台显示的本地地址。

## 系统要求

### 运行发布版本

- Windows 10 / 11（64 位）
- 可访问目标 Apache IoTDB RPC 服务的网络权限
- 具备所执行操作所需的 IoTDB 最小权限账号

### 从源码构建

- Windows（64 位）
- Go 1.24 或更高版本

项目随附 `github.com/apache/iotdb-client-go` v1.3.7 和 `github.com/apache/thrift` v0.15.0 的源码副本，位于 `third_party/`。依赖未变化时可在离线环境构建。

## 启动参数

| 作用 | 命令行参数 | 默认值 |
| --- | --- | --- |
| Web 监听地址 | `-addr` | `127.0.0.1:52014` |
| 自动打开浏览器 | `-open-browser` | `true` |

示例：改用另一个本地端口启动：

```bat
MiniApacheIoTDBManager.exe -addr 127.0.0.1:18080
```

示例：仅启动服务，不自动打开浏览器：

```bat
MiniApacheIoTDBManager.exe -open-browser=false
```

> 如主动将 `-addr` 设置为 `0.0.0.0:端口` 或其他非回环地址，请先配置防火墙、访问控制和额外认证措施。该工具不适合直接暴露到公网。

## 构建

在 Windows 中双击或运行：

```bat
normal_build.bat
```

脚本会强制使用项目内的本地依赖，不会访问网络。构建成功后生成 `MiniApacheIoTDBManager.exe`。

也可手动执行：

```bat
set GOPROXY=off
set GOSUMDB=off
go build -mod=mod -trimpath -ldflags="-s -w" -o MiniApacheIoTDBManager.exe .
```

## 使用说明与限制

- **数据修改：** 写入、删除、TTL 设置和模式变更会立即提交给目标 IoTDB 实例。请先备份，并在测试环境验证 SQL。
- **查询：** 服务端单次最多返回 500 行；可在请求中选择最多 1000 行。界面会继续对结果进行分页显示。
- **设备浏览：** 对象树为 `SHOW DATABASES` 和 `SHOW DEVICES` 的当前结果，单次各最多加载 300 行；大型实例请优先使用 SQL 控制台进行定向查询。
- **SQL 执行：** 查询超时为 60 秒。复杂查询、全库扫描和写入操作的实际资源消耗由 IoTDB 服务端决定。
- **保存的连接：** 配置文件位于 `%USERPROFILE%\.mini-manage\iotdb-manage.json`，其中包含保存的连接密码明文。请仅在受信任的个人 Windows 帐户中使用，避免在共享电脑上保存连接信息。
- **网络：** 本地管理界面没有额外的登录层，安全边界依赖默认的回环监听和 Windows 用户帐户权限。

## 安全建议

1. 为此工具使用 IoTDB 专用、最小权限的账号，不要使用生产环境超级管理员账号处理日常查询。
2. 不要将密码、令牌、导出数据或本地配置文件提交到 Git。
3. 使用完成后关闭程序控制台窗口，以停止本地 Web 服务和数据库会话。
4. 如曾将密码提交到远程仓库或分享给无关人员，请立即在 IoTDB 中轮换该密码。

## 项目结构

```text
.
├── main.go                   # Go 后端、HTTP API 与嵌入式静态页面
├── web/index.html            # 本地 Web 管理界面
├── normal_build.bat          # 可提交的离线 Windows 构建脚本
├── third_party/iotdb-client-go # Apache IoTDB Go 客户端源码
├── third_party/thrift        # Apache Thrift 源码
├── LICENSE                   # 本项目 MIT 许可证
├── NOTICE                    # 第三方许可证与声明
└── go.mod                    # Go 模块与本地依赖替换配置
```

## 开发与验证

修改 Go 源码后，运行：

```bat
set GOPROXY=off
set GOSUMDB=off
go build -mod=mod ./...
```

随后启动新生成的 `MiniApacheIoTDBManager.exe`，使用测试 IoTDB 实例验证连接、浏览、查询和写入操作。

## 许可证

本项目代码采用 [MIT License](LICENSE)，版权归 `ATongHru` 所有。

本项目包含以下 Apache License 2.0 第三方依赖源码副本。再发布源码或编译后的可执行文件时，请保留其许可证文本、版权声明及通知文件：

- `github.com/apache/iotdb-client-go` v1.3.7；原始许可证和通知文件位于 [`third_party/iotdb-client-go/`](third_party/iotdb-client-go/)。
- `github.com/apache/thrift` v0.15.0；原始许可证和通知文件位于 [`third_party/thrift/`](third_party/thrift/)。

完整的第三方声明见 [NOTICE](NOTICE)。
