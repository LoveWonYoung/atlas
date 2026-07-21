# atlas

`atlas` 是一个面向 Go 的 CAN、CAN FD 和 LIN 总线设备与诊断库。CAN 能力来自 canbuskit，LIN 能力已从 linbuskit 合入；同一项目现在可以通过 `device.Init` 使用统一的设备初始化入口。

项目提供：

- Toomoss、TSMaster 等底层设备封装
- CAN / CAN FD 与 LIN 的统一设备初始化入口
- ISO-TP 传输层实现
- LIN Transport Protocol 实现
- UDS over CAN 与 UDS over LIN 客户端
- 常见 UDS 服务封装
- 面向刷写场景的 HEX / SREC 分块辅助能力

项目适合做 ECU 诊断、刷写、自动化测试，以及把不同总线和硬件接入统一的 Go 接口。

## 模块结构

仓库的主要包如下：

- `device`：CAN / LIN 共用的设备初始化和生命周期入口
- `driver`：底层 CAN / CAN FD 驱动
- `lindriver`：底层 LIN 驱动
- `liniface`：LIN 驱动公共接口和帧类型
- `tp_layer`：ISO-15765-2 传输层，实现单帧、多帧、流控、超时管理
- `tplin`：LIN 传输层及主站、从站、模拟网络
- `uds_client`：UDS over CAN 和 UDS over LIN 客户端
- `services`：对常见 UDS 服务做了更高层封装

如果现成服务不够用，也可以直接调用 `UDSClient.Request(...)` 发送任意 SID。

## 已支持的设备

### 本地硬件驱动

- `driver.NewToomoss(...)`
  - Windows
  - macOS（`darwin && cgo`）
- `driver.NewTSMaster(...)`
  - Windows
- `driver.NewPCAN(...)`
  - Windows
- `driver.NewVector(...)`
  - Windows
- `driver.NewAutoDriver(...)`
  - Windows
  - 按 `Toomoss -> TSMaster -> PCAN -> Vector` 顺序自动选择第一个可用设备

LIN 驱动：

- `lindriver.NewToomoss(...)`：Windows、macOS（`darwin && cgo`）
- `lindriver.NewTSMaster(...)`：Windows
- `lindriver.NewMockDriver()`：所有平台，用于测试

新代码建议优先使用 `device.Init(...)`，只有需要厂商专有能力时才直接构造底层驱动。

## 安装

```bash
go get github.com/LoveWonYoung/atlas
```

## 统一初始化

CAN、CAN FD 和 LIN 共用一个初始化函数。返回的 `Device` 统一负责关闭底层资源。

CAN FD + Toomoss：

```go
dev, err := device.Init(device.Config{
    Bus:      device.BusCAN,
    Provider: device.ProviderToomoss,
    CAN: device.CANConfig{
        Type:    driver.CANFD,
        Channel: driver.CHANNEL1,
    },
})
if err != nil {
    log.Fatal(err)
}
defer dev.Close()

canDriver := dev.CANDriver()
```

LIN + Toomoss：

```go
dev, err := device.Init(device.Config{
    Bus:      device.BusLIN,
    Provider: device.ProviderToomoss,
    LIN: device.LINConfig{
        Channels: []liniface.Channel{0, 1},
        BaudRate: 19_200,
        Mode:     device.LINMaster,
    },
})
if err != nil {
    log.Fatal(err)
}
defer dev.Close()

linDriver := dev.LINDriver()
client := uds_client.NewClient(linDriver, 0x01)
defer client.Close()
```

`ProviderAuto` 在 Windows 的 CAN 模式下按 Toomoss、TSMaster、PCAN、Vector 的顺序探测。未填写 LIN 通道、波特率和模式时，默认使用通道 0、19.2 kbit/s、主站模式。

## 快速开始

下面示例演示一个典型链路：

`CAN Driver -> Adapter -> ISO-TP -> UDS Client -> UDS Service`

```go
package main

import (
	"fmt"
	"log"

    "github.com/LoveWonYoung/atlas/driver"
    "github.com/LoveWonYoung/atlas/services"
    isotp "github.com/LoveWonYoung/atlas/tp_layer"
    "github.com/LoveWonYoung/atlas/uds_client"
)

func main() {
	dev := driver.NewToomoss(driver.CANFD, driver.CHANNEL1)

	addr, err := isotp.NewAddress(
		isotp.Normal11Bit,
		isotp.WithTxID(0x7C6),
		isotp.WithRxID(0x7C7),
	)
	if err != nil {
		log.Fatal(err)
	}

	client, err := uds_client.NewUDSClient(dev, addr, isotp.DefaultConfig())
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	client.SetFDMode(true)

	rdbi := services.NewReadDataByIdentifier(client)
	resp, err := rdbi.ReadDataByIdentifier(0xF190)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("VIN: %X\n", resp.Values[0xF190])
}
```

如果你在 Windows 下希望自动挑选本机可用设备，可以把驱动替换成：

```go
dev := driver.NewAutoDriver(driver.CANFD)
```

## CAN 寻址与 ISO-TP 配置

`tp_layer` 支持多种寻址模式：

- `Normal11Bit`
- `Normal29Bit`
- `NormalFixed29Bit`
- `Extended11Bit`
- `Extended29Bit`
- `Mixed11Bit`
- `Mixed29Bit`

基础配置来自：

```go
cfg := isotp.DefaultConfig()
```

你可以按需覆盖：

- `PaddingByte`
- `TimeoutN_As / N_Bs / N_Cs`
- `TimeoutN_Ar / N_Br / N_Cr`
- `BlockSize`
- `StMin`

## UDS 客户端能力

CAN 使用 `uds_client.UDSClient`，负责：

- 请求发送与响应接收
- 超时管理
- `0x7F` 负响应解析
- `0x78 Response Pending` 自动继续等待
- 可重试负响应的有限重试
- 物理地址 / 功能地址切换
- CAN / CAN FD 切换

常用方法：

- `Request(payload []byte)`
- `RequestWithTimeout(payload, timeout)`
- `RequestWithContext(ctx, payload, opts)`
- `SendAndRecv(payload, timeout)`
- `SetFDMode(isFD bool)`
- `SetFunctionalAddress(addr)`
- `UseFunctionalAddress()`
- `UsePhysicalAddress()`

例如，直接发送一个未封装的 UDS 请求：

```go
resp, err := client.Request([]byte{0x10, 0x03})
```

LIN 使用 `uds_client.Client`，底层连接 `tplin`：

```go
client := uds_client.NewClient(linDriver, 0x01)
defer client.Close()

responseNAD, resp, err := client.SendAndRec(
    []byte{0x22, 0xF1, 0x89},
    2*time.Second,
)
```

`tplin.NewMaster`、`tplin.NewSlave` 和 `tplin.NewSimulatedLinNetwork` 也可以用于更底层的 LIN 主从通信与无硬件测试。

## 已封装的 UDS 服务

`services` 目录目前包含：

- `ReadDataByIdentifier` (`0x22`)
- `RoutineControl` (`0x31`)
- `RequestDownload` (`0x34`)
- `TransferData` (`0x36`)
- `RequestTransferExit` (`0x37`)
- `SecurityAccess` (`0x27`)

示例：读取多个 DID

```go
rdbi := services.NewReadDataByIdentifier(client)

resp, err := rdbi.ReadDataByIdentifierWithLengths(
	map[uint16]int{
		0xF187: 16,
		0xF190: 17,
	},
	0xF187,
	0xF190,
)
if err != nil {
	log.Fatal(err)
}

fmt.Printf("DID F187: %X\n", resp.Values[0xF187])
fmt.Printf("DID F190: %X\n", resp.Values[0xF190])
```

## 刷写流程示例

仓库已经提供了刷写链路里最常见的几个步骤封装：

1. `RequestDownload`
2. `TransferData`
3. `RequestTransferExit`

同时支持把 HEX / SREC 文件解析成分段和分块。

```go
reqDownload := services.NewRequestDownload(client)
transfer := services.NewTransferData(client)
exit := services.NewRequestTransferExit(client)

downloadResp, err := reqDownload.RequestDownload(0x00100000, 0x00002000, 4, 4)
if err != nil {
	log.Fatal(err)
}

fmt.Printf("ECU max block len: %d\n", downloadResp.MaxLength)

_, nextSeq, err := transfer.TransferHexFile("./app.hex", 256, 1)
if err != nil {
	log.Fatal(err)
}

fmt.Printf("next sequence: 0x%02X\n", nextSeq)

_, err = exit.RequestTransferExit(nil)
if err != nil {
	log.Fatal(err)
}
```

如果你只想解析文件，不立刻发送，也可以直接使用：

- `ParseHexSegments`
- `MyHexParser`
- `MyHexParserWithLengths`

支持按扩展名或内容自动识别：

- Intel HEX
- SREC / S19 / S28 / S37

## SecurityAccess 说明

`services.SecurityAccess` 在不同平台行为不同：

- Windows：通过 `SecKey.dll` 加载 `SecKeyCmac` 计算 key
- 非 Windows：提供 stub，实现会返回不支持错误

如果你的项目依赖 `SecurityAccess`，需要自行准备匹配 ECU 算法的 `SecKey.dll`。

## 注意事项

- `driver` 的 `Write(id, fd, data)` 通过 `fd` 标志发送 CAN / CAN FD。
- `lindriver` 实现 `liniface.Driver`；LIN 通道从 0 开始编号。
- `device.Device` 一次只表示一种总线连接；需要 CAN 和 LIN 时分别调用 `device.Init`，并分别关闭。
- `services` 只封装了部分常见 UDS 服务；其他服务建议直接用 `UDSClient.Request(...)`。
- `UDSClient.Close()` 会同时关闭后台 goroutine 和底层设备连接，使用结束后应主动调用。

## 测试

```bash
go test ./...
```

当前仓库已经包含 `tp_layer`、`uds_client` 的测试。

## License

[MIT]
