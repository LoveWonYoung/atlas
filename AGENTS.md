# AGENTS.md

面向本仓库的 AI / 编码助手说明。人类文档见 `README.md`（内容可能仍写旧名 canbuskit）。

## Project

- **Module path**: `github.com/LoveWonYoung/atlas`（所有 import 必须用此路径，不要用 `canbuskit` / `linbuskit`）
- **Language**: Go（见 `go.mod`）
- **领域**: CAN / CAN FD 与 LIN 的硬件驱动封装、传输层、UDS 客户端

## Architecture

| Package | Role |
|---------|------|
| `driver` | CAN/LIN 硬件驱动；`CANDriver` / `ToomossLIN` 等 |
| `tp_layer` | ISO-15765-2（CAN 侧 ISO-TP） |
| `liniface` | LIN 驱动抽象（`Driver`、`LinEvent`、`Channel`） |
| `tplin` | LIN 传输层（master/slave） |
| `uds_client` | 基于 CAN + ISO-TP 的 UDS 客户端 |
| `uds_client_lin` | 基于 LIN 的 UDS 客户端 |
| `preset` | 组装 driver + tp + UDS 的便捷入口 |

典型链路：

- CAN: `driver` → `tp_layer` → `uds_client`
- LIN: `driver`（实现 `liniface.Driver`）→ `tplin` → `uds_client_lin`

## Build tags（必读）

`driver` 大量使用平台 build tag，改驱动时必须保持对称：

| Tag | 文件示例 |
|-----|----------|
| `windows` | `toomoss.go`, `toomoss_loadlibrary.go`, `toomoss_lin_windows.go`, `pcan.go`, `vector.go`, `tsmaster.go`, `auto_driver.go` |
| `darwin && cgo` | `toomoss_darwin.go`, `toomoss_lin_darwin.go` |

约定：

- 同名 API（如 `NewToomoss`、`NewToomossLIN`、`UsbScan`）跨平台签名保持一致；平台差异放在实现里。
- Windows Toomoss：CAN/LIN 共享 `toomoss_loadlibrary.go` 的 DLL / USB 状态；LIN 初始化用 `isToomossUSBOpened()` 复用已打开 handle。
- Darwin Toomoss：CAN 与 LIN 目前是两套 CGO 加载路径；**不要在两个文件里重复定义** `DevHandle`、`UsbScan`、`usbClose`、`ensureToomossLoaded` 等同名 Go 符号。
- 无硬件时优先改接口与纯 Go 包（`tp_layer`、`tplin`、`liniface`、测试 mock），不要假定本机有 DLL/dylib。

## Commands

```bash
go test ./...
go test ./tp_layer ./uds_client ./tplin ./uds_client_lin ./preset ./liniface
go test ./can_driver -tags=windows   # 仅在 Windows 上有意义
```

Darwin Toomoss 需要 `CGO_ENABLED=1` 且本机有 TCANLINPro 的 dylib。

## Coding conventions

- 匹配周围文件的命名、错误包装（`fmt.Errorf("...: %w", err)`）和锁粒度；不要引入无关重构。
- 驱动构造失败要释放本函数打开的 USB/session；已复用的 handle 不要在失败路径里关掉。
- LIN 通道用 `liniface.Channel`；硬件 API 参数常用 `byte`，边界处显式转换。
- 不要把 `byte` 类型别名成 `liniface.Channel`。
- 日志：CAN/LIN 帧日志走现有 `printLogEnabled()` / `logLINMessage` 一类开关，不要无条件 `log.Printf` 刷屏。
- 标准 CAN ID 仅 `0x000–0x7FF`；驱动层不支持 29 位扩展帧。
- 用户未明确要求时：不改 `go.mod` 依赖、不主动 commit/push、不写无关 markdown。

## When editing Toomoss LIN

- 以 `driver/toomoss_lin_windows.go` 的 `NewToomossLIN` 为行为基准改 darwin。
- 返回 `*ToomossLIN`，单例由 `toomossInstanceActive` 守护。
- `Close()` 需幂等（`closeOnce`），并清掉 instance active 标志。

## Verification

改完后至少：

1. 对触及的包跑 `go test`
2. 若改了 `//go:build` 文件，确认对应平台能编译（Windows 或 `darwin,cgo`）
3. 确认 import 路径全是 `github.com/LoveWonYoung/atlas/...`
